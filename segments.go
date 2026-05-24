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
