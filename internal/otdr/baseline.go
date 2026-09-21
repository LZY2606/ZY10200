package otdr

import (
	"fmt"
	"sort"
)

// piecewiseBaseline fits line segments between loss events. Breakpoints sit
// at every splice/connector window edge, so the baseline "clicks down" at the
// events rather than smearing their loss across the fiber.
func piecewiseBaseline(p []float64, events []Event, fiberEnd int, cal Calibration) ([]float64, []Breakpoint) {
	n := len(p)
	base := make([]float64, n)

	type joint struct{ idx int }
	bounds := []int{0}
	seen := map[int]bool{0: true}
	for _, e := range events {
		if e.Kind == KindTruncation {
			continue
		}
		for _, idx := range []int{e.StartIndex, e.EndIndex} {
			idx = clamp(idx, 0, fiberEnd)
			if !seen[idx] {
				seen[idx] = true
				bounds = append(bounds, idx)
			}
		}
	}
	bounds = append(bounds, fiberEnd)
	sort.Ints(bounds)

	// Merge bounds that are too close to fit a segment.
	merged := []int{bounds[0]}
	for _, b := range bounds[1:] {
		if b-merged[len(merged)-1] >= 2 {
			merged = append(merged, b)
		}
	}
	bounds = merged

	exclude := make([]bool, n)
	for _, e := range events {
		if e.Kind == KindConnector {
			for k := e.StartIndex; k <= e.EndIndex; k++ {
				exclude[k] = true
			}
		}
	}

	breakpoints := make([]Breakpoint, 0, len(bounds))
	for seg := 0; seg < len(bounds)-1; seg++ {
		lo, hi := bounds[seg], bounds[seg+1]
		var sx, sy, sxx, sxy float64
		k := 0
		for i := lo; i <= hi; i++ {
			if exclude[i] {
				continue
			}
			x := float64(i)
			y := p[i]
			sx += x
			sy += y
			sxx += x * x
			sxy += x * y
			k++
		}
		var slope, intercept float64
		if k >= 2 {
			f := float64(k)
			den := f*sxx - sx*sx
			slope = (f*sxy - sx*sy) / den
			intercept = (sy - slope*sx) / f
		} else {
			intercept = p[clamp(lo, 0, n-1)]
		}
		for i := lo; i <= hi && i < n; i++ {
			base[i] = slope*float64(i) + intercept
		}
		if seg == 0 {
			breakpoints = append(breakpoints, Breakpoint{lo, cal.OneWayMeters(lo), base[lo]})
		}
		val := slope*float64(hi) + intercept
		breakpoints = append(breakpoints, Breakpoint{hi, cal.OneWayMeters(hi), val})
	}
	// Beyond the fiber end (noise floor) hold the last value.
	if fiberEnd < n-1 {
		v := base[fiberEnd]
		for i := fiberEnd + 1; i < n; i++ {
			base[i] = v
		}
	}
	return base, breakpoints
}

// buildLosses merges overlapping event windows into one group each, then
// measures each group's drop once from the piecewise baseline. Events inside
// one group cannot be double counted.
func buildLosses(traceID string, cal Calibration, events []Event, ghostEventIDs map[string]bool, fiberEnd int, baseline []float64) ([]LossItem, float64) {
	type group struct {
		lo, hi  int
		eventIDs []string
	}
	measurable := make([]Event, 0, len(events))
	for _, e := range events {
		if (e.Kind == KindSplice || e.Kind == KindConnector) && !ghostEventIDs[e.ID] {
			measurable = append(measurable, e)
		}
	}
	sort.SliceStable(measurable, func(i, j int) bool {
		return measurable[i].StartIndex < measurable[j].StartIndex
	})

	var groups []group
	for _, e := range measurable {
		if len(groups) == 0 || e.StartIndex > groups[len(groups)-1].hi {
			groups = append(groups, group{lo: e.StartIndex, hi: e.EndIndex, eventIDs: []string{e.ID}})
			continue
		}
		g := &groups[len(groups)-1]
		if e.EndIndex > g.hi {
			g.hi = e.EndIndex
		}
		g.eventIDs = append(g.eventIDs, e.ID)
	}

	items := make([]LossItem, 0, len(groups))
	total := 0.0
	for gi, g := range groups {
		pre := clamp(g.lo-3, 0, fiberEnd)
		post := clamp(g.hi+3, 0, fiberEnd)
		loss := baseline[pre] - baseline[post]
		if loss < 0 {
			loss = 0
		}
		ids := append([]string{}, g.eventIDs...)
		items = append(items, LossItem{
			GroupID:    fmt.Sprintf("L:%s:%d", traceID, gi),
			StartIndex: g.lo,
			EndIndex:   g.hi,
			StartM:     cal.OneWayMeters(g.lo),
			EndM:       cal.OneWayMeters(g.hi),
			LossDB:     loss,
			EventIDs:   ids,
		})
		total += loss
	}
	return items, total
}

// CumulativeDB returns the cumulative event-loss budget at each sample: a
// step function that jumps by each non-overlapping group's loss.
func (a *Analysis) CumulativeDB() []float64 {
	n := a.SampleCount
	cum := make([]float64, n)
	i := 0
	acc := 0.0
	for _, g := range a.Losses {
		for ; i < n && i < g.StartIndex; i++ {
			cum[i] = acc
		}
		acc += g.LossDB
		i = g.EndIndex + 1
	}
	for ; i < n; i++ {
		cum[i] = acc
	}
	return cum
}

// Note renders the audit note describing the distance conversion inputs.
func (a *Analysis) Note() DistanceNote {
	return DistanceNote{
		TraceID:        a.TraceID,
		SampleCount:    a.SampleCount,
		DTNS:           a.Calibration.DTNS,
		T0NS:           a.Calibration.T0NS,
		GroupIndex:     a.Calibration.GroupIndex,
		SpatialSampleM: a.Calibration.SpatialSampleMeters(),
		DeadZoneNS:     a.DeadZoneNS,
		DeadZoneM:      a.DeadZoneM,
	}
}
