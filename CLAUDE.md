# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Viam module (`vijayvuyyuru:wled:wled`) that controls a WLED-compatible ESP32 LED controller. Registered as a Generic service (`rdk:service:generic`). Written in Go.

The module is the **sole transport owner** for all LED communication — no visualizer module should call the WLED device directly. It provides two transport paths:

- **HTTP JSON API** — for state control, WLED built-in effects, on/off, brightness
- **E1.31 sACN (UDP)** — for real-time per-pixel frame data across 3 universes (144 LEDs each)

## Build & Run

```bash
go build -o bin/wled ./cmd/module/   # build the module binary
go build ./...                        # verify all packages compile
```

The binary entrypoint is `cmd/module/main.go`. Module metadata is in `meta.json`.

## Architecture

### Files

| File | Purpose |
|------|---------|
| `module.go` | Resource struct, config, constructor (`NewWled`), `DoCommand` routing, lifecycle |
| `wled.go` | HTTP layer — `PostState` (POST /json/state) and `GetState` (GET /json/state) |
| `sacn.go` | sACN layer — transmitter init, `sendFrame`, `enterSACNMode`, `exitSACNMode`, cleanup |
| `cmd/module/main.go` | Binary entrypoint (Viam ModularMain) |
| `cmd/cli/main.go` | CLI testing harness |

### Config Attributes

```json
{
  "wled_ip": "wled-7f091c.local",
  "wled_port": 80,
  "brightness": 0.6,
  "http_timeout": 10
}
```

- `wled_ip` (string, required) — IP or mDNS hostname of the WLED device
- `wled_port` (int, optional, default 80) — HTTP port
- `brightness` (float, optional) — initial brightness 0.0-1.0, applied on startup
- `http_timeout` (float, optional, default 10) — HTTP request timeout in seconds

### DoCommand Shapes

**Shape A — Global commands** (`"command"` key):
- `"off"` — exits sACN mode, POSTs `{"on": false, "lor": 0}`
- `"on"` — exits sACN mode, POSTs `{"on": true, "lor": 0}`
- `"status"` — GETs `/json/state`, returns raw WLED response
- `"frame"` — sends per-pixel RGB data via sACN (Shape C)

**Shape B — Passthrough** (no `"command"` key):
- Entire map forwarded to `POST /json/state` with `lor: 0` injected
- Used for WLED segment effects (fx, sx, ix, col, pal)

**Shape C — Frame** (`"command": "frame"`):
```json
{"command": "frame", "rings": {"0": [R,G,B,...], "1": [...], "2": [...]}}
```
- Each ring: flat RGB array, 144 pixels x 3 = 432 values
- Ring "0" → universe 1, "1" → universe 2, "2" → universe 3

### sACN ↔ HTTP Mode Switching

The module manages switching between sACN (realtime per-pixel) and HTTP (WLED effects) transparently. WLED's `lor` (Live data Override) field controls which mode is active.

**Startup**: sACN transmitter initialized eagerly. Black frames + `lor: 0` sent immediately to prevent keep-alive packets from locking WLED into realtime mode.

**Frame command arrives** (`enterSACNMode`):
- Sets `lor: 1` via HTTP (only on first frame, tracked by `sacnActive` bool)
- Sends pixel data to sACN universe channels

**HTTP command arrives** (`exitSACNMode`):
- Sends black frames on all 3 universes
- Sets `lor: 0` via HTTP
- Resets `sacnActive` to false
- Every HTTP command also includes `lor: 0` as belt-and-suspenders

**Thread safety**: `sacnMu` mutex protects `sacn` and `sacnActive` fields. The sACN transmitter stays alive for the module's lifetime — only the `lor` flag toggles.

**Known go-sacn library issue**: The library has a concurrent map access bug on shutdown. Channels are closed sequentially with 50ms pauses between each to let internal goroutines drain.

### WLED Device

- Hostname: `wled-7f091c.local` (mDNS)
- 3 segments: 0-144, 144-288, 288-432 (3 strips of 144 WS2812B)
- Segments must be configured correctly in the WLED web UI

## Key Dependencies

- `go.viam.com/rdk` — Viam module SDK
- `github.com/Hundemeier/go-sacn` — E1.31 sACN UDP transmitter
- Standard library `net/http` for WLED HTTP API
