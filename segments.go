package wled

import (
	"context"
	"fmt"
)

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

// Validate checks the invariants of a single segment: positive geometry and
// non-negative grp/spc. Cross-segment invariants (uniqueness, contiguous IDs,
// overlaps, count limit) live in Config.Validate where the slice is in scope.
func (seg SegmentConfig) Validate() error {
	if seg.Start < 0 {
		return fmt.Errorf("start (%d) must be >= 0", seg.Start)
	}
	if seg.Stop <= seg.Start {
		return fmt.Errorf("stop (%d) must be greater than start (%d)", seg.Stop, seg.Start)
	}
	if seg.Grp < 0 {
		return fmt.Errorf("grp (%d) must be >= 0", seg.Grp)
	}
	if seg.Spc < 0 {
		return fmt.Errorf("spc (%d) must be >= 0", seg.Spc)
	}
	return nil
}

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
			grp = 1 // WLED default; sent explicitly so device state is deterministic
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
