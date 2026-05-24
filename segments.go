package wled

import (
	"fmt"
	"sort"
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
