// bonk opens apps when you physically bonk your laptop.
// It reads the Apple Silicon accelerometer directly via IOKit HID —
// no separate sensor daemon required. Needs sudo.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
	"github.com/taigrr/apple-silicon-accelerometer/detector"
	"github.com/taigrr/apple-silicon-accelerometer/sensor"
	"github.com/taigrr/apple-silicon-accelerometer/shm"
	"gopkg.in/yaml.v3"
)

var version = "dev"

// Config holds the user's bonk-to-app mappings and tuning settings.
// It is loaded from a YAML file (default: ~/.bonk.yaml).
type Config struct {
	// Bindings maps number of bonks to the app name to open.
	// Example: 1 -> "Spotify", 2 -> "Terminal", 3 -> "Safari"
	Bindings map[int]string `yaml:"bindings"`
	Settings struct {
		// MinAmplitude is the minimum g-force to register a bonk (0.0–1.0).
		// Lower values are more sensitive.
		MinAmplitude float64 `yaml:"min_amplitude"`
		// PatternWindowMs is how long (ms) to wait after the first bonk
		// to collect additional bonks before firing the action.
		PatternWindowMs int `yaml:"pattern_window_ms"`
		// CooldownMs is the minimum time (ms) between pattern triggers.
		CooldownMs int `yaml:"cooldown_ms"`
		// DebounceMs is the minimum gap (ms) between individual bonk events.
		// Events closer than this are treated as aftershock from the same hit.
		DebounceMs int `yaml:"debounce_ms"`
	} `yaml:"settings"`
}

func defaultConfig() Config {
	var c Config
	c.Bindings = map[int]string{}
	c.Settings.MinAmplitude = 0.30
	c.Settings.PatternWindowMs = 500
	c.Settings.CooldownMs = 1000
	c.Settings.DebounceMs = 350
	return c
}

// patternDetector collects bonk events within a time window and fires
// once the window expires with no further bonks.
type patternDetector struct {
	mu        sync.Mutex
	count     int
	timer     *time.Timer
	window    time.Duration
	debounce  time.Duration // minimum gap between individual bonks
	lastBonk  time.Time
	onPattern func(n int)
}

func (pd *patternDetector) bonk() {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	now := time.Now()
	// Debounce: ignore events that are aftershock vibrations from the same hit
	if !pd.lastBonk.IsZero() && now.Sub(pd.lastBonk) < pd.debounce {
		return
	}
	pd.lastBonk = now

	pd.count++
	if pd.timer != nil {
		pd.timer.Stop()
	}
	count := pd.count
	pd.timer = time.AfterFunc(pd.window, func() {
		pd.mu.Lock()
		pd.count = 0
		pd.mu.Unlock()
		pd.onPattern(count)
	})
}

var (
	configPath        string
	minAmplitude      float64
	cooldownMs        int
	patternWinMs      int
	verbose           bool
	serviceConfigPath string
)

const plistLabel = "com.hriprsd.bonk"
const plistPath = "/Library/LaunchDaemons/com.hriprsd.bonk.plist"

const plistTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.hriprsd.bonk</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.BinaryPath}}</string>
    {{- if .ConfigPath}}
    <string>--config</string><string>{{.ConfigPath}}</string>
    {{- end}}
  </array>
  <key>KeepAlive</key><true/>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>/tmp/bonk.log</string>
  <key>StandardErrorPath</key><string>/tmp/bonk.err</string>
</dict>
</plist>
`

func buildPlist(binaryPath, configPath string) (string, error) {
	t, err := template.New("plist").Parse(plistTmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, struct {
		BinaryPath string
		ConfigPath string
	}{binaryPath, configPath}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var sensorReady = make(chan struct{})
var sensorErr = make(chan error, 1)

const (
	defaultSensorPollInterval = 10 * time.Millisecond
	defaultMaxSampleBatch     = 200
	sensorStartupDelay        = 100 * time.Millisecond
)

func main() {
	defaultCfgPath := filepath.Join(os.Getenv("HOME"), ".bonk.yaml")

	rootCmd := &cobra.Command{
		Use:   "bonk",
		Short: "Open apps by slapping your laptop",
		Long: `bonk listens to your Apple Silicon accelerometer and launches
apps based on how many times you slap the laptop.

Configure slap patterns in ~/.bonk.yaml.

Requires sudo (for IOKit HID accelerometer access).`,
		Version:      version,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), cmd)
		},
	}

	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "C", defaultCfgPath, "Path to config file")
	rootCmd.Flags().Float64Var(&minAmplitude, "min-amplitude", 0, "Override minimum amplitude threshold (0.0-1.0)")
	rootCmd.Flags().IntVar(&cooldownMs, "cooldown", 0, "Override cooldown between pattern triggers (ms)")
	rootCmd.Flags().IntVar(&patternWinMs, "pattern-window", 0, "Override window to collect bonks into a pattern (ms)")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Print raw bonk events and pattern info")

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Create a default config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			return initConfig(configPath)
		},
	}
	rootCmd.AddCommand(initCmd)

	calibrateCmd := &cobra.Command{
		Use:   "calibrate",
		Short: "Print live accelerometer amplitude so you can tune min_amplitude",
		Long: `Reads the accelerometer and prints every detected event's amplitude in
real-time. Slap your laptop at different intensities to see what values
your hits produce, then set min_amplitude in ~/.bonk.yaml accordingly.

Requires sudo. Press ctrl+c to stop.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCalibrate(cmd.Context())
		},
	}
	rootCmd.AddCommand(calibrateCmd)

	serviceCmd := &cobra.Command{
		Use:   "service",
		Short: "Manage bonk launchd service (always-on daemon)",
	}

	serviceInstallCmd := &cobra.Command{
		Use:   "install",
		Short: "Install and load bonk as a launchd LaunchDaemon (requires sudo)",
		RunE: func(cmd *cobra.Command, args []string) error {
			bin, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolving binary path: %w", err)
			}
			// Auto-detect config path from the invoking user when not specified.
			// When run via sudo, SUDO_USER is set to the real user.
			cfgPath := serviceConfigPath
			if cfgPath == "" {
				home := os.Getenv("HOME")
				if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
					// Resolve the real user's home directory
					if h, err := exec.Command("eval", "echo", "~"+sudoUser).Output(); err == nil && len(h) > 0 {
						home = strings.TrimSpace(string(h))
					} else {
						// Fallback: common macOS path
						home = "/Users/" + sudoUser
					}
				}
				cfgPath = filepath.Join(home, ".bonk.yaml")
			}
			if _, err := os.Stat(cfgPath); err != nil {
				return fmt.Errorf("config not found at %s — run 'bonk init' first (without sudo)", cfgPath)
			}
			plist, err := buildPlist(bin, cfgPath)
			if err != nil {
				return fmt.Errorf("rendering plist: %w", err)
			}
			if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
				return fmt.Errorf("writing plist: %w", err)
			}
			if out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput(); err != nil {
				return fmt.Errorf("launchctl load: %w\n%s", err, out)
			}
			fmt.Printf("bonk: service installed and started.\n  plist: %s\n  logs:  /tmp/bonk.log, /tmp/bonk.err\n  to stop: sudo bonk service uninstall\n", plistPath)
			return nil
		},
	}
	serviceInstallCmd.Flags().StringVar(&serviceConfigPath, "config", "", "Embed config path in plist ProgramArguments")

	serviceUninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Unload and remove bonk launchd service (requires sudo)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if out, err := exec.Command("launchctl", "unload", plistPath).CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "bonk: launchctl unload warning: %v\n%s\n", err, out)
			}
			if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing plist: %w", err)
			}
			fmt.Println("bonk: service uninstalled.")
			return nil
		},
	}

	serviceStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether the bonk launchd service is running",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := exec.Command("launchctl", "list", plistLabel).CombinedOutput()
			if err != nil {
				fmt.Println("bonk service is not installed.")
				return nil
			}
			fmt.Printf("bonk service is running.\n%s", out)
			return nil
		},
	}

	serviceCmd.AddCommand(serviceInstallCmd, serviceUninstallCmd, serviceStatusCmd)
	rootCmd.AddCommand(serviceCmd)

	if err := fang.Execute(context.Background(), rootCmd); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd *cobra.Command) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bonk: could not load config (%v)\n       run 'bonk init' to create a default config at %s\n", err, configPath)
		return err
	}

	// CLI flags override config file values.
	if cmd.Flags().Changed("min-amplitude") {
		cfg.Settings.MinAmplitude = minAmplitude
	}
	if cmd.Flags().Changed("cooldown") {
		cfg.Settings.CooldownMs = cooldownMs
	}
	if cmd.Flags().Changed("pattern-window") {
		cfg.Settings.PatternWindowMs = patternWinMs
	}

	if len(cfg.Bindings) == 0 {
		return fmt.Errorf("no bindings configured in %s\nAdd entries like:\n  bindings:\n    1: \"Spotify\"\n    2: \"Terminal\"", configPath)
	}

	fmt.Printf("bonk: %d binding(s) loaded — amplitude≥%.3fg, window=%dms, cooldown=%dms\n",
		len(cfg.Bindings), cfg.Settings.MinAmplitude, cfg.Settings.PatternWindowMs, cfg.Settings.CooldownMs)
	for n := 1; n <= 5; n++ {
		if app, ok := cfg.Bindings[n]; ok && app != "" {
			fmt.Printf("  %s → %s\n", bonkLabel(n), app)
		}
	}

	if os.Geteuid() != 0 {
		return fmt.Errorf("bonk requires root privileges for accelerometer access, run with: sudo bonk")
	}

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	accelRing, err := shm.CreateRing(shm.NameAccel)
	if err != nil {
		return fmt.Errorf("creating accel shm: %w", err)
	}
	defer accelRing.Close()
	defer accelRing.Unlink()

	go func() {
		close(sensorReady)
		if err := sensor.Run(sensor.Config{
			AccelRing: accelRing,
			Restarts:  0,
		}); err != nil {
			sensorErr <- err
		}
	}()

	select {
	case <-sensorReady:
	case err := <-sensorErr:
		return fmt.Errorf("sensor worker failed: %w", err)
	case <-ctx.Done():
		return nil
	}

	time.Sleep(sensorStartupDelay)
	return listen(ctx, cfg, accelRing)
}

func listen(ctx context.Context, cfg Config, accelRing *shm.RingBuffer) error {
	det := detector.New()
	var lastAccelTotal uint64
	var lastEventTime time.Time
	var lastPatternMu sync.Mutex
	var lastPattern time.Time

	cooldown := time.Duration(cfg.Settings.CooldownMs) * time.Millisecond
	patternWindow := time.Duration(cfg.Settings.PatternWindowMs) * time.Millisecond

	debounce := time.Duration(cfg.Settings.DebounceMs) * time.Millisecond

	pd := &patternDetector{
		window:   patternWindow,
		debounce: debounce,
		onPattern: func(n int) {
			now := time.Now()

			lastPatternMu.Lock()
			elapsed := now.Sub(lastPattern)
			if elapsed < cooldown {
				lastPatternMu.Unlock()
				if verbose {
					fmt.Printf("bonk: %s ignored (cooldown, %.0fms remaining)\n",
						bonkLabel(n), (cooldown - elapsed).Seconds()*1000)
				}
				return
			}
			lastPattern = now
			lastPatternMu.Unlock()

			app, ok := cfg.Bindings[n]
			if !ok || app == "" {
				if verbose {
					fmt.Printf("bonk: %s — no binding configured\n", bonkLabel(n))
				}
				return
			}

			fmt.Printf("bonk: %s → opening %s\n", bonkLabel(n), app)
			go launchApp(app)
		},
	}

	fmt.Println("bonk: listening... (ctrl+c to quit)")
	ticker := time.NewTicker(defaultSensorPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nbonk: bye!")
			return nil
		case err := <-sensorErr:
			return fmt.Errorf("sensor worker failed: %w", err)
		case <-ticker.C:
		}

		now := time.Now()
		tNow := float64(now.UnixNano()) / 1e9

		samples, newTotal := accelRing.ReadNew(lastAccelTotal, shm.AccelScale)
		lastAccelTotal = newTotal
		if len(samples) > defaultMaxSampleBatch {
			samples = samples[len(samples)-defaultMaxSampleBatch:]
		}

		nSamples := len(samples)
		for idx, sample := range samples {
			tSample := tNow - float64(nSamples-idx-1)/float64(det.FS)
			det.Process(sample.X, sample.Y, sample.Z, tSample)
		}

		if len(det.Events) == 0 {
			continue
		}

		ev := det.Events[len(det.Events)-1]
		if ev.Time.Equal(lastEventTime) {
			continue
		}
		lastEventTime = ev.Time

		if ev.Amplitude < cfg.Settings.MinAmplitude {
			continue
		}

		if verbose {
			fmt.Printf("bonk: detected [amp=%.5fg severity=%s]\n", ev.Amplitude, ev.Severity)
		}
		pd.bonk()
	}
}

func runCalibrate(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("bonk calibrate requires root privileges, run with: sudo bonk calibrate")
	}

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	accelRing, err := shm.CreateRing(shm.NameAccel)
	if err != nil {
		return fmt.Errorf("creating accel shm: %w", err)
	}
	defer accelRing.Close()
	defer accelRing.Unlink()

	calSensorReady := make(chan struct{})
	calSensorErr := make(chan error, 1)

	go func() {
		close(calSensorReady)
		if err := sensor.Run(sensor.Config{
			AccelRing: accelRing,
			Restarts:  0,
		}); err != nil {
			calSensorErr <- err
		}
	}()

	select {
	case <-calSensorReady:
	case err := <-calSensorErr:
		return fmt.Errorf("sensor worker failed: %w", err)
	case <-ctx.Done():
		return nil
	}

	time.Sleep(sensorStartupDelay)

	fmt.Println("bonk calibrate: slap your laptop at different intensities.")
	fmt.Println("  Events below 0.01g are filtered (noise).")
	fmt.Println("  Press ctrl+c to stop.")

	det := detector.New()
	var lastAccelTotal uint64
	var lastEventTime time.Time

	ticker := time.NewTicker(defaultSensorPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nbonk calibrate: done.")
			return nil
		case err := <-calSensorErr:
			return fmt.Errorf("sensor worker failed: %w", err)
		case <-ticker.C:
		}

		now := time.Now()
		tNow := float64(now.UnixNano()) / 1e9

		samples, newTotal := accelRing.ReadNew(lastAccelTotal, shm.AccelScale)
		lastAccelTotal = newTotal
		if len(samples) > defaultMaxSampleBatch {
			samples = samples[len(samples)-defaultMaxSampleBatch:]
		}

		nSamples := len(samples)
		for idx, sample := range samples {
			tSample := tNow - float64(nSamples-idx-1)/float64(det.FS)
			det.Process(sample.X, sample.Y, sample.Z, tSample)
		}

		if len(det.Events) == 0 {
			continue
		}

		ev := det.Events[len(det.Events)-1]
		if ev.Time.Equal(lastEventTime) {
			continue
		}
		lastEventTime = ev.Time

		if ev.Amplitude < 0.01 {
			continue
		}

		label := "light"
		switch {
		case ev.Amplitude >= 0.50:
			label = "HARD SLAP"
		case ev.Amplitude >= 0.30:
			label = "slap"
		case ev.Amplitude >= 0.15:
			label = "firm tap"
		case ev.Amplitude >= 0.05:
			label = "tap"
		}
		fmt.Printf("  %.3fg  (%s)\n", ev.Amplitude, label)
	}
}

func launchApp(name string) {
	if err := exec.Command("open", "-a", name).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "bonk: failed to open %q: %v\n", name, err)
	}
}

func bonkLabel(n int) string {
	return fmt.Sprintf("%dx slap", n)
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", path, err)
	}
	return cfg, nil
}

func initConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("bonk: config already exists at %s\n", path)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	const template = `# bonk config — maps slap patterns to apps
# Run: sudo bonk
# Tip: run 'sudo bonk calibrate' to see what amplitude your slaps produce

bindings:
  1: "Spotify"      # 1x slap
  2: "Terminal"     # 2x slap
  3: "Safari"       # 3x slap
  # 4: "Finder"    # 4x slap (uncomment to enable)
  # 5: "Calendar"  # 5x slap

settings:
  # Minimum g-force to register a slap (lower = more sensitive).
  # Use 'sudo bonk calibrate' to find the right value for your machine.
  min_amplitude: 0.30

  # How long (ms) to wait after the first slap for more slaps
  # before firing the action. Increase if double-slaps aren't
  # registering reliably.
  pattern_window_ms: 500

  # Minimum time (ms) between pattern triggers (prevents accidental
  # double-firing from vibration).
  cooldown_ms: 1000

  # Minimum gap (ms) between individual slap events. Events closer
  # than this are treated as aftershock vibration from the same hit.
  debounce_ms: 350
`

	if err := os.WriteFile(path, []byte(template), 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	fmt.Printf("bonk: created config at %s\n       edit it to set your bindings, then run: sudo bonk\n", path)
	return nil
}
