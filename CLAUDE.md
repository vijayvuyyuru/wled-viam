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
| `sacn.go` | sACN layer — `initSACN` (lazy on first frame), `sendFrame`, `teardownSACN`, channel cleanup |
| `segments.go` | Segment config + validation + startup reconciliation + `get_segments` read |
| `cmd/module/main.go` | Binary entrypoint (Viam ModularMain) |
| `cmd/cli/main.go` | CLI testing harness |

### Config Attributes

```json
{
  "wled_ip": "wled-7f091c.local",
  "wled_port": 80,
  "brightness": 0.6,
  "http_timeout": 10,
  "segments": [
    {"id": 0, "start": 0, "stop": 144},
    {"id": 1, "start": 144, "stop": 288},
    {"id": 2, "start": 288, "stop": 432}
  ]
}
```

- `wled_ip` (string, required) — IP or mDNS hostname of the WLED device
- `wled_port` (int, optional, default 80) — HTTP port
- `brightness` (float, optional) — initial brightness 0.0-1.0, applied on startup
- `http_timeout` (float, optional, default 10) — HTTP request timeout in seconds
- `segments` (array, **required**) — list of WLED segments to reconcile onto the device at startup. Each entry has `id` (int, contiguous starting at 0), `start` and `stop` (ints, `stop > start`, non-overlapping), and optional `rev` (bool), `grp` (int, default 1), `spc` (int, default 0). Validation rejects empty arrays, duplicate IDs, gaps in IDs, overlapping ranges, and counts exceeding 16 (WLED firmware limit).

### DoCommand Shapes

**Shape A — Global commands** (`"command"` key):
- `"off"` — POSTs `{"on": false}` to `/json/state`. Does not teardown sACN.
- `"on"` — POSTs `{"on": true}` to `/json/state`. Does not teardown sACN.
- `"status"` — GETs `/json/state`, returns raw WLED response
- `"frame"` — sends per-pixel RGB data via sACN (Shape C)
- `"get_segments"` — returns `{"segments": [{"id", "start", "stop", "len", "rev", "grp", "spc"}, ...]}` reflecting the module's cached segment config. Does not query the device. Consumers in spotifyViz call this on resource start to learn the canonical strip geometry.

**Shape B — Passthrough** (no `"command"` key):
- Calls `teardownSACN` (closes the sACN transmitter so WLED can revert to HTTP-effect rendering), then forwards the entire map as-is to `POST /json/state`
- Used for WLED segment effects (fx, sx, ix, col, pal)

**Shape C — Frame** (`"command": "frame"`):
```json
{"command": "frame", "rings": {"0": [R,G,B,...], "1": [...], "2": [...]}}
```
- Each ring: flat RGB array, 144 pixels x 3 = 432 values
- Ring "0" → universe 1, "1" → universe 2, "2" → universe 3

### Segment Sync

The WLED device loses segment configuration on power cycle. The module treats the Viam `segments` config as the declared source of truth and reconciles it onto the device on every `NewWled` call.

**Startup**: After brightness, the module POSTs `{"seg": [<configured segments>, <stop:0 deletion entries for ids N..15>]}` to `/json/state`. This is one HTTP call that both installs configured segments and removes any stale extras (deleting a non-existent segment is a no-op on WLED's side).

**`get_segments` DoCommand**: returns the cached config in a consumer-friendly shape that includes the computed `len` field. Does **not** query the device — the cache is authoritative because `applySegmentConfig` keeps the device in sync with it.

**Why not save to flash?** WLED supports persisting state via `nsave`, but that adds flash wear and lets device state silently drift from Viam config. The "always reconcile on start" approach trades one extra HTTP call per reload for a deterministic state model.

### sACN ↔ HTTP Mode Switching

The module manages switching between sACN (realtime per-pixel) and HTTP (WLED effects) by owning the sACN transmitter lifecycle. The transmitter is lazily created and torn down on demand — the absence of sACN packets is what lets WLED fall back to HTTP-effect rendering.

**Startup**: sACN transmitter is NOT created. Only segments and brightness are pushed via HTTP. No sACN packets are emitted until a frame command arrives.

**Frame command arrives** (`sendFrame`):
- Under `sacnMu`, checks if `s.sacn` is nil; if so calls `initSACN` to build the transmitter and activate universes 1–3 unicast to the WLED device
- Parses `cmd["rings"]` and pushes DMX bytes into the per-universe channels
- Subsequent frames reuse the existing transmitter

**Shape B passthrough arrives** (`teardownSACN`):
- Under `sacnMu`, closes the sACN transmitter and nils `s.sacn`
- The map (e.g. effect commands like `{"on": true, "seg": [...]}`) is then forwarded unchanged to `POST /json/state`
- With no live sACN traffic, WLED renders the HTTP effect

**`off` / `on` commands**: Do NOT touch sACN state — they only flip the global on/off. If a frame command was in flight, calling `off` then `frame` reuses the still-alive transmitter.

**Thread safety**: `sacnMu` mutex protects the `sacn` field (a `*sacnState`). The transmitter is recreated as needed rather than kept alive for the module lifetime.

**Known go-sacn library issue**: The library has a concurrent map access bug on shutdown. Channels are closed sequentially with 50ms pauses between each to let internal goroutines drain.

### WLED Device

- Hostname: `wled-7f091c.local` (mDNS)
- 3 segments: 0-144, 144-288, 288-432 (3 strips of 144 WS2812B)
- Segments are reconciled by the module on every start via the `segments` config (see Segment Sync). No manual WLED web UI configuration required.

## Key Dependencies

- `go.viam.com/rdk` — Viam module SDK
- `github.com/Hundemeier/go-sacn` — E1.31 sACN UDP transmitter
- Standard library `net/http` for WLED HTTP API
