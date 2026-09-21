package otdr

import (
	"math"
	"sort"
)

// Analyze 在不改动原始采样的前提下，对一条轨迹做一次完整解释。
// rev 是当前参数修订号；折射率变化后应重新调用本函数。
func Analyze(t Trace, rev int) Interpretation {
	params := t.Params
	n := len(t.SamplePower)
	it := Interpretation{
		TraceID:     t.ID,
		TraceCode:   t.Code,
		Rev:         rev,
		GroupIndex:  params.GroupIndex,
		DeadzoneM:   params.LaunchDeadzone,
		SampleCount: n,
		LowerBoundM: 0,
	}
	if n == 0 {
		return it
	}

	db := make([]float64, n)
	for i, p := range t.SamplePower {
		db[i] = SampleDB(p)
	}
	it.DB = db
	it.LastM = SampleDistanceM(params, n-1)

	// 窗口尺度跟随脉宽（换算成采样数），宽脉宽的事件窗更宽。
	pulseSamples := int((params.PulseWidthNS / params.SampleInterval) + 0.5)
	if pulseSamples < 2 {
		pulseSamples = 2
	}
	// 双轨检测：
	//   wideMed —— 约 6 脉宽的中值，只用于把反射峰从电平中移除并找峰；
	//   stepMed —— 约 2 脉宽的中值并屏蔽峰簇，用于定位持久台阶（熔接）。
	// 宽中值会把台阶“抹平”一小段，所以台阶检测不能直接用它。
	wideHalf := pulseSamples * 3
	pulseHalf := pulseSamples

	deadIdx := firstIndexBeyondM(params, params.LaunchDeadzone, n)

	// 1) 反射峰：滚动中值给出“无峰局部电平”，正残差超过阈值即为峰。
	wideMed := movingMedian(db, wideHalf)
	resid := make([]float64, n)
	for i := range db {
		resid[i] = db[i] - wideMed[i]
	}
	reflections := detectReflectionClusters(db, wideMed, resid, deadIdx, pulseHalf,
		reflectionSpikeThreshold(params))

	// 构造台阶检测用电平：窄中值，但在所有反射簇位置替换为宽中值，
	// 避免峰形制造假台阶。
	stepMed := movingMedian(db, pulseHalf)
	blocked := map[int]bool{}
	for _, rf := range reflections {
		// 屏蔽反射簇及其两侧：中值窗口会把尖峰“拖尾”，不屏蔽干净会
		// 在峰后伪造台阶，或污染下一个设施前的清洁段（幽灵与其父反射
		// 相距仅一个连接器死区，宽脉宽下尤其明显）。事件窗本身不扩大。
		lo := clampInt(rf.start-wideHalf, 0, n-1)
		hi := clampInt(rf.end+wideHalf, 0, n-1)
		for i := lo; i <= hi; i++ {
			stepMed[i] = wideMed[i]
			blocked[i] = true
		}
	}
	// 2) 台阶（熔接/连接器插入损耗）：在中值电平上找持续的向下跳变。
	steps := detectSteps(db, stepMed, blocked, deadIdx, pulseHalf, stepThreshold(params))

	// 3) 把峰与台阶合并成事件候选（同一物理事件可能同时有峰与台阶）。
	it.Events = buildEvents(params, db, wideMed, reflections, steps, pulseHalf)

	// 4) 末端判定：截断（零功率缺口）或自然末端。
	if ev := detectEnd(params, db, wideMed, n, deadIdx); ev != nil {
		it.Events = append(it.Events, *ev)
	}

	// 5) 幽灵判定：必须引用前导强反射且满足近似 2 倍距离关系，不能只凭峰高。
	classifyGhosts(it.Events, params)

	sort.SliceStable(it.Events, func(i, j int) bool {
		return it.Events[i].AnchorIndex < it.Events[j].AnchorIndex
	})

	// 6) 分段基线：在相邻事件之间做 OLS，事件窗内线性桥接。
	it.Baseline, it.Anchors, _ = segmentedBaseline(db, it.Events, params)

	// 7) 用事件窗外两侧高原中值复核台阶损耗与峰高。plateau 取 2 个脉宽，
	// level 是已屏蔽反射拖尾的窄中值电平。
	plateau := pulseSamples * 2
	for i := range it.Events {
		refineEventLoss(&it.Events[i], stepMed, plateau)
	}

	// 8) 累计损耗：重叠事件窗取并集，绝不重复计损。
	it.CumLoss, it.TotalLossDB = cumulativeLoss(it.Events, stepMed, plateau, n)

	for i := range it.Events {
		if it.Events[i].Kind == KindTruncation {
			it.LowerBoundM = it.Events[i].DistanceM
		}
	}
	return it
}

func firstIndexBeyondM(params TraceParams, meters float64, n int) int {
	for i := 0; i < n; i++ {
		if SampleDistanceM(params, i) >= meters {
			return i
		}
	}
	return n
}

// reflectionSpikeThreshold 峰残差阈值随噪声（平均次数/脉宽）自适应，
// 但下限足够高，避免把普通波动当反射。
func reflectionSpikeThreshold(params TraceParams) float64 {
	return math.Max(0.45, 3.0/math.Sqrt(float64(params.AvgCount)/1024.0))
}

// stepThreshold 台阶（损耗）判定阈值。
func stepThreshold(params TraceParams) float64 {
	return math.Max(0.12, 1.2/math.Sqrt(float64(params.AvgCount)/1024.0))
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
