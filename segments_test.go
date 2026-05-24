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
