package otdr

// Kind of detected OTDR event.
type Kind string

const (
	// KindSplice is a non-reflective loss step (fusion splice / bend).
	KindSplice Kind = "splice"
	// KindConnector is a reflective event (connector / mechanical joint).
	KindConnector Kind = "connector"
	// KindTruncation marks the acquisition end: only a length lower bound is
	// ever reported there, never a fabricated fiber end.
	KindTruncation Kind = "truncation"
)

// Breakpoint is one joint of the piecewise baseline. Baseline segments run
// between consecutive breakpoints; a splice/connector level drop shows as a
// step between two segments.
type Breakpoint struct {
	Index int     `json:"index"`
	Meters float64 `json:"meters"`
	DB    float64 `json:"db"`
}

// Event is a detected event candidate. Identity is (TraceID, StartIndex):
// the raw sample window. Metric distance is evidence carried on the version
// and must never be used as identity.
type Event struct {
	ID         string  `json:"id"`
	TraceID    string  `json:"trace_id"`
	Kind       Kind    `json:"kind"`
	StartIndex int     `json:"start_index"`
	EndIndex   int     `json:"end_index"`
	PeakIndex  int     `json:"peak_index"`
	StartM     float64 `json:"start_m"`
	EndM       float64 `json:"end_m"`
	PeakM      float64 `json:"peak_m"`
	// LossDB is the level drop across this event's window. Zero for ghosts
	// (a ghost is a reflection copy with no real loss) and for truncation.
	LossDB float64 `json:"loss_db"`
	// PeakDB is the reflection spike height above local baseline.
	PeakDB float64 `json:"peak_db"`
	// Strong marks reflections capable of creating a ghost echo.
	Strong bool `json:"strong"`
}

// Overlaps reports whether two event windows share any raw samples.
func (e Event) Overlaps(o Event) bool {
	return e.StartIndex <= o.EndIndex && o.StartIndex <= e.EndIndex
}

// GhostCandidate ties a suspected ghost reflection to the leading strong
// reflection that produced it, including the measured distance ratio.
// A ghost is never asserted from peak height alone.
type GhostCandidate struct {
	GhostEventID string  `json:"ghost_event_id"`
	ParentEventID string `json:"parent_event_id"`
	ParentM      float64 `json:"parent_m"`
	GhostM       float64 `json:"ghost_m"`
	Ratio        float64 `json:"ratio"`
	LossDB       float64 `json:"loss_db"`
	Reason       string  `json:"reason"`
}

// LossItem is one non-overlapping loss contribution to the cumulative budget.
// Overlapping event windows are merged into one group before measurement so a
// single physical drop is never counted twice.
type LossItem struct {
	GroupID     string  `json:"group_id"`
	StartIndex  int     `json:"start_index"`
	EndIndex    int     `json:"end_index"`
	StartM      float64 `json:"start_m"`
	EndM        float64 `json:"end_m"`
	LossDB      float64 `json:"loss_db"`
	EventIDs    []string `json:"event_ids"`
}

// Analysis is the full evidence bundle for one (trace, calibration, dead zone)
// interpretation version.
type Analysis struct {
	TraceID     string           `json:"trace_id"`
	Calibration Calibration      `json:"calibration"`
	// DeadZoneNS is the launch dead zone in round-trip ns; samples inside it
	// are excluded from event detection.
	DeadZoneNS  float64          `json:"dead_zone_ns"`
	DeadZoneM   float64          `json:"dead_zone_m"`
	SampleCount int              `json:"sample_count"`
	// FiberEndIndex is the last sample still on fiber (before the noise
	// floor at a truncated acquisition).
	FiberEndIndex int     `json:"fiber_end_index"`
	FiberEndM     float64 `json:"fiber_end_m"`
	// Truncated means the trace ends in the instrument noise floor rather
	// than at a clear terminal reflection; FiberEndM is then a lower bound.
	Truncated bool `json:"truncated"`
	LengthBound string  `json:"length_bound"` // "lower_bound" or "observed"
	// BaselineDB holds one piecewise-linear baseline value per sample.
	BaselineDB  []float64       `json:"baseline_db"`
	Breakpoints []Breakpoint    `json:"breakpoints"`
	Events      []Event         `json:"events"`
	Ghosts      []GhostCandidate `json:"ghosts"`
	Losses      []LossItem      `json:"losses"`
	TotalLossDB float64         `json:"total_loss_db"`
}

// DistanceNote records the conversion inputs kept for replay/audit.
type DistanceNote struct {
	TraceID          string  `json:"trace_id"`
	SampleCount      int     `json:"sample_count"`
	DTNS             float64 `json:"dt_ns"`
	T0NS             float64 `json:"t0_ns"`
	GroupIndex       float64 `json:"group_index"`
	SpatialSampleM   float64 `json:"spatial_sample_m"`
	DeadZoneNS       float64 `json:"dead_zone_ns"`
	DeadZoneM        float64 `json:"dead_zone_m"`
}
