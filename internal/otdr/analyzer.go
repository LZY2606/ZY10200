package otdr

import (
	"fmt"
	"math"
	"sort"
)

// Analysis parameters.
const (
	// trendWindowSamples smooths log power into the local trend baseline. It
	// must be wider than the widest reflection impulse (100 ns ~= 100 samples)
	// so a spike cannot bias its own trend estimate; a median tolerates up to
	// half the window being contaminated.
	trendWindowSamples = 501
	// endWindowSamples is a short robust window used to locate where the live
	// fiber gives way to the post-truncation noise floor.
	endWindowSamples = 21
	// reflectThresholdDB flags a sample as reflective when it sits this far
	// above the smoothed trend.
	reflectThresholdDB = 2.5
	// reflectMinClusterSamples rejects isolated noise spikes.
	reflectMinClusterSamples = 3
	// strongReflectPeakDB marks reflections energetic enough to cast ghosts.
	strongReflectPeakDB = 8.0
	// stepMeasureHalfWindow samples (per side) fit the local line level.
	stepMeasureHalfWindow = 40
	// stepSkipTransient samples next to an event excluded from line fits.
	stepSkipTransient = 8
	// stepMinDropDB is the smallest level drop reported as a splice. Both the
	// fitted-line drop and a robust median drop must exceed it.
	stepMinDropDB = 0.07
	// stepMedianGateDB rejects line-fit picks not corroborated by medians.
	stepMedianGateDB = 0.06
	// nmsHalfWindow is the suppression radius (samples) between step picks.
	nmsHalfWindow = 48
	// ghostRatioTolerance accepts a 2x path-length ghost.
	ghostRatioMin = 1.8
	ghostRatioMax = 2.2
	// ghostMaxLossDB: a real connector at that distance would show a level
	// drop; a ghost copy shows none.
	ghostMaxLossDB = 0.08
	// floorBelowSignalDB is how far under the start signal the noise floor is.
	floorBelowSignalDB = 18.0
)

// Analyze interprets an immutable trace under one calibration + launch dead
// zone. It never edits raw samples; changing the group index produces a new
// Analysis while raw sample indices stay stable.
func Analyze(t *RawTrace, cal Calibration, deadZoneNS float64) *Analysis {
	n := len(t.PowerDB)
	if n == 0 {
		return &Analysis{TraceID: t.ID, Calibration: cal, DeadZoneNS: deadZoneNS}
	}

	smooth := rollingMedian(t.PowerDB, trendWindowSamples)
	endMedian := rollingMedian(t.PowerDB, endWindowSamples)
	fiberEnd := detectFiberEnd(endMedian)

	deadM := PulseDeadZoneMeters(deadZoneNS, cal.GroupIndex)
	deadSamples := int(math.Ceil(deadM / cal.SpatialSampleMeters()))

	residual := make([]float64, n)
	for i := 0; i < n; i++ {
		residual[i] = t.PowerDB[i] - smooth[i]
	}

	// Reflection clusters (evaluated only on the live fiber and after the
	// launch dead zone).
	mask := make([]bool, n)
	for i := deadSamples; i <= fiberEnd; i++ {
		if residual[i] >= reflectThresholdDB {
			mask[i] = true
		}
	}
	clusters := clusterMask(mask)

	events := make([]Event, 0, len(clusters)+4)
	blocked := make([]bool, n) // samples excluded from generic step scan
	for _, c := range clusters {
		ev := buildConnector(t, cal, c, smooth, residual)
		events = append(events, ev)
		for k := ev.StartIndex; k <= ev.EndIndex; k++ {
			blocked[k] = true
		}
		// Suppress generic splice picks in the transient shoulders too.
		for k := ev.StartIndex - stepSkipTransient*2; k <= ev.EndIndex+stepSkipTransient*2; k++ {
			if k >= 0 && k <= fiberEnd {
				blocked[k] = true
			}
		}
	}

	// Generic non-reflective level steps (splices).
	steps := detectSteps(t.PowerDB, fiberEnd, deadSamples, blocked)
	for _, s := range steps {
		events = append(events, Event{
			ID:         eventID(t.ID, s.idx),
			TraceID:    t.ID,
			Kind:       KindSplice,
			StartIndex: s.lo,
			EndIndex:   s.hi,
			PeakIndex:  s.idx,
			StartM:     cal.OneWayMeters(s.lo),
			EndM:       cal.OneWayMeters(s.hi),
			PeakM:      cal.OneWayMeters(s.idx),
			LossDB:     s.drop,
		})
	}

	// Terminal / truncation record.
	truncated := fiberEnd < n-1
	lengthBound := "observed"
	if truncated {
		lengthBound = "lower_bound"
		events = append(events, Event{
			ID:         fmt.Sprintf("E:%s:end", t.ID),
			TraceID:    t.ID,
			Kind:       KindTruncation,
			StartIndex: fiberEnd,
			EndIndex:   n - 1,
			PeakIndex:  fiberEnd,
			StartM:     cal.OneWayMeters(fiberEnd),
			EndM:       cal.OneWayMeters(n - 1),
			PeakM:      cal.OneWayMeters(fiberEnd),
		})
	}

	sort.SliceStable(events, func(i, j int) bool {
		return events[i].StartIndex < events[j].StartIndex
	})

	// Piecewise linear baseline through splice/connector level changes.
	baseline, breaks := piecewiseBaseline(t.PowerDB, events, fiberEnd, cal)

	// Ghost evidence: leading strong reflection + 2x distance, no real loss.
	ghosts := detectGhosts(t.ID, events)
	ghostEventIDs := map[string]bool{}
	for _, g := range ghosts {
		ghostEventIDs[g.GhostEventID] = true
	}

	// Non-overlapping loss groups.
	losses, total := buildLosses(t.ID, cal, events, ghostEventIDs, fiberEnd, baseline)

	return &Analysis{
		TraceID:       t.ID,
		Calibration:   cal,
		DeadZoneNS:    deadZoneNS,
		DeadZoneM:     deadM,
		SampleCount:   n,
		FiberEndIndex: fiberEnd,
		FiberEndM:     cal.OneWayMeters(fiberEnd),
		Truncated:     truncated,
		LengthBound:   lengthBound,
		BaselineDB:    baseline,
		Breakpoints:   breaks,
		Events:        events,
		Ghosts:        ghosts,
		Losses:        losses,
		TotalLossDB:   total,
	}
}

type span struct{ lo, hi int }

func clusterMask(mask []bool) []span {
	var out []span
	i := 0
	for i < len(mask) {
		if !mask[i] {
			i++
			continue
		}
		j := i
		for j < len(mask) && mask[j] {
			j++
		}
		if j-i >= reflectMinClusterSamples {
			out = append(out, span{i, j - 1})
		}
		i = j
	}
	return out
}

func buildConnector(t *RawTrace, cal Calibration, c span, smooth, residual []float64) Event {
	peak := c.lo
	best := residual[c.lo]
	for k := c.lo; k <= c.hi; k++ {
		if residual[k] > best {
			best = residual[k]
			peak = k
		}
	}
	lo := c.lo - stepSkipTransient
	hi := c.hi + stepSkipTransient
	leftLo := maxInt(0, c.lo-stepSkipTransient-stepMeasureHalfWindow)
	leftHi := maxInt(0, c.lo-stepSkipTransient-1)
	rightLo := minInt(len(smooth)-1, c.hi+stepSkipTransient+1)
	rightHi := minInt(len(smooth)-1, c.hi+stepSkipTransient+stepMeasureHalfWindow)
	left := medianRange(smooth, leftLo, leftHi)
	right := medianRange(smooth, rightLo, rightHi)
	loss := left - right
	if loss < 0 {
		loss = 0
	}
	return Event{
		ID:         eventID(t.ID, peak),
		TraceID:    t.ID,
		Kind:       KindConnector,
		StartIndex: maxInt(0, lo),
		EndIndex:   minInt(len(smooth)-1, hi),
		PeakIndex:  peak,
		StartM:     cal.OneWayMeters(maxInt(0, lo)),
		EndM:       cal.OneWayMeters(minInt(len(smooth)-1, hi)),
		PeakM:      cal.OneWayMeters(peak),
		LossDB:     loss,
		PeakDB:     best,
		Strong:     best >= strongReflectPeakDB,
	}
}

type stepPick struct {
	idx, lo, hi int
	drop        float64
}

func detectSteps(p []float64, fiberEnd, deadSamples int, blocked []bool) []stepPick {
	type cand struct {
		idx  int
		drop float64
	}
	var cands []cand
	for i := deadSamples; i <= fiberEnd; i++ {
		if blocked[i] {
			continue
		}
		llo := i - stepSkipTransient - stepMeasureHalfWindow
		lhi := i - stepSkipTransient
		rlo := i + stepSkipTransient
		rhi := i + stepSkipTransient + stepMeasureHalfWindow
		if llo < 0 || rhi > fiberEnd {
			continue
		}
		leftLine := lineLevel(p, llo, lhi, lhi)
		rightLine := lineLevel(p, rlo, rhi, rlo)
		drop := leftLine - rightLine
		if drop < stepMinDropDB {
			continue
		}
		// Robust corroboration: the shoulder medians must agree that this is
		// a real level step rather than a slope fit of noise.
		mDrop := medianRange(p, llo, lhi) - medianRange(p, rlo, rhi)
		if mDrop < stepMedianGateDB {
			continue
		}
		cands = append(cands, cand{i, drop})
	}
	// Non-maximum suppression: keep the strongest pick within each radius.
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].drop > cands[j].drop })
	kept := make([]stepPick, 0, len(cands))
	taken := make([]bool, len(cands))
	for ci, c := range cands {
		if taken[ci] {
			continue
		}
		taken[ci] = true
		for cj := ci + 1; cj < len(cands); cj++ {
			if !taken[cj] && absInt(cands[cj].idx-c.idx) <= nmsHalfWindow {
				taken[cj] = true
			}
		}
		kept = append(kept, stepPick{
			idx:  c.idx,
			lo:   c.idx - stepSkipTransient,
			hi:   c.idx + stepSkipTransient,
			drop: c.drop,
		})
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].idx < kept[j].idx })
	out := make([]stepPick, 0, len(cands))
	out = append(out, kept...)
	return out
}

func medianRange(x []float64, lo, hi int) float64 {
	if hi < lo {
		lo, hi = hi, lo
	}
	lo = clamp(lo, 0, len(x)-1)
	hi = clamp(hi, 0, len(x)-1)
	return medianOf(x, lo, hi)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// lineLevel evaluates a least-squares line fit on p[lo:hi+1] at index at.
func lineLevel(p []float64, lo, hi, at int) float64 {
	if hi < lo {
		return p[clamp(at, 0, len(p)-1)]
	}
	var sx, sy, sxx, sxy float64
	k := 0
	for i := lo; i <= hi; i++ {
		x := float64(i)
		y := p[i]
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
		k++
	}
	f := float64(k)
	den := f*sxx - sx*sx
	if math.Abs(den) < 1e-12 {
		return sy / f
	}
	slope := (f*sxy - sx*sy) / den
	intercept := (sy - slope*sx) / f
	return slope*float64(at) + intercept
}

func detectFiberEnd(smooth []float64) int {
	n := len(smooth)
	startLevel := smooth[minInt(n-1, endWindowSamples)]
	floorLevel := startLevel - floorBelowSignalDB
	for i := n - 1; i >= 0; i-- {
		if smooth[i] > floorLevel {
			return i
		}
	}
	return n - 1
}

func detectGhosts(traceID string, events []Event) []GhostCandidate {
	var strong []Event
	for _, e := range events {
		if e.Kind == KindConnector && e.Strong {
			strong = append(strong, e)
		}
	}
	var out []GhostCandidate
	for _, e := range events {
		if e.Kind != KindConnector || e.Strong {
			continue
		}
		best := -1.0
		bestRatio := 0.0
		for si, s := range strong {
			if s.PeakM <= 0 {
				continue
			}
			ratio := e.PeakM / s.PeakM
			if ratio >= ghostRatioMin && ratio <= ghostRatioMax && e.LossDB <= ghostMaxLossDB {
				if best < 0 || math.Abs(ratio-2.0) < math.Abs(bestRatio-2.0) {
					best = float64(si)
					bestRatio = ratio
				}
			}
		}
		if best >= 0 {
			s := strong[int(best)]
			out = append(out, GhostCandidate{
				GhostEventID:  e.ID,
				ParentEventID: s.ID,
				ParentM:       s.PeakM,
				GhostM:        e.PeakM,
				Ratio:         bestRatio,
				LossDB:        e.LossDB,
				Reason: fmt.Sprintf(
					"reflection at %.1f m is %.2fx the strong reflection at %.1f m with no level drop (%.3f dB)",
					e.PeakM, bestRatio, s.PeakM, e.LossDB),
			})
		}
	}
	return out
}

func eventID(traceID string, sampleIndex int) string {
	return fmt.Sprintf("E:%s:%d", traceID, sampleIndex)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
