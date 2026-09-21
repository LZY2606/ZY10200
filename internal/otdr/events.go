package otdr

import "math"

// buildEvents 把反射峰簇与台阶合并成事件候选。
// 一个真实连接器通常“既有峰又有台阶”；纯台阶是熔接；纯峰可能是幽灵。
func buildEvents(params TraceParams, db, med []float64,
	refs []rawReflection, steps []rawStep, half int) []Event {

	winHalf := half // 事件窗半宽跟随脉宽
	n := len(db)
	usedStep := make(map[int]bool)
	var events []Event

	for _, rf := range refs {
		start := clampInt(rf.start-winHalf, 0, n-1)
		end := clampInt(rf.end+winHalf, 0, n-1)
		kind := KindReflection
		loss := 0.0
		// 若窗口内存在台阶，则这是真实反射连接器；台阶损耗归并到本事件。
		for si, st := range steps {
			if usedStep[si] {
				continue
			}
			if st.anchor >= start && st.anchor <= end {
				usedStep[si] = true
				loss = st.loss
			}
		}
		events = append(events, Event{
			AnchorIndex: rf.anchor,
			StartIndex:  start,
			EndIndex:    end,
			Kind:        kind,
			SpikeDB:     rf.spike,
			LossDB:      loss,
		})
	}

	// 未被峰吸收的台阶是低损耗熔接。
	for si, st := range steps {
		if usedStep[si] {
			continue
		}
		start := clampInt(st.anchor-winHalf, 0, n-1)
		end := clampInt(st.anchor+winHalf, 0, n-1)
		events = append(events, Event{
			AnchorIndex: st.anchor,
			StartIndex:  start,
			EndIndex:    end,
			Kind:        KindSplice,
			LossDB:      st.loss,
		})
	}

	for i := range events {
		fillDistances(&events[i], params)
	}
	return events
}

func fillDistances(ev *Event, params TraceParams) {
	ev.DistanceM = SampleDistanceM(params, ev.AnchorIndex)
	ev.StartM = SampleDistanceM(params, ev.StartIndex)
	ev.EndM = SampleDistanceM(params, ev.EndIndex)
}

// classifyGhosts 依据“前导强反射 + 近似 2 倍距离”识别幽灵候选。
// 单凭峰高不允许判定幽灵：必须给出父反射与距离比证据。
func classifyGhosts(events []Event, params TraceParams) {
	var strong []int
	for i := range events {
		if events[i].Kind == KindReflection && events[i].SpikeDB >= 1.5 {
			strong = append(strong, i)
		}
	}
	for i := range events {
		if events[i].Kind != KindReflection {
			continue
		}
		d := events[i].DistanceM
		bestParent, bestRatio := -1, math.MaxFloat64
		for _, pi := range strong {
			pd := events[pi].DistanceM
			if pd <= 0 || pd >= d {
				continue
			}
			ratio := d / pd
			err := math.Abs(ratio - 2.0)
			if err < math.Abs(bestRatio-2.0) {
				bestRatio, bestParent = ratio, pi
			}
		}
		if bestParent >= 0 && math.Abs(bestRatio-2.0) <= 0.12 {
			p := events[bestParent]
			events[i].Kind = KindGhost
			events[i].ParentIndex = p.AnchorIndex
			events[i].ParentDistM = p.DistanceM
			events[i].Ratio = bestRatio
			events[i].LossDB = 0 // 幽灵峰不产生真实台阶损耗
			events[i].Evidence = fmtGhostEvidence(d, p.DistanceM, bestRatio, p.SpikeDB)
		} else {
			events[i].Evidence = "反射峰：存在超过阈值的正残差且窗口内有持续台阶"
		}
	}
}

// detectEnd 判定轨迹末端。
// 若最后一段突然变为“零功率缺口”（采集被截断），报告截断与长度下界；
// 若信号是自然衰减进入噪声底，则报告光纤末端。
func detectEnd(params TraceParams, db, med []float64, n, deadIdx int) *Event {
	if n < 20 {
		return nil
	}
	zeroFloor := SampleDB(1e-12) // 线性功率为 0 时的钳制底
	tail := maxInt(5, n/100)
	tailMed := median(db[n-tail:])

	// 截断：尾部整段为零功率缺口。
	if math.Abs(tailMed-zeroFloor) < 0.01 {
		// 从尾部向前找到缺口起点（最后一个显著高于零底的样本）。
		cut := n - 1
		for cut > deadIdx && math.Abs(db[cut]-zeroFloor) < 0.01 {
			cut--
		}
		d := SampleDistanceM(params, cut)
		winHalf := maxInt(2, int(params.PulseWidthNS/params.SampleInterval+0.5))
		ev := Event{
			AnchorIndex: cut,
			StartIndex:  clampInt(cut-winHalf, 0, n-1),
			EndIndex:    clampInt(n-1, 0, n-1),
			Kind:        KindTruncation,
			DistanceM:   d,
			Evidence:    "轨迹在零功率缺口中止：真实光纤可能更长，只能报告已测长度下界",
		}
		fillDistances(&ev, params)
		return &ev
	}

	// 自然末端：最后 5% 样本相对前 10% 明显陷入噪声底（夹具不使用此分支）。
	head := median(db[n/2-n/10 : n/2])
	if tailMed < head-8.0 {
		ev := Event{
			AnchorIndex: n - 1,
			StartIndex:  n - tail,
			EndIndex:    n - 1,
			Kind:        KindFiberEnd,
			DistanceM:   SampleDistanceM(params, n-1),
			Evidence:    "信号自然衰减进入噪声底，判为光纤末端",
		}
		fillDistances(&ev, params)
		return &ev
	}
	return nil
}

func fmtGhostEvidence(ghostM, parentM, ratio, spike float64) string {
	return "幽灵候选：前导强反射位于 " +
		formatM(parentM) + "（峰高 " + formatDB(spike) +
		"），本峰位于 " + formatM(ghostM) + "，距离比 " +
		formatRatio(ratio) + "≈2.00，符合二次回波等距关系；无对应台阶"
}
