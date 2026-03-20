# AGENTS.md

> Guidelines for AI agents working in this repository.

## Project Overview

**bonk** is a macOS CLI tool that opens apps when you physically bonk an Apple Silicon MacBook. It reads the built-in accelerometer, detects 1/2/3 bonk patterns, and launches apps via `open -a`. Single-file Go application, no audio dependencies.

- **Platform**: macOS on Apple Silicon (M2+) only
- **Runtime requirement**: `sudo` (for IOKit HID accelerometer access)
- **Architecture**: Single `main.go` file

## Commands

### Build & Run

```bash
# Build
go build -o bonk .

# Create default config
./bonk init

# Run (requires sudo)
sudo ./bonk
sudo ./bonk --config /path/to/config.yaml
sudo ./bonk --verbose   # show raw bonk events

# Install as always-on launchd daemon (requires sudo)
sudo ./bonk service install
sudo ./bonk service status
sudo ./bonk service uninstall
```

### Install

```bash
go install github.com/hriprsd/bonk@latest
sudo cp "$(go env GOPATH)/bin/bonk" /usr/local/bin/bonk
```

### Release

Releases are automated via GitHub Actions + GoReleaser when a `v*` tag is pushed:

```bash
git tag v1.0.0
git push origin v1.0.0
```

## Code Organization

```
bonk/
├── main.go              # All application code (single file)
├── doc/
│   ├── logo.png
│   └── config.example.yaml
├── go.mod
├── .goreleaser.yaml     # Release configuration
└── .github/workflows/   # CI/CD
```

## Key Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/taigrr/apple-silicon-accelerometer` | Reads accelerometer via IOKit HID |
| `github.com/spf13/cobra` | CLI framework |
| `github.com/charmbracelet/fang` | CLI config/execution wrapper |
| `gopkg.in/yaml.v3` | Config file parsing |

## Config Format

User config lives at `~/.bonk.yaml`:

```yaml
bindings:
  1: "Spotify"      # single bonk
  2: "Terminal"     # double bonk
  3: "Safari"       # triple bonk

settings:
  min_amplitude: 0.05
  pattern_window_ms: 500
  cooldown_ms: 1000
```

## Code Patterns

### Pattern Detection Flow

1. `sensor.Run()` reads accelerometer in a background goroutine
2. Data shared via `shm.RingBuffer` (POSIX shared memory)
3. `detector.New()` processes samples with vibration detection algorithms
4. Events are fed into `patternDetector` which collects bonks within a time window
5. When the window expires, `onPattern(n)` fires and launches the bound app via `exec.Command("open", "-a", app)`

### Key Types

- `Config` — loaded from YAML, holds `Bindings` map and `Settings`
- `patternDetector` — collects bonk events within a window, fires callback with count
- `listen` — main event loop

### Concurrency

- `patternDetector.mu sync.Mutex` protects tap count and timer
- `lastPatternMu sync.Mutex` protects cooldown timestamp
- App launch runs in goroutines (`go launchApp(...)`)

## Constants

| Constant | Value | Purpose |
|----------|-------|---------|
| `defaultSensorPollInterval` | 10ms | Accelerometer polling rate |
| `defaultMaxSampleBatch` | 200 | Max samples processed per tick |
| `sensorStartupDelay` | 100ms | Wait after sensor init |

## Gotchas

1. **Root required**: The app must run with `sudo` for IOKit HID access. `run()` checks `os.Geteuid() != 0`.

2. **Apple Silicon only**: Only builds for `darwin/arm64`. Intel Macs not supported.

3. **Private dependency**: `github.com/taigrr/apple-silicon-accelerometer` requires `GOPRIVATE=github.com/taigrr/apple-silicon-accelerometer` and a GitHub PAT in CI.

4. **Single file**: All code is in `main.go`. Follow existing patterns when adding features.

5. **CGO disabled**: Builds use `CGO_ENABLED=0`.

6. **Service plist path**: `bonk service install` writes to `/Library/LaunchDaemons/com.hriprsd.bonk.plist` (root-owned).

## Version

Version is injected via ldflags at build time:

```go
var version = "dev"
```

GoReleaser sets `-X main.version={{.Version}}` during release builds.
