package main

import (
	"fmt"
	"echoeventroom/internal/otdr"
)

func main() {
	for _, s := range otdr.DefaultFixtures() {
		t := otdr.BuildFixture(s)
		cal := otdr.Calibration{T0NS: t.T0NS, DTNS: t.DTNS, GroupIndex: t.GroupIndex}
		a := otdr.Analyze(t, cal, otdr.DefaultDeadZoneNS(t))
		fmt.Printf("== %s samples=%d dspace=%.4fm fiberEnd=%d(%.1fm) trunc=%v bound=%s totalLoss=%.3f\n",
			t.ID, len(t.PowerDB), cal.SpatialSampleMeters(), a.FiberEndIndex, a.FiberEndM, a.Truncated, a.LengthBound, a.TotalLossDB)
		for _, e := range a.Events {
			fmt.Printf("   %-10s win=[%d,%d] peak=%d @%.1fm loss=%.3f peakDB=%.2f strong=%v\n",
				e.Kind, e.StartIndex, e.EndIndex, e.PeakIndex, e.PeakM, e.LossDB, e.PeakDB, e.Strong)
		}
		for _, g := range a.Ghosts {
			fmt.Printf("   GHOST %s <- %s ratio=%.3f loss=%.3f\n", g.GhostEventID, g.ParentEventID, g.Ratio, g.LossDB)
		}
		for _, l := range a.Losses {
			fmt.Printf("   LOSS %s [%d,%d] @%.1f-%.1fm = %.3f events=%v\n", l.GroupID, l.StartIndex, l.EndIndex, l.StartM, l.EndM, l.LossDB, l.EventIDs)
		}
	}
}
