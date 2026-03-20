<p align="center">
  <img src="https://raw.githubusercontent.com/hriprsd/bonk/master/doc/logo.png" alt="bonk logo" width="200">
</p>

# bonk

Bonk your MacBook to open apps.

Uses the Apple Silicon accelerometer (Bosch BMI286 IMU via IOKit HID) to detect 1/2/3 bonk patterns on your laptop and launch apps via `open -a`. Single binary, no dependencies.

## Requirements

- macOS on Apple Silicon (M2+)
- `sudo` (for IOKit HID accelerometer access)
- Go 1.26+ (if building from source)

## Install

Download from the [latest release](https://github.com/hriprsd/bonk/releases/latest).

Or install from source:

```bash
go install github.com/hriprsd/bonk@latest
```

> **Note:** `go install` places the binary in `$GOBIN` (if set) or `$(go env GOPATH)/bin` (defaults to `~/go/bin`). Copy it to a system path so `sudo bonk` works:
>
> ```bash
> sudo cp "$(go env GOPATH)/bin/bonk" /usr/local/bin/bonk
> ```

## Quick Start

```bash
# 1. Generate a default config
bonk init

# 2. Edit the config to map bonk patterns to your apps
nano ~/.bonk.yaml

# 3. Run (requires sudo for accelerometer access)
sudo bonk
```

## Config

`~/.bonk.yaml`:

```yaml
# bonk config — maps bonk patterns to apps
# Copy this to ~/.bonk.yaml, or run: bonk init

bindings:
  1: "Spotify"      # single bonk → open Spotify
  2: "Terminal"     # double bonk → open Terminal
  3: "Safari"       # triple bonk → open Safari
  # 4: "Finder"    # quadruple bonk (uncomment to enable)
  # 5: "Calendar"  # 5x bonk

settings:
  # Minimum g-force to register a bonk (lower = more sensitive)
  # Range: 0.0 (any vibration) to 1.0 (very hard hit only)
  min_amplitude: 0.05

  # How long (ms) to wait after the first bonk for more bonks
  # before firing the action. Increase if double-bonks aren't
  # registering reliably on your desk/surface.
  pattern_window_ms: 500

  # Minimum time (ms) between pattern triggers (prevents accidental
  # double-firing from surface vibration after a bonk).
  cooldown_ms: 1000
```

## CLI Reference

```
bonk [flags]
bonk init
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `~/.bonk.yaml` | Path to config file |
| `--min-amplitude` | `0.05` | Minimum g-force to register a bonk (overrides config) |
| `--cooldown` | `1000` | Cooldown between pattern triggers in ms (overrides config) |
| `--pattern-window` | `500` | Time window in ms to accumulate bonks (overrides config) |
| `--verbose` | `false` | Log detected bonks and fired actions to stdout |

**Subcommand:** `bonk init` — writes the default config to `~/.bonk.yaml` (does not overwrite if file already exists).

## How It Works

1. Opens the Apple Silicon IMU sensor via IOKit HID
2. Continuously reads raw accelerometer data
3. Runs STA/LTA detection to identify impact events above `min_amplitude`
4. Accumulates bonks within the `pattern_window` (e.g., two bonks in 500ms = double-bonk)
5. After the window expires with no new bonks, looks up the count in `bindings`
6. Fires `open -a <AppName>` for the matching binding
7. Waits `cooldown_ms` before accepting the next pattern

## Running as a Service

To start `bonk` automatically at boot:

```bash
sudo tee /Library/LaunchDaemons/com.hriprsd.bonk.plist > /dev/null << 'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.hriprsd.bonk</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/bonk</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/bonk.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/bonk.err</string>
</dict>
</plist>
EOF
```

Load and start:

```bash
sudo launchctl load /Library/LaunchDaemons/com.hriprsd.bonk.plist
```

The plist lives in `/Library/LaunchDaemons`, so launchd runs it as root — no `sudo` needed at runtime.

To stop:

```bash
sudo launchctl unload /Library/LaunchDaemons/com.hriprsd.bonk.plist
```

## License

MIT

<!-- Links -->
[readme-zh-link]: ./README-zh.md
