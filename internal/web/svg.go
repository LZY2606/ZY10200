package web

import (
	"fmt"
	"html"
	"math"
	"strings"

	"otdrroom/internal/otdr"
)

const (
	chartW = 980
	paneH  = 104
	padL   = 64
	padR   = 18
)

// xy 把米制距离与数值映射到面板像素。
type xy struct {
	left, width, top, height float64
	x0, x1, y0, y1           float64
}

func (g xy) X(x float64) float64 {
	return g.left + (x-g.x0)/(g.x1-g.x0)*g.width
}
func (g xy) Y(y float64) float64 {
	return g.top + g.height - (y-g.y0)/(g.y1-g.y0)*g.height
}

func (g xy) plotWidth() float64 { return g.width }

// RenderTraceSVG 渲染单条轨迹四联面板：
// 原始线性采样、对数功率与分段基线、事件候选、累积损耗。
func RenderTraceSVG(it otdr.Interpretation) string {
	plotW := float64(chartW - padL - padR)
	totalH := 4*paneH + 58
	xMax := it.LastM
	if xMax <= 0 {
		xMax = 1
	}
	dbLo, dbHi := dataRange(it.DB)
	cumHi := math.Max(it.TotalLossDB, 0.1)

	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg class="chart" viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg">`,
		chartW, totalH)

	titles := []string{
		"① 原始采样（线性功率；截断后无回波，留空）",
		"② 对数功率 dB 与分段基线",
		"③ 事件候选（事件窗 + 锚点采样）",
		"④ 累积事件损耗 dB（重叠窗取并集，不重复计损）",
	}

	for pi, title := range titles {
		top := 16 + float64(pi*paneH)
		var y0, y1 float64
		switch pi {
		case 0:
			y0, y1 = 0, 1
		case 1:
			y0, y1 = dbLo, dbHi
		case 2:
			y0, y1 = 0, 1
		default:
			y0, y1 = 0, cumHi
		}
		g := xy{left: padL, width: plotW, top: top, height: paneH - 28,
			x0: 0, x1: xMax, y0: y0, y1: y1}

		drawPaneFrame(&b, g, title, it, pi == len(titles)-1)
		switch pi {
		case 0:
			drawLinear(&b, it, g)
		case 1:
			drawSeries(&b, it, it.DB, g, "#1f6feb", 1, false)
			drawSeries(&b, it, it.Baseline, g, "#fb8f44", 1.6, false)
		case 2:
			drawEvents(&b, it, g)
		default:
			drawSeries(&b, it, it.CumLoss, g, "#1a7f37", 1.8, true)
			fmt.Fprintf(&b, `<text x="%d" y="%.0f" class="axis-label">合计 %.3f dB</text>`,
				chartW-padR-90, top+14, it.TotalLossDB)
		}
	}

	fmt.Fprintf(&b,
		`<text x="%d" y="%d" class="axis-title">米制距离（按群折射率 n=%.4f 校准） · 样本数 %d · 参数修订 rev=%d · 死区 %.0fm</text>`,
		padL, totalH-10, it.GroupIndex, it.SampleCount, it.Rev, it.DeadzoneM)
	b.WriteString(`</svg>`)
	return b.String()
}

func drawPaneFrame(b *strings.Builder, g xy, title string, it otdr.Interpretation, xLabels bool) {
	fmt.Fprintf(b, `<rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" fill="#fbfcfe" stroke="#d0d7de"/>`,
		g.left, g.top, g.width, g.height)
	fmt.Fprintf(b, `<text x="8" y="%.0f" class="pane-title">%s</text>`,
		g.top+14, html.EscapeString(title))
	// 发射端死区阴影
	dx := g.X(math.Min(it.DeadzoneM, g.x1))
	fmt.Fprintf(b, `<rect x="%.0f" y="%.0f" width="%.1f" height="%.0f" fill="#fff8c5" opacity="0.75"/>`,
		g.left, g.top, dx-g.left, g.height)
	for m := 0.0; m <= g.x1+1; m += 200 {
		x := g.X(m)
		fmt.Fprintf(b, `<line x1="%.1f" y1="%.0f" x2="%.1f" y2="%.0f" stroke="#eef0f3"/>`,
			x, g.top, x, g.top+g.height)
		if xLabels {
			fmt.Fprintf(b, `<text x="%.1f" y="%.0f" class="tick" text-anchor="middle">%.0fm</text>`,
				x, g.top+g.height+14, m)
		}
	}
}

func sampleX(it otdr.Interpretation, i int, xMax float64) float64 {
	if it.SampleCount <= 1 {
		return 0
	}
	return xMax * float64(i) / float64(it.SampleCount-1)
}

func drawSeries(b *strings.Builder, it otdr.Interpretation, vals []float64,
	g xy, color string, width float64, gapZero bool) {
	zeroFloor := otdr.SampleDB(1e-12)
	d := downsamplePath(func(i int) (x, y float64, zero bool) {
		if gapZero && math.Abs(vals[i]-zeroFloor) < 0.01 {
			return 0, 0, true
		}
		return g.X(sampleX(it, i, g.x1)), g.Y(vals[i]), false
	}, len(vals), 640)
	fmt.Fprintf(b, `<path d="%s" fill="none" stroke="%s" stroke-width="%.1f"/>`, d, color, width)
}

// downsamplePath 把逐采样折线降采样到最多 maxPts 个有效点，采用每桶
// min/max 包络：既能压缩体积，又不会削平反射尖峰。
func downsamplePath(pt func(i int) (x, y float64, zero bool), n, maxPts int) string {
	var d strings.Builder
	mov := true
	var bx0, bx1, byMin, byMax float64
	flush := func(has bool) {
		if !has {
			return
		}
		// 先画桶内低点再画高点，视觉上形成尖峰包络。
		cmd := "L"
		if mov {
			cmd = "M"
			mov = false
		}
		fmt.Fprintf(&d, "%s%.1f,%.1f L%.1f,%.1f ", cmd, bx0, byMin, bx1, byMax)
	}
	has := false
	for i := 0; i < n; i++ {
		x, y, zero := pt(i)
		if zero {
			flush(has)
			has = false
			mov = true
			continue
		}
		if !has {
			bx0, bx1, byMin, byMax = x, x, y, y
			has = true
			continue
		}
		if x-bx0 > 2.0 {
			flush(has)
			bx0, bx1, byMin, byMax = x, x, y, y
			has = true
			continue
		}
		bx1 = x
		if y < byMin {
			byMin = y
		}
		if y > byMax {
			byMax = y
		}
	}
	flush(has)
	return strings.TrimSpace(d.String())
}

func drawLinear(b *strings.Builder, it otdr.Interpretation, g xy) {
	zeroFloor := otdr.SampleDB(1e-12)
	d := downsamplePath(func(i int) (x, y float64, zero bool) {
		if math.Abs(it.DB[i]-zeroFloor) < 0.01 {
			return 0, 0, true
		}
		v := math.Pow(10, it.DB[i]/10.0)
		return g.X(sampleX(it, i, g.x1)), g.Y(math.Min(1, v*1.5)), false
	}, it.SampleCount, 640)
	fmt.Fprintf(b, `<path d="%s" fill="none" stroke="#57606a" stroke-width="0.8"/>`, d)
}

func drawEvents(b *strings.Builder, it otdr.Interpretation, g xy) {
	midY := g.top + g.height/2
	for _, ev := range it.Events {
		col := kindColor(ev.Kind)
		x0, x1 := g.X(ev.StartM), g.X(ev.EndM)
		fmt.Fprintf(b, `<rect x="%.1f" y="%.0f" width="%.1f" height="%.0f" fill="%s" opacity="0.16" stroke="%s"/>`,
			x0, g.top+6, math.Max(2, x1-x0), g.height-12, col, col)
		ax := g.X(ev.DistanceM)
		fmt.Fprintf(b, `<line x1="%.1f" y1="%.0f" x2="%.1f" y2="%.0f" stroke="%s" stroke-width="1.4"/>`,
			ax, g.top+4, ax, g.top+g.height-4, col)
		fmt.Fprintf(b, `<text x="%.1f" y="%.0f" class="ev-label" fill="%s">%s</text>`,
			ax, midY-2, col, html.EscapeString(otdr.EventLabel(ev.Kind)))
		if ev.Kind == otdr.KindTruncation {
			fmt.Fprintf(b, `<text x="%.1f" y="%.0f" class="ev-sub">长度 ≥ %.1fm</text>`,
				ax, midY+12, ev.DistanceM)
		}
	}
}

func kindColor(k otdr.Kind) string {
	switch k {
	case otdr.KindReflection:
		return "#0969da"
	case otdr.KindSplice:
		return "#9a6700"
	case otdr.KindGhost:
		return "#8250df"
	case otdr.KindTruncation:
		return "#cf222e"
	case otdr.KindFiberEnd:
		return "#1a7f37"
	}
	return "#57606a"
}

func dataRange(xs []float64) (lo, hi float64) {
	lo, hi = math.MaxFloat64, -math.MaxFloat64
	for _, v := range xs {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	pad := (hi - lo) * 0.08
	return lo - pad, hi + pad
}
