package wled

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
			cfg := &Config{Segments: tt.segs}
			err := cfg.validateSegments()
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
