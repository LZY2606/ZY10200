package web

import (
	"fmt"
	"html"
	"math"
	"strings"

	"otdrroom/internal/app"
	"otdrroom/internal/otdr"
)

// RenderOverlaySVG 把两条轨迹画在同一米制坐标轴上。
// 只有用户显式确认的对应（links）才会连线；距离接近的峰不自动等同。
func RenderOverlaySVG(p app.AlignedPair) string {
	const h = 230
	plotW := float64(chartW - padL - padR)
	xMax := p.MaxDistanceM
	if xMax <= 0 {
		xMax = 1
	}
	var combined []float64
	combined = append(combined, p.A.DB...)
	combined = append(combined, p.B.DB...)
	dbLo, dbHi := dataRange(combined)

	g := xy{left: padL, width: plotW, top: 40, height: h - 60,
		x0: 0, x1: xMax, y0: dbLo, y1: dbHi}

	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg class="chart" viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg">`,
		chartW, h)
	fmt.Fprintf(&b, `<rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" fill="#fbfcfe" stroke="#d0d7de"/>`,
		g.left, g.top, g.width, g.height)
	fmt.Fprintf(&b, `<text x="8" y="22" class="pane-title">距离校准对齐（同米制坐标；只有已确认对应的事件才连线）</text>`)

	for m := 0.0; m <= xMax+1; m += 200 {
		x := g.X(m)
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.0f" x2="%.1f" y2="%.0f" stroke="#eef0f3"/>`,
			x, g.top, x, g.top+g.height)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.0f" class="tick" text-anchor="middle">%.0fm</text>`,
			x, g.top+g.height+14, m)
	}

	drawOverlayTrace(&b, p.A, g, "#1f6feb", 0.10)
	drawOverlayTrace(&b, p.B, g, "#bc4c00", 0.10)

	drawOverlayEvents(&b, p.A, g, "#1f6feb")
	drawOverlayEvents(&b, p.B, g, "#bc4c00")

	// 用户确认的跨轨迹对应：在两条轨迹的锚点事件之间画虚线。
	for _, l := range p.Links {
		ea := findAnchor(p.A, l.TraceA, l.AnchorA, l.TraceB, l.AnchorB)
		eb := findAnchor(p.B, l.TraceA, l.AnchorA, l.TraceB, l.AnchorB)
		if ea == nil || eb == nil {
			continue
		}
		fmt.Fprintf(&b,
			`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#1a7f37" stroke-width="1.6" stroke-dasharray="5 4"/>`,
			g.X(ea.DistanceM), g.Y(p.A.DB[clamp(ea.AnchorIndex, 0, p.A.SampleCount-1)]),
			g.X(eb.DistanceM), g.Y(p.B.DB[clamp(eb.AnchorIndex, 0, p.B.SampleCount-1)]))
	}

	fmt.Fprintf(&b, `<rect x="%d" y="8" width="10" height="10" fill="#1f6feb"/><text x="%d" y="17" class="legend">%s n=%.4f</text>`,
		chartW-250, chartW-236, html.EscapeString(p.A.TraceCode), p.A.GroupIndex)
	fmt.Fprintf(&b, `<rect x="%d" y="8" width="10" height="10" fill="#bc4c00"/><text x="%d" y="17" class="legend">%s n=%.4f</text>`,
		chartW-120, chartW-106, html.EscapeString(p.B.TraceCode), p.B.GroupIndex)
	b.WriteString(`</svg>`)
	return b.String()
}

func drawOverlayTrace(b *strings.Builder, it otdr.Interpretation, g xy, color string, alpha float64) {
	zeroFloor := otdr.SampleDB(1e-12)
	d := downsamplePath(func(i int) (x, y float64, zero bool) {
		v := it.DB[i]
		if math.Abs(v-zeroFloor) < 0.01 {
			return 0, 0, true
		}
		xx := it.LastM * float64(i) / float64(it.SampleCount-1)
		return g.X(xx), g.Y(v), false
	}, it.SampleCount, 640)
	fmt.Fprintf(b, `<path d="%s" fill="none" stroke="%s" stroke-width="1.1" opacity="%.2f"/>`,
		d, color, 1-alpha)
}

func drawOverlayEvents(b *strings.Builder, it otdr.Interpretation, g xy, color string) {
	for _, ev := range it.Events {
		x := g.X(ev.DistanceM)
		r := 3.2
		if ev.Kind == otdr.KindGhost {
			fmt.Fprintf(b, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="none" stroke="%s" stroke-width="1.4"/>`,
				x, g.Y(it.DB[clamp(ev.AnchorIndex, 0, it.SampleCount-1)]), r, color)
		} else {
			fmt.Fprintf(b, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="%s"/>`,
				x, g.Y(it.DB[clamp(ev.AnchorIndex, 0, it.SampleCount-1)]), r, color)
		}
	}
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

func findAnchor(it otdr.Interpretation, tA int64, aA int, tB int64, aB int) *otdr.Event {
	var idx int
	if it.TraceID == tA {
		idx = aA
	} else if it.TraceID == tB {
		idx = aB
	} else {
		return nil
	}
	return it.EventByAnchor(idx)
}
