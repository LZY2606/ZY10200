package otdr

import (
	"sort"
)

// mergedWindow 是并集去重后的事件窗。
type mergedWindow struct {
	start, end int
	events     []int // 落在该并集窗内的事件下标
}

type segFit struct {
	lo, hi    int
	intercept float64
	slope     float64
}

// unionWindows 把互相重叠/相邻的事件窗合并为不相交并集窗。
func unionWindows(events []Event, n int) []mergedWindow {
	type ws struct{ s, e, idx int }
	var all []ws
	for i := range events {
		all = append(all, ws{events[i].StartIndex, events[i].EndIndex, i})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].s < all[j].s })
	var out []mergedWindow
	for _, w := range all {
		if len(out) == 0 || w.s > out[len(out)-1].end+1 {
			out = append(out, mergedWindow{start: w.s, end: w.e, events: []int{w.idx}})
			continue
		}
		m := &out[len(out)-1]
		if w.e > m.end {
			m.end = w.e
		}
		m.events = append(m.events, w.idx)
	}
	return out
}

// segmentedBaseline 在事件窗外做 OLS 拟合，事件窗内线性桥接。
// 返回逐采样基线与“基线拐点”（每段端点）列表。
func segmentedBaseline(db []float64, events []Event, params TraceParams) ([]float64, []Anchor, []segFit) {
	n := len(db)
	base := make([]float64, n)
	wins := unionWindows(events, n)
	var anchors []Anchor

	// 收集所有“清洁区间”（事件窗之间）并各自 OLS。
	var segs []segFit
	cursor := 0
	for _, w := range wins {
		if cursor < w.start-1 {
			segs = append(segs, segFit{lo: cursor, hi: w.start - 1})
		}
		cursor = w.end + 1
	}
	if cursor < n {
		segs = append(segs, segFit{lo: cursor, hi: n - 1})
	}

	for si := range segs {
		s := &segs[si]
		a, b := robustSegmentFit(db, s.lo, s.hi)
		s.intercept, s.slope = a, b
		for i := s.lo; i <= s.hi; i++ {
			base[i] = a + b*float64(i)
		}
	}

	// 在每个事件窗内，用前后清洁段的端点电平做线性桥接。
	for _, w := range wins {
		leftLevel, rightLevel := 0.0, 0.0
		haveL, haveR := false, false
		if s := prevSeg(segs, w.start); s != nil {
			leftLevel = s.intercept + s.slope*float64(w.start-1)
			haveL = true
		}
		if s := nextSeg(segs, w.end); s != nil {
			rightLevel = s.intercept + s.slope*float64(w.end+1)
			haveR = true
		}
		switch {
		case haveL && haveR:
			for i := w.start; i <= w.end; i++ {
				t := float64(i-(w.start-1)) / float64(w.end+1-(w.start-1))
				base[i] = leftLevel + t*(rightLevel-leftLevel)
			}
		case haveL:
			for i := w.start; i <= w.end; i++ {
				base[i] = leftLevel
			}
		case haveR:
			for i := w.start; i <= w.end; i++ {
				base[i] = rightLevel
			}
		default:
			for i := w.start; i <= w.end; i++ {
				base[i] = 0
			}
		}
		if haveL {
			anchors = appendAnchor(anchors, params, w.start)
		}
		if haveR {
			anchors = appendAnchor(anchors, params, w.end)
		}
	}

	// 极端情况下没有任何清洁段（理论上夹具不会发生）。
	for i := range base {
		if base[i] == 0 && db[i] != 0 {
			base[i] = db[i]
		}
	}
	return base, anchors, segs
}

// robustSegmentFit 估计一个清洁区间的基线电平与斜率。
// OLS 对残留的发射反射尾很敏感（会把首段斜率拉陡）。这里先取窗口中值
// 作为无峰电平，再用 MAD 只剔除“高于”基线的离群点（反射尾），
// 最后做 OLS——熔接台阶表现为区间外的电平差，不会被当离群点删除。
func robustSegmentFit(db []float64, lo, hi int) (intercept, slope float64) {
	n := hi - lo + 1
	if n < 4 {
		return linfit(db, lo, hi)
	}
	half := minInt(maxInt(3, n/40), 25)
	level := make([]float64, n)
	for i := lo; i <= hi; i++ {
		a := clampInt(i-half, lo, hi)
		b := clampInt(i+half, lo, hi)
		level[i-lo] = median(db[a : b+1])
	}
	intercept0, slope0 := linfit(level, 0, n-1)
	// 两轮迭代剔除正残差（反射尾）。
	for iter := 0; iter < 2; iter++ {
		res := make([]float64, 0, n)
		for i := 0; i < n; i++ {
			d := level[i] - (intercept0 + slope0*float64(i))
			if d < 0 {
				d = -d
			}
			res = append(res, d)
		}
		mad := median(res)
		if mad < 1e-6 {
			break
		}
		var xs, ys, ww []float64
		for i := 0; i < n; i++ {
			pos := level[i] - (intercept0 + slope0*float64(i))
			if pos > 3.5*mad {
				continue // 反射尾离群点
			}
			xs = append(xs, float64(i))
			ys = append(ys, level[i])
			ww = append(ww, 1)
		}
		if len(xs) < 4 {
			break
		}
		intercept0, slope0 = weightedLinfit(xs, ys, ww)
	}
	// linfit 以局部索引 0..n-1 为 x，换回全局采样索引的截距。
	return intercept0 - slope0*float64(lo), slope0
}

// weightedLinfit 等权重 OLS（保留参数以便后续加权扩展）。
func weightedLinfit(xs, ys, ws []float64) (a, b float64) {
	var sw, sx, sy, sxx, sxy float64
	for i := range xs {
		w := ws[i]
		sw += w
		sx += w * xs[i]
		sy += w * ys[i]
		sxx += w * xs[i] * xs[i]
		sxy += w * xs[i] * ys[i]
	}
	den := sw*sxx - sx*sx
	if den == 0 {
		return sy / sw, 0
	}
	b = (sw*sxy - sx*sy) / den
	a = (sy - b*sx) / sw
	return a, b
}

func appendAnchor(in []Anchor, params TraceParams, idx int) []Anchor {
	a := Anchor{Index: idx, DistanceM: SampleDistanceM(params, idx)}
	if len(in) > 0 && in[len(in)-1].Index == idx {
		return in
	}
	return append(in, a)
}

func prevSeg(segs []segFit, start int) *segFit {
	for i := range segs {
		if segs[i].hi < start && (i == len(segs)-1 || segs[i+1].lo > start) {
			return &segs[i]
		}
	}
	return nil
}

func nextSeg(segs []segFit, end int) *segFit {
	for i := range segs {
		if segs[i].lo > end {
			return &segs[i]
		}
	}
	return nil
}

// plateauMeds 返回事件窗左侧与右侧各 plateau 宽“高原”的中值电平。
// 高原取在屏蔽了反射拖尾的电平序列上，因此不被峰污染；左右高原尺寸相同，
// 光纤前向衰减在两侧的贡献相同，相减时自动抵消。
func plateauMeds(level []float64, ev Event, plateau int) (left, right float64, ok bool) {
	lHi := ev.StartIndex - plateau - 1
	lLo := ev.StartIndex - 2*plateau
	rLo := ev.EndIndex + plateau + 1
	rHi := ev.EndIndex + 2*plateau
	if lLo < 0 || rHi >= len(level) {
		return 0, 0, false
	}
	return plateauLevel(level, lLo, lHi, ev.AnchorIndex),
		plateauLevel(level, rLo, rHi, ev.AnchorIndex), true
}

// refineEventLoss 用事件窗外两侧高原中值电平差复核台阶损耗。
// 幽灵/截断没有真实台阶，损耗保持 0。
func refineEventLoss(ev *Event, level []float64, plateau int) {
	switch ev.Kind {
	case KindSplice, KindReflection:
		if l, r, ok := plateauMeds(level, *ev, plateau); ok {
			if d := l - r; d > 0 {
				ev.LossDB = d
			} else {
				ev.LossDB = 0
			}
		}
	}
}

// cumulativeLoss 逐采样给出累计事件损耗。
// 重叠事件窗先取并集，并集窗的损耗按分段基线的“净下降”只计一次，
// 因而两个事件窗重叠时绝不会重复计算损耗。幽灵/截断窗没有真实净下降，
// 自然贡献为 0。
func cumulativeLoss(events []Event, level []float64, plateau, n int) ([]float64, float64) {
	out := make([]float64, n)
	wins := unionWindows(events, n)
	total := 0.0
	winLoss := make([]float64, len(wins))
	for wi, w := range wins {
		// 多个重叠事件共享同一并集窗：用并集窗两侧高原中值的电平差
		// 统计一次净下降，绝不重复计损。幽灵/截断（右侧为零缺口）不参与。
		lHi := w.start - plateau - 1
		lLo := w.start - 2*plateau
		rLo := w.end + plateau + 1
		rHi := w.end + 2*plateau
		if lLo < 0 || rHi >= n {
			winLoss[wi] = 0
			continue
		}
		zeroFloor := SampleDB(1e-12)
		mid := (w.start + w.end) / 2
		lm := plateauLevel(level, lLo, lHi, mid)
		rm := plateauLevel(level, rLo, rHi, mid)
		if lm < zeroFloor+2 || rm < zeroFloor+2 {
			winLoss[wi] = 0
			continue
		}
		drop := lm - rm
		if drop < 0 {
			drop = 0
		}
		winLoss[wi] = drop
	}
	cum := 0.0
	for wi, w := range wins {
		for i := 0; i < w.start && i < n; i++ {
			out[i] = cum
		}
		add := winLoss[wi]
		for i := w.start; i <= w.end && i < n; i++ {
			t := float64(i-w.start+1) / float64(w.end-w.start+1)
			out[i] = cum + t*add
		}
		cum += add
		total += add
	}
	for i := 0; i < n; i++ {
		if out[i] == 0 && (len(wins) == 0 || i > wins[len(wins)-1].end) {
			out[i] = total
		}
	}
	return out, total
}
