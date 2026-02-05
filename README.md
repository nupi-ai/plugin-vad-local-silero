# VAD Local Silero

Local voice activity detection adapter for Nupi, powered by [Silero VAD v5](https://github.com/snakers4/silero-vad).

## Quick Start

```bash
# Download dependencies
make download-model
make download-ort

# Build (production, requires ORT)
make build

# Build (development, no ORT needed)
make build-stub

# Run tests
make test           # stub only
make test-silero    # with silero (requires ORT + model)
```

## Configuration

Environment variables (or JSON config):

| Variable | Default | Description |
|----------|---------|-------------|
| `NUPI_VAD_ENGINE` | `auto` | Engine selection (see below) |
| `NUPI_VAD_THRESHOLD` | `0.5` | Speech confidence threshold [0.0-1.0] |
| `NUPI_VAD_MIN_SPEECH_DURATION_MS` | `250` | Min speech duration before START event [1-60000 ms] |
| `NUPI_VAD_MIN_SILENCE_DURATION_MS` | `300` | Min silence duration before END event [1-60000 ms] |
| `NUPI_ORT_LIB_PATH` | (auto) | Explicit path to ONNX Runtime library |
| `NUPI_DEV_MODE` | - | Set to `1` to enable CWD-based library lookup and auto fallback |

### Engine Selection

| Value | Behavior |
|-------|----------|
| `auto` | Uses Silero if available and working, falls back to stub on failure (best for development) |
| `silero` | Requires native Silero engine, exits on failure (use in production) |
| `stub` | Deterministic test engine, ignores audio content |

**Build variants:**
- `make build` compiles with `-tags silero` (production)
- `make build-stub` compiles without tags (development/testing)

**Auto mode behavior:**
- If Silero is not compiled in → uses stub (warning logged)
- If Silero is compiled but ORT fails:
  - With `NUPI_DEV_MODE=1` → falls back to stub (warning logged)
  - Without `NUPI_DEV_MODE` → **exits with error** (production-safe default)
- Set `NUPI_VAD_ENGINE=silero` explicitly to always require native engine

## Supported Platforms

| OS | Architecture | Status |
|----|--------------|--------|
| macOS | arm64, x86_64 | Supported |
| Linux | x64, arm64 | Supported |
| Windows | x64, arm64 | Supported |

On Windows, run `download-ort.sh` via Git Bash or WSL.

## Audio Format

- Sample rate: 16kHz
- Encoding: PCM signed 16-bit little-endian (s16le)
- Channels: mono

## Streaming Protocol

**Per-stream configuration (`config_json`):**
- Can be sent in any message before the first PCM chunk
- Locked once the first PCM chunk is received
- Sending `config_json` after audio starts is ignored (warning logged)

## Development

```bash
# Run go mod tidy (preserves silero dependencies)
make tidy

# Never use bare "go mod tidy" - it removes onnxruntime_go

# Run from source (enable CWD-based ORT lookup)
NUPI_DEV_MODE=1 go run -tags silero ./cmd/adapter/
```

**Security note:** CWD-based library lookup is disabled by default to prevent shared library hijacking. Use `NUPI_DEV_MODE=1` only during development.
