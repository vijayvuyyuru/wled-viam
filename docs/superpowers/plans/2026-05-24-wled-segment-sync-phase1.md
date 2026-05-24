# WLED Segment Sync — Phase 1 (wled-viam) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add startup segment reconciliation and a `get_segments` DoCommand to the WLED Viam Go module so segment geometry survives WLED power cycles and is exposed as canonical metadata for downstream consumers.

**Architecture:** A new `segments.go` file owns segment config types, validation, the startup sync routine, and the read-side helper. `Config.Validate` enforces the segments invariant at config-load time. `NewWled` calls `applySegmentConfig` after brightness init — one HTTP POST that pushes configured segments and zeroes out any extras (deletion via `stop: 0`) up to WLED's 16-segment firmware limit. `DoCommand` gains a `get_segments` case that returns the cached config (not a live device GET) so consumers get a stable, infallible read.

**Tech Stack:** Go 1.21+, `go.viam.com/rdk` SDK, `net/http`, `net/http/httptest` for integration tests. No new external dependencies.

**Spec:** `docs/superpowers/specs/2026-05-24-wled-segment-sync-design.md`

**Phase 2 (out of scope here):** `spotifyViz` consumer changes — fetch segments on first async call, drop `strip_length` config, convert `rainbow_flow_command` to a builder. Will get its own plan written from the `spotifyViz` worktree.

---

## File Structure

| File | Responsibility | Status |
|------|----------------|--------|
| `segments.go` | `SegmentConfig` struct, `validateSegments`, `applySegmentConfig`, `getSegments`, `maxWLEDSegments` constant | Create |
| `segments_test.go` | Unit tests for validation, integration tests for apply/get using `httptest` | Create |
| `module.go` | Add `Segments []SegmentConfig` to `Config`; call `validateSegments` from `Config.Validate`; call `applySegmentConfig` from `NewWled`; add `get_segments` case to `DoCommand` switch | Modify |
| `CLAUDE.md` | Document new `segments` config attribute and `get_segments` DoCommand shape | Modify |

---

## Task 1: Scaffold `segments.go` with types and constants

**Files:**
- Create: `segments.go`

- [ ] **Step 1: Create `segments.go` with the types and constants**

```go
package wled

// maxWLEDSegments is the upper bound on segments supported by the WLED firmware.
// applySegmentConfig pads its POST with stop:0 deletion entries up to this ID
// to ensure no stale segments survive on the device.
const maxWLEDSegments = 16

// SegmentConfig describes one WLED segment that the module will reconcile
// onto the device at startup. Geometry fields (Start, Stop) are required by
// Validate; Rev/Grp/Spc are optional and default to WLED defaults when unset.
type SegmentConfig struct {
	ID    int  `json:"id"`
	Start int  `json:"start"`
	Stop  int  `json:"stop"`
	Rev   bool `json:"rev,omitempty"`
	Grp   int  `json:"grp,omitempty"`
	Spc   int  `json:"spc,omitempty"`
}
```

- [ ] **Step 2: Verify package still compiles**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 3: Commit**

```bash
git add segments.go
git commit -m "wled: add SegmentConfig struct and maxWLEDSegments constant"
```

---

## Task 2: Add `Segments` field to `Config`

**Files:**
- Modify: `module.go` (the `Config` struct, around line 25)

- [ ] **Step 1: Add the `Segments` field**

Edit `module.go`. Change the `Config` struct from:

```go
type Config struct {
	WledIP      string  `json:"wled_ip"`
	WledPort    int     `json:"wled_port,omitempty"`
	Brightness  float64 `json:"brightness,omitempty"`
	HTTPTimeout float64 `json:"http_timeout,omitempty"`
}
```

To:

```go
type Config struct {
	WledIP      string          `json:"wled_ip"`
	WledPort    int             `json:"wled_port,omitempty"`
	Brightness  float64         `json:"brightness,omitempty"`
	HTTPTimeout float64         `json:"http_timeout,omitempty"`
	Segments    []SegmentConfig `json:"segments"`
}
```

(Note: no `omitempty` on `Segments` — we want explicit empty arrays in config to be visible to validation, not silently elided.)

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add module.go
git commit -m "wled: add Segments field to Config"
```

---

## Task 3: Implement segment validation (TDD)

**Files:**
- Create: `segments_test.go`
- Modify: `segments.go` (add `validateSegments` function)
- Modify: `module.go` (`Config.Validate` calls `validateSegments`)

- [ ] **Step 1: Write the failing test**

Create `segments_test.go`:

```go
package wled

import (
	"strings"
	"testing"
)

func TestValidateSegments(t *testing.T) {
	tests := []struct {
		name    string
		segs    []SegmentConfig
		wantErr string // substring expected in error; "" means expect no error
	}{
		{
			name:    "empty list rejected",
			segs:    nil,
			wantErr: "at least one",
		},
		{
			name: "valid 3x144 contiguous",
			segs: []SegmentConfig{
				{ID: 0, Start: 0, Stop: 144},
				{ID: 1, Start: 144, Stop: 288},
				{ID: 2, Start: 288, Stop: 432},
			},
			wantErr: "",
		},
		{
			name: "valid heterogeneous lengths",
			segs: []SegmentConfig{
				{ID: 0, Start: 0, Stop: 100},
				{ID: 1, Start: 100, Stop: 250},
				{ID: 2, Start: 250, Stop: 432},
			},
			wantErr: "",
		},
		{
			name: "duplicate IDs rejected",
			segs: []SegmentConfig{
				{ID: 0, Start: 0, Stop: 144},
				{ID: 0, Start: 144, Stop: 288},
			},
			wantErr: "duplicate id",
		},
		{
			name: "non-contiguous IDs rejected",
			segs: []SegmentConfig{
				{ID: 0, Start: 0, Stop: 144},
				{ID: 2, Start: 144, Stop: 288},
			},
			wantErr: "contiguous",
		},
		{
			name: "ids not starting at zero rejected",
			segs: []SegmentConfig{
				{ID: 1, Start: 0, Stop: 144},
				{ID: 2, Start: 144, Stop: 288},
			},
			wantErr: "contiguous",
		},
		{
			name: "stop <= start rejected",
			segs: []SegmentConfig{
				{ID: 0, Start: 100, Stop: 100},
			},
			wantErr: "stop",
		},
		{
			name: "negative start rejected",
			segs: []SegmentConfig{
				{ID: 0, Start: -1, Stop: 144},
			},
			wantErr: "start",
		},
		{
			name: "overlapping ranges rejected",
			segs: []SegmentConfig{
				{ID: 0, Start: 0, Stop: 200},
				{ID: 1, Start: 150, Stop: 300},
			},
			wantErr: "overlap",
		},
		{
			name: "negative grp rejected",
			segs: []SegmentConfig{
				{ID: 0, Start: 0, Stop: 144, Grp: -1},
			},
			wantErr: "grp",
		},
		{
			name: "negative spc rejected",
			segs: []SegmentConfig{
				{ID: 0, Start: 0, Stop: 144, Spc: -1},
			},
			wantErr: "spc",
		},
		{
			name: "exceeds maxWLEDSegments rejected",
			segs: func() []SegmentConfig {
				out := make([]SegmentConfig, maxWLEDSegments+1)
				for i := range out {
					out[i] = SegmentConfig{ID: i, Start: i * 10, Stop: (i + 1) * 10}
				}
				return out
			}(),
			wantErr: "exceeds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSegments(tt.segs)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run TestValidateSegments -v`
Expected: compilation error referencing `undefined: validateSegments`.

- [ ] **Step 3: Implement `validateSegments`**

Edit `segments.go`. Add to the top of the file (after the `import` block — add `"fmt"` and `"sort"` to imports if not present; this is the first thing in the file that needs them):

```go
import (
	"fmt"
	"sort"
)
```

Then add the function:

```go
// validateSegments enforces the segment-config invariants required by the
// startup sync: non-empty, unique contiguous IDs starting at 0, non-negative
// and non-overlapping ranges, non-negative Grp/Spc, and within the
// firmware's segment limit.
func validateSegments(segs []SegmentConfig) error {
	if len(segs) == 0 {
		return fmt.Errorf("segments must contain at least one entry")
	}
	if len(segs) > maxWLEDSegments {
		return fmt.Errorf("segments count %d exceeds firmware limit %d", len(segs), maxWLEDSegments)
	}

	// IDs unique and contiguous starting at 0.
	seenIDs := make(map[int]bool, len(segs))
	for i, seg := range segs {
		if seenIDs[seg.ID] {
			return fmt.Errorf("segments[%d]: duplicate id %d", i, seg.ID)
		}
		seenIDs[seg.ID] = true
	}
	for expectedID := 0; expectedID < len(segs); expectedID++ {
		if !seenIDs[expectedID] {
			return fmt.Errorf("segment ids must be contiguous starting at 0; missing id %d", expectedID)
		}
	}

	// Per-segment field validation.
	for i, seg := range segs {
		if seg.Start < 0 {
			return fmt.Errorf("segments[%d]: start (%d) must be >= 0", i, seg.Start)
		}
		if seg.Stop <= seg.Start {
			return fmt.Errorf("segments[%d]: stop (%d) must be greater than start (%d)", i, seg.Stop, seg.Start)
		}
		if seg.Grp < 0 {
			return fmt.Errorf("segments[%d]: grp (%d) must be >= 0", i, seg.Grp)
		}
		if seg.Spc < 0 {
			return fmt.Errorf("segments[%d]: spc (%d) must be >= 0", i, seg.Spc)
		}
	}

	// No overlapping ranges. Sort by Start and check adjacent pairs.
	sorted := make([]SegmentConfig, len(segs))
	copy(sorted, segs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Start < sorted[i-1].Stop {
			return fmt.Errorf("segments with ids %d and %d overlap (%d..%d vs %d..%d)",
				sorted[i-1].ID, sorted[i].ID,
				sorted[i-1].Start, sorted[i-1].Stop,
				sorted[i].Start, sorted[i].Stop)
		}
	}

	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run TestValidateSegments -v`
Expected: all subtests PASS.

- [ ] **Step 5: Wire `validateSegments` into `Config.Validate`**

Edit `module.go`. Replace `Config.Validate`:

```go
func (cfg *Config) Validate(path string) ([]string, []string, error) {
	if cfg.WledIP == "" {
		return nil, nil, fmt.Errorf("%s: wled_ip is required", path)
	}
	return nil, nil, nil
}
```

With:

```go
func (cfg *Config) Validate(path string) ([]string, []string, error) {
	if cfg.WledIP == "" {
		return nil, nil, fmt.Errorf("%s: wled_ip is required", path)
	}
	if err := validateSegments(cfg.Segments); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return nil, nil, nil
}
```

- [ ] **Step 6: Verify build still passes**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add segments.go segments_test.go module.go
git commit -m "wled: validate segment config (uniqueness, contiguous IDs, no overlaps)"
```

---

## Task 4: Implement `applySegmentConfig` (TDD with httptest)

**Files:**
- Modify: `segments.go` (add `applySegmentConfig` method)
- Modify: `segments_test.go` (add integration test)

- [ ] **Step 1: Write the failing integration test**

Edit `segments_test.go`. Replace the existing import block with the complete set needed through the end of Task 7:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.viam.com/rdk/logging"
)
```

Add a helper at the bottom of the file (after `TestValidateSegments`):

```go
// newFakeWLED stands up an httptest.Server that mimics WLED's /json/state endpoint.
// It records every POST body so tests can assert on them.
func newFakeWLED(t *testing.T) (*httptest.Server, *[]map[string]interface{}) {
	t.Helper()
	var (
		mu       sync.Mutex
		captured []map[string]interface{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/json/state" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(body, &parsed); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		captured = append(captured, parsed)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

// newTestWled returns a *wledWled pointed at the given fake server, with the
// configured segments. No sACN state is touched.
func newTestWled(t *testing.T, baseURL string, segs []SegmentConfig) *wledWled {
	t.Helper()
	// httptest URL is "http://127.0.0.1:PORT" — strip the scheme to fit wledBase format.
	stripped := strings.TrimPrefix(baseURL, "http://")
	host := stripped
	port := 80
	if i := strings.LastIndex(stripped, ":"); i >= 0 {
		host = stripped[:i]
		fmt.Sscanf(stripped[i+1:], "%d", &port)
	}
	return &wledWled{
		logger:     logging.NewTestLogger(t),
		cfg:        &Config{WledIP: host, WledPort: port, Segments: segs},
		wledBase:   baseURL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}
```

Add the test:

```go
func TestApplySegmentConfig(t *testing.T) {
	srv, captured := newFakeWLED(t)

	segs := []SegmentConfig{
		{ID: 0, Start: 0, Stop: 144},
		{ID: 1, Start: 144, Stop: 288, Rev: true},
		{ID: 2, Start: 288, Stop: 432, Grp: 2, Spc: 1},
	}
	s := newTestWled(t, srv.URL, segs)

	if err := s.applySegmentConfig(context.Background()); err != nil {
		t.Fatalf("applySegmentConfig: %v", err)
	}

	if len(*captured) != 1 {
		t.Fatalf("expected 1 POST, got %d", len(*captured))
	}
	payload, ok := (*captured)[0]["seg"].([]interface{})
	if !ok {
		t.Fatalf("expected seg array in POST body, got %T", (*captured)[0]["seg"])
	}

	// Three configured segments + (maxWLEDSegments - 3) deletion entries.
	wantTotal := maxWLEDSegments
	if len(payload) != wantTotal {
		t.Fatalf("expected %d seg entries, got %d", wantTotal, len(payload))
	}

	// Spot-check configured entries.
	first := payload[0].(map[string]interface{})
	if first["id"].(float64) != 0 || first["start"].(float64) != 0 || first["stop"].(float64) != 144 {
		t.Errorf("seg 0 wrong: %v", first)
	}
	if first["grp"].(float64) != 1 { // unset grp normalizes to 1
		t.Errorf("seg 0 grp expected 1, got %v", first["grp"])
	}
	if first["rev"].(bool) != false {
		t.Errorf("seg 0 rev expected false, got %v", first["rev"])
	}

	second := payload[1].(map[string]interface{})
	if second["rev"].(bool) != true {
		t.Errorf("seg 1 rev expected true, got %v", second["rev"])
	}

	third := payload[2].(map[string]interface{})
	if third["grp"].(float64) != 2 {
		t.Errorf("seg 2 grp expected 2, got %v", third["grp"])
	}
	if third["spc"].(float64) != 1 {
		t.Errorf("seg 2 spc expected 1, got %v", third["spc"])
	}

	// Deletion entries: ids 3..maxWLEDSegments-1 with stop:0.
	for i := 3; i < maxWLEDSegments; i++ {
		entry := payload[i].(map[string]interface{})
		if entry["id"].(float64) != float64(i) {
			t.Errorf("deletion entry %d: wrong id %v", i, entry["id"])
		}
		if entry["stop"].(float64) != 0 {
			t.Errorf("deletion entry %d: stop expected 0, got %v", i, entry["stop"])
		}
	}
}
```

(`fmt` is already in the import block from Step 1 above — used by `newTestWled`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestApplySegmentConfig -v`
Expected: compilation error `undefined: (*wledWled).applySegmentConfig`.

- [ ] **Step 3: Implement `applySegmentConfig`**

Edit `segments.go`. Add the necessary imports (extend the import block):

```go
import (
	"context"
	"fmt"
	"sort"
)
```

Add the method at the bottom of the file:

```go
// applySegmentConfig pushes the configured segments to the device and zeroes
// out (deletes) any segment IDs above the configured count up to the firmware
// limit. One HTTP POST handles the whole reconciliation; deleting a
// nonexistent segment is a no-op on WLED's side.
func (s *wledWled) applySegmentConfig(ctx context.Context) error {
	segs := s.cfg.Segments
	payload := make([]map[string]interface{}, 0, maxWLEDSegments)
	for _, seg := range segs {
		grp := seg.Grp
		if grp == 0 {
			grp = 1 // WLED default; sent explicitly so the device state is deterministic
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
	for id := len(segs); id < maxWLEDSegments; id++ {
		payload = append(payload, map[string]interface{}{
			"id":   id,
			"stop": 0,
		})
	}
	if _, err := s.PostState(ctx, map[string]interface{}{"seg": payload}); err != nil {
		return fmt.Errorf("apply segment config: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run TestApplySegmentConfig -v`
Expected: PASS.

Also run the full test suite to confirm no regressions: `go test ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add segments.go segments_test.go
git commit -m "wled: implement applySegmentConfig with strict-delete reconciliation"
```

---

## Task 5: Wire `applySegmentConfig` into `NewWled`

**Files:**
- Modify: `module.go` (`NewWled`, around line 99–105)

- [ ] **Step 1: Add the call site**

Edit `module.go`. After the brightness block in `NewWled` (currently lines 99–105), add the segment sync. The relevant region currently reads:

```go
	// Apply initial brightness if configured
	if conf.Brightness > 0 {
		bri := int(conf.Brightness * 255)
		if _, err := s.PostState(ctx, map[string]interface{}{"bri": bri}); err != nil {
			logger.Warnw("failed to set initial brightness", "error", err)
		}
	}

	// sACN transmitter is lazily initialized on first frame command
	// and torn down when switching to HTTP. No keep-alive interference.
```

Change it to:

```go
	// Apply initial brightness if configured
	if conf.Brightness > 0 {
		bri := int(conf.Brightness * 255)
		if _, err := s.PostState(ctx, map[string]interface{}{"bri": bri}); err != nil {
			logger.Warnw("failed to set initial brightness", "error", err)
		}
	}

	// Reconcile segment geometry. WLED loses segments on power cycle and
	// after firmware resets, so the Viam module is the declared source of
	// truth — push configured segments on every start. Failure is logged
	// and tolerated (next reload retries) to match the brightness pattern.
	if err := s.applySegmentConfig(ctx); err != nil {
		logger.Warnw("failed to apply segment config", "error", err)
	}

	// sACN transmitter is lazily initialized on first frame command
	// and torn down when switching to HTTP. No keep-alive interference.
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 3: Verify tests still pass**

Run: `go test ./...`
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add module.go
git commit -m "wled: call applySegmentConfig from NewWled after brightness init"
```

---

## Task 6: Implement `getSegments` helper (TDD)

**Files:**
- Modify: `segments.go` (add `getSegments` method)
- Modify: `segments_test.go` (add test)

- [ ] **Step 1: Write the failing test**

Edit `segments_test.go`. Add the test:

```go
func TestGetSegments(t *testing.T) {
	segs := []SegmentConfig{
		{ID: 0, Start: 0, Stop: 144},
		{ID: 1, Start: 144, Stop: 288, Rev: true, Grp: 2, Spc: 1},
	}
	s := &wledWled{
		logger: logging.NewTestLogger(t),
		cfg:    &Config{Segments: segs},
	}

	out := s.getSegments()
	rawList, ok := out["segments"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected segments to be []map[string]interface{}, got %T", out["segments"])
	}
	if len(rawList) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(rawList))
	}

	first := rawList[0]
	if first["id"] != 0 || first["start"] != 0 || first["stop"] != 144 || first["len"] != 144 {
		t.Errorf("seg 0 wrong: %v", first)
	}
	if first["rev"] != false || first["grp"] != 1 || first["spc"] != 0 {
		t.Errorf("seg 0 defaults wrong: %v", first)
	}

	second := rawList[1]
	if second["len"] != 144 {
		t.Errorf("seg 1 len wrong: %v", second["len"])
	}
	if second["rev"] != true || second["grp"] != 2 || second["spc"] != 1 {
		t.Errorf("seg 1 explicit fields wrong: %v", second)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestGetSegments -v`
Expected: compilation error `undefined: (*wledWled).getSegments`.

- [ ] **Step 3: Implement `getSegments`**

Edit `segments.go`. Add at the bottom of the file:

```go
// getSegments returns the cached segment config in DoCommand-friendly shape.
// Does not query the device — the Viam config is the declared source of truth
// and applySegmentConfig keeps the device aligned with it. Consumers calling
// this on every resource start get a stable, infallible read.
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

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run TestGetSegments -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add segments.go segments_test.go
git commit -m "wled: implement getSegments helper returning cached config"
```

---

## Task 7: Expose `get_segments` via `DoCommand` (TDD)

**Files:**
- Modify: `module.go` (`DoCommand` switch around lines 126–138)
- Modify: `segments_test.go` (add DoCommand test)

- [ ] **Step 1: Write the failing test**

Edit `segments_test.go`. Add the test:

```go
func TestDoCommand_GetSegments(t *testing.T) {
	segs := []SegmentConfig{
		{ID: 0, Start: 0, Stop: 100},
		{ID: 1, Start: 100, Stop: 250},
	}
	s := &wledWled{
		logger: logging.NewTestLogger(t),
		cfg:    &Config{Segments: segs},
	}

	out, err := s.DoCommand(context.Background(), map[string]interface{}{"command": "get_segments"})
	if err != nil {
		t.Fatalf("DoCommand returned error: %v", err)
	}
	list, ok := out["segments"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected segments list, got %T", out["segments"])
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(list))
	}
	if list[0]["len"] != 100 {
		t.Errorf("seg 0 len expected 100, got %v", list[0]["len"])
	}
	if list[1]["len"] != 150 {
		t.Errorf("seg 1 len expected 150, got %v", list[1]["len"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestDoCommand_GetSegments -v`
Expected: FAIL — `unknown command: "get_segments"`.

- [ ] **Step 3: Add the `get_segments` case to the switch**

Edit `module.go`. In `DoCommand`, the switch currently looks like:

```go
		switch cmdStr {
		case "off":
			return s.PostState(ctx, map[string]interface{}{"on": false})
		case "on":
			return s.PostState(ctx, map[string]interface{}{"on": true})
		case "status":
			return s.GetState(ctx)
		case "frame":
			// Shape C — per-pixel frame via sACN
			return s.sendFrame(ctx, cmd)
		default:
			return nil, fmt.Errorf("unknown command: %q", cmdStr)
		}
```

Add a `get_segments` case before `default`:

```go
		switch cmdStr {
		case "off":
			return s.PostState(ctx, map[string]interface{}{"on": false})
		case "on":
			return s.PostState(ctx, map[string]interface{}{"on": true})
		case "status":
			return s.GetState(ctx)
		case "frame":
			// Shape C — per-pixel frame via sACN
			return s.sendFrame(ctx, cmd)
		case "get_segments":
			return s.getSegments(), nil
		default:
			return nil, fmt.Errorf("unknown command: %q", cmdStr)
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run TestDoCommand_GetSegments -v`
Expected: PASS.

Run the full suite: `go test ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add module.go segments_test.go
git commit -m "wled: expose get_segments via DoCommand"
```

---

## Task 8: Update documentation

**Files:**
- Modify: `CLAUDE.md` — extend `Config Attributes` and `DoCommand Shapes` sections; add a new `Segment Sync` subsection under `Architecture`.

- [ ] **Step 1: Update `Config Attributes` section**

In `CLAUDE.md`, find the "Config Attributes" section. The current example reads:

```json
{
  "wled_ip": "wled-7f091c.local",
  "wled_port": 80,
  "brightness": 0.6,
  "http_timeout": 10
}
```

Replace with:

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

Then below the existing bullet list (after `http_timeout`), add:

```
- `segments` (array, **required**) — list of WLED segments to reconcile onto the device at startup. Each entry has `id` (int, contiguous starting at 0), `start` and `stop` (ints, `stop > start`, non-overlapping), and optional `rev` (bool), `grp` (int, default 1), `spc` (int, default 0). Validation rejects empty arrays, duplicate IDs, gaps in IDs, overlapping ranges, and counts exceeding 16 (WLED firmware limit).
```

- [ ] **Step 2: Update `DoCommand Shapes` section — add `get_segments` to Shape A**

Find the "Shape A — Global commands" bullet list. After the `"frame"` bullet, add:

```
- `"get_segments"` — returns `{"segments": [{"id", "start", "stop", "len", "rev", "grp", "spc"}, ...]}` reflecting the module's cached segment config. Does not query the device. Consumers in spotifyViz call this on resource start to learn the canonical strip geometry.
```

- [ ] **Step 3: Add a `Segment Sync` subsection**

In `CLAUDE.md`, find the `### sACN ↔ HTTP Mode Switching` heading. Just above it, add a new subsection:

````markdown
### Segment Sync

The WLED device loses segment configuration on power cycle. The module treats the Viam `segments` config as the declared source of truth and reconciles it onto the device on every `NewWled` call.

**Startup**: After brightness, the module POSTs `{"seg": [<configured segments>, <stop:0 deletion entries for ids N..15>]}` to `/json/state`. This is one HTTP call that both installs configured segments and removes any stale extras (deleting a non-existent segment is a no-op).

**`get_segments` DoCommand**: returns the cached config in a consumer-friendly shape that includes the computed `len` field. Does **not** query the device — the cache is authoritative because `applySegmentConfig` keeps the device in sync with it.

**Why not save to flash?** WLED supports persisting state via `nsave`, but that adds flash wear and lets device state silently drift from Viam config. The "always reconcile on start" approach trades one extra HTTP call per reload for a deterministic state model.
````

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: document segments config and get_segments DoCommand"
```

---

## Task 9: Final verification

- [ ] **Step 1: Run the full test suite**

Run: `go test ./... -v`
Expected: all tests PASS, no skips, no errors.

- [ ] **Step 2: Run a clean build**

Run: `go build -o /tmp/wled-segment-sync-verify ./cmd/module/`
Expected: no output, binary created.

- [ ] **Step 3: Confirm `go vet` is clean**

Run: `go vet ./...`
Expected: no output.

- [ ] **Step 4: Manual device verification** (requires the real WLED device on the network)

1. Power-cycle the WLED device (unplug the shared power supply, wait 5 seconds, plug back in).
2. Open WLED web UI at `http://wled-7f091c.local` — confirm segments revert to whatever the device boots with (typically a single segment).
3. Reload the WLED Viam module on the machine (via Viam app or `viam reload` CLI). Ensure the machine config has the new `segments` array.
4. Refresh the WLED web UI. Confirm three segments are now configured at 0–144 / 144–288 / 288–432.
5. From a terminal, invoke `get_segments` via `viam machine do-command --resource=<wled-resource-name> --command='{"command": "get_segments"}'` (or equivalent SDK call). Confirm the response shape matches the test expectations.
6. Test an effect via spotify-viz or default-viz to confirm segments are correctly addressable post-sync.

- [ ] **Step 5: Note any Phase 2 follow-ups**

If any unexpected friction surfaces during manual verification (e.g., spotifyViz visualizers fail to address segments correctly, default-viz's hardcoded 3-segment command becomes a problem), capture it as a note in the Phase 2 plan when that worktree is set up. Phase 1 is done as soon as the WLED side reconciles and `get_segments` returns the expected shape — spotifyViz integration is Phase 2's problem.

---

## Phase 2 hook

Phase 2 (in the `spotifyViz` repo) will:

1. Add `fetch_segments(led_module, logger) -> list[dict]` to `src/models/led_commands.py`.
2. Convert the module-level `rainbow_flow_command` constant to a `rainbow_flow_for(segments)` builder.
3. Add lazy `_ensure_segments` async helper to each visualizer (spotify_viz, weather_viz, calibration_viz, default_viz). Called at the top of `do_command` / `get_images` / `visualize` paths.
4. Replace `self._strip_length` reads with `self._segments[0]["len"]` and `range(3)` with `range(len(self._segments))`.
5. Drop `strip_length` from `validate_config` and from machine config in each consumer component.
6. Audit `weather_viz.py` for whether `_strip_length` is actually used; remove the field entirely if not.

The data model in this Phase 1 plan (full segment list returned from `get_segments`, including `len`) intentionally preserves per-ring data so Phase 2 + a future prong-2 (heterogeneous lengths in rendering) are unblocked — prong 2 becomes "change `[0]` indices to `[i]`" at the sites that care.
