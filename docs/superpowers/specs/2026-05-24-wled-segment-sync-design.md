# WLED Segment Sync & Metadata Exposure

**Date:** 2026-05-24
**Status:** Draft — pending user review

## Problem

WLED ESP32 controllers do not persist segment configuration through power cycles on the current hardware setup. Every time the shared power supply is cycled, segments revert to a single default span across all LEDs, and the user must reconfigure 3 segments (0–144, 144–288, 288–432) manually in the WLED web UI before any visualizer renders correctly.

Separately, downstream Viam resources in `spotifyViz` (`spotify-viz`, `weather-viz`, `calibration-viz`, `default-viz`) currently encode strip geometry (`strip_length`, ring count) as their own Viam config attributes — duplicating data that conceptually belongs to the WLED layer. This couples each visualizer's config to the LED hardware shape and risks drift when hardware changes.

A near-term hardware change will introduce **heterogeneous segments** — different numbers of LEDs per segment. Any solution must not bake in a uniform-length assumption.

## Goals

1. On WLED module startup, push the configured segment geometry to the device so segments survive power cycles transparently.
2. Expose segment metadata through a new `DoCommand` so downstream resources can fetch the canonical strip geometry instead of duplicating it in their own config.
3. Update `spotifyViz` visualizer resources to fetch segment data on startup and drop their `strip_length` config attribute.
4. Avoid baking in a uniform-segment-length assumption — keep the data model open for heterogeneous segments.

## Non-Goals

- Persisting segments to WLED's flash (`nsave`). Choice: avoid flash wear; treat the WLED device as ephemeral and the Viam module as the durable source of truth.
- Hot-reload of segment changes without a Viam module reload. Choice: AlwaysRebuild already covers this — config changes trigger a new resource instance.
- Updating visualizer rendering logic to use per-ring lengths (album sampler, debug image, calibration patterns). This is **prong 2**, a separate follow-up. This spec ensures prong 1 does not block prong 2.

## Design

### Architecture

```
┌──────────────────────┐      Viam DoCommand        ┌────────────────────────┐
│  spotifyViz resource │  ───────────────────────▶  │  WLED module (Go)      │
│  (spotify_viz,       │  get_segments → segments[] │                        │
│   weather_viz,       │                            │  ┌──────────────────┐  │
│   calibration_viz,   │                            │  │ Config.Segments  │  │
│   default_viz)       │                            │  │ (source of truth)│  │
│                      │                            │  └──────────────────┘  │
│  Stores _segments    │                            │           │            │
│  list from response  │                            │           ▼            │
└──────────────────────┘                            │   applySegmentConfig   │
                                                    │   on NewWled()         │
                                                    └────────────┬───────────┘
                                                                 │ HTTP POST /json/state
                                                                 ▼
                                                          ┌────────────┐
                                                          │ WLED ESP32 │
                                                          └────────────┘
```

The WLED module is the **sole declared source of truth** for segment geometry. The device is treated as ephemeral state to be reconciled on every module start. Downstream resources consume segment metadata from the WLED module — never from their own config or directly from the device.

### Component changes

#### 1. `wled-viam` — Config schema

Add a `Segments` field to `Config`:

```go
type SegmentConfig struct {
    ID    int  `json:"id"`
    Start int  `json:"start"`
    Stop  int  `json:"stop"`
    Rev   bool `json:"rev,omitempty"`  // strip wired in reverse direction
    Grp   int  `json:"grp,omitempty"`  // group N LEDs (WLED default: 1)
    Spc   int  `json:"spc,omitempty"`  // skip N LEDs between groups (WLED default: 0)
}

type Config struct {
    WledIP      string          `json:"wled_ip"`
    WledPort    int             `json:"wled_port,omitempty"`
    Brightness  float64         `json:"brightness,omitempty"`
    HTTPTimeout float64         `json:"http_timeout,omitempty"`
    Segments    []SegmentConfig `json:"segments"`
}
```

`Validate` rules:

- `len(Segments) >= 1` — segments are **required**. No defaults. Empty/missing produces a clear error.
- IDs are unique.
- IDs are contiguous starting at 0 (i.e., `{0, 1, …, N-1}`).
- For every segment: `Stop > Start >= 0`.
- No overlapping ranges (sort by start, verify each `Start[i] >= Stop[i-1]`).
- `Grp` and `Spc` are non-negative if specified.

Validation errors should include the segment index and a description (e.g., `"%s: segments[2]: stop (200) must be greater than start (200)"`).

#### 2. `wled-viam` — Startup sync (`segments.go`)

New file: `segments.go`. Houses `SegmentConfig`, `applySegmentConfig`, `getSegments`, and validation helpers. Keeps `module.go` lifecycle-focused and `wled.go` purely about HTTP transport.

```go
const maxWLEDSegments = 16  // WLED firmware limit; safe upper bound

func (s *wledWled) applySegmentConfig(ctx context.Context) error {
    payload := make([]map[string]interface{}, 0, maxWLEDSegments)
    for _, seg := range s.cfg.Segments {
        grp := seg.Grp
        if grp == 0 {
            grp = 1  // WLED's default; sent explicitly for determinism
        }
        payload = append(payload, map[string]interface{}{
            "id":    seg.ID,
            "start": seg.Start,
            "stop":  seg.Stop,
            "rev":   seg.Rev,
            "grp":   grp,
            "spc":   seg.Spc,
        })
    }
    // Strict delete: mark segment IDs above our count for deletion.
    // Deleting a non-existent segment is a no-op on the WLED side.
    for id := len(s.cfg.Segments); id < maxWLEDSegments; id++ {
        payload = append(payload, map[string]interface{}{
            "id":   id,
            "stop": 0,  // stop:0 deletes the segment
        })
    }
    _, err := s.PostState(ctx, map[string]interface{}{"seg": payload})
    return err
}
```

Called from `NewWled` after brightness application:

```go
// In NewWled, after brightness:
if err := s.applySegmentConfig(ctx); err != nil {
    logger.Warnw("failed to apply segment config", "error", err)
}
```

The current sACN transmitter is lazily initialized on first frame command (see `sacn.go:sendFrame`), so there is no ordering constraint between segment sync and sACN init at startup time. The segment-sync HTTP POST and any subsequent sACN-mode frame both operate on whatever segment layout is on the device after sync completes.

**Failure mode:** log a warning and continue. The module starts even if the device is unreachable (matches the existing initial-brightness pattern in `NewWled`). Next module reload after the device is back online will re-apply segments.

#### 3. `wled-viam` — `get_segments` DoCommand

Add a new case to the Shape A switch in `DoCommand`:

```go
case "get_segments":
    return s.getSegments(), nil
```

```go
func (s *wledWled) getSegments() map[string]interface{} {
    segs := make([]map[string]interface{}, 0, len(s.cfg.Segments))
    for _, seg := range s.cfg.Segments {
        grp := seg.Grp
        if grp == 0 {
            grp = 1
        }
        segs = append(segs, map[string]interface{}{
            "id":    seg.ID,
            "start": seg.Start,
            "stop":  seg.Stop,
            "len":   seg.Stop - seg.Start,
            "rev":   seg.Rev,
            "grp":   grp,
            "spc":   seg.Spc,
        })
    }
    return map[string]interface{}{"segments": segs}
}
```

Returns the cached config, **not** a live GET to the device. Rationale: the module is declared source of truth; a live GET would be circular and adds a network failure surface to every consumer's startup path.

#### 4. `spotifyViz` — shared helper (`led_commands.py`)

Add a fetch helper:

```python
async def fetch_segments(led_module, logger) -> list[dict]:
    """Fetch segment metadata from WLED module. Returns the full segments list.
    Raises if missing or empty — visualizers depend on this for sane operation."""
    result = await led_module.do_command({"command": "get_segments"})
    segs = result.get("segments", [])
    if not segs:
        raise ValueError("WLED module returned empty segments")
    return segs
```

Convert `rainbow_flow_command` (module-level constant with hardcoded 3-segment array) into a builder function:

```python
RAINBOW_FLOW_PARAMS = {"fx": FX_FLOW, "pal": PAL_RAINBOW, "sx": 149, "ix": 138}

def rainbow_flow_for(segments: list[dict]) -> dict:
    return {"on": True, "seg": [{"id": s["id"], **RAINBOW_FLOW_PARAMS} for s in segments]}
```

#### 5. `spotifyViz` — visualizer changes

For each of `spotify_viz.py`, `weather_viz.py`, `calibration_viz.py`, `default_viz.py`:

- **Class attributes:** replace `_strip_length: int = 144` with `_segments: list[dict]` (initialized empty).
- **`new()` method:** after `find_dependency(... led_module ...)`, fetch and store segments:
  ```python
  resource._segments = await fetch_segments(resource.led_module, resource.logger)
  ```
  Requires `new()` to be `async` if it isn't already. If the Viam Python SDK doesn't support `async def new()` at the time of implementation, fall back to a lazy fetch on first use.
- **`validate_config`:** remove any `strip_length` references (currently optional reads, so this is pure removal).
- **Use sites:** mechanical replacement of `self._strip_length` with `self._segments[0]["len"]` and any hardcoded `range(3)` with `range(len(self._segments))`. **This is intentionally a uniform-assumption-preserving change** — prong 2 will replace `[0]` with `[i]` at the sites that care about per-ring length.

Special cases:

- **`weather_viz.py`:** the audit during implementation should verify whether `_strip_length` is read anywhere outside the constructor. If it is dead code (only set, never read), remove the field entirely rather than replace it.
- **`default_viz.py`:** replace the imported `rainbow_flow_command` constant with `rainbow_flow_for(self._segments)` at the call site.

### Data flow on resource startup

```
spotifyViz visualizer .new()
  ├── super().new() — base resource setup
  ├── resolve led_module dependency from `dependencies`
  ├── await fetch_segments(led_module, logger)
  │     └── led_module.do_command({"command": "get_segments"})
  │           └── returns {"segments": [{"id": 0, "start": 0, "stop": 144, "len": 144, ...}, ...]}
  ├── store result in resource._segments
  └── proceed with the rest of init (scheduler, animator, etc.)
```

### Error handling

| Failure | Where | Behavior |
|---------|-------|----------|
| Invalid segment config (overlaps, gaps, etc.) | `Config.Validate` | Reject at config-load time; module never instantiates |
| WLED device unreachable on startup | `applySegmentConfig` | Log warning, continue. Next reload retries. |
| `get_segments` called before module initialized | — | Cannot happen; `Validate` guarantees `Segments` is non-empty |
| spotifyViz fetch returns empty list | `fetch_segments` | Raise ValueError — visualizer fails to start. Loud failure preferred over silent misrender. |
| spotifyViz fetch HTTP error | `fetch_segments` | Propagate Viam SDK exception — visualizer fails to start |

### Testing

#### `wled-viam`

- **Config validation unit tests** (new `segments_test.go`):
  - Empty segments rejected.
  - Duplicate IDs rejected.
  - Non-contiguous IDs (`[0, 2]`) rejected.
  - Overlapping ranges rejected.
  - `Stop <= Start` rejected.
  - Valid 3×144 config accepted.
  - Valid heterogeneous config (e.g., `[{0, 0, 100}, {1, 100, 250}, {2, 250, 432}]`) accepted.
- **Integration test** (new `segments_integration_test.go`):
  - Spin up an `httptest.Server` mimicking WLED's `/json/state` endpoint.
  - Construct the module pointing at it.
  - Assert the POST body matches the expected segment payload (configured segments + `stop:0` deletion entries for IDs `len(segments)..15`).
  - Assert `getSegments` returns the right shape including the computed `len` field.
- **Manual verification on real device:**
  1. Power-cycle WLED.
  2. Open WLED web UI — confirm segments revert.
  3. Reload Viam module.
  4. Confirm WLED web UI shows segments matching config.
  5. Run any spotifyViz Effect-mode visualizer — confirm rendering is correct.

#### `spotifyViz`

- Manual: after rebuild and config update, reload each visualizer resource. Check logs for successful fetch. Verify rendering still works on the existing 3×144 setup.

## Rollout

Phased two-PR rollout:

1. **PR 1 — `wled-viam`:** segment config + startup sync + `get_segments` DoCommand. Backwards-incompatible (new required `segments` field). Update the user's machine config to add the `segments` array before the new module version is deployed.
2. **PR 2 — `spotifyViz`:** visualizers fetch on startup; drop `strip_length` from spotify-viz / weather-viz / calibration-viz components in machine config. default-viz becomes dynamic via `rainbow_flow_for`.

The phased split provides a bisection point — if either prong introduces a regression, rollback affects only one repo.

## Open questions / follow-ups (out of scope here)

- **Prong 2:** per-ring length handling in spotify_viz sACN sampler (`_circular_sample_rings`), debug image rendering, calibration_viz pattern building. The data model in this spec preserves per-ring lengths in `_segments`; prong 2 changes which index those code paths read.
- **`led_commands.py` `RAINBOW_COMET` and any other module-level constants encoding segment shape:** convert to builder functions as they're encountered. Currently `rainbow_flow_command` is the only one in scope; audit others during implementation.
- **WLED max segments:** assumed 16. If a future firmware build changes this, `maxWLEDSegments` becomes a config-derived constant or is bumped.
- **Async `new()` SDK support:** if the Viam Python SDK doesn't allow `async def new()`, fall back to lazy fetch on first `visualize()` call.

## Coordination

Only the user's own resources are affected. No cross-team coordination required. The two-PR rollout requires updating the machine config in two stages (add `segments` to the WLED component before deploying spotifyViz changes; remove `strip_length` from spotifyViz components after PR 2 deploys).
