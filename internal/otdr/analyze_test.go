package otdr

import (
	"math"
	"testing"
)

// loadFixtures 生成两条夹具轨迹。
func loadFixtures(t *testing.T) []Trace {
	t.Helper()
	specs := FixtureSpecs()
	out := make([]Trace, len(specs))
	for i, sp := range specs {
		out[i] = GenerateTrace(int64(i+1), sp)
	}
	return out
}

func eventByKind(it Interpretation, k Kind) *Event {
	for i := range it.Events {
		if it.Events[i].Kind == k {
			return &it.Events[i]
		}
	}
	return nil
}

// TestFixtureEvents 校验夹具上四类事件都被检出且位置正确。
func TestFixtureEvents(t *testing.T) {
	for _, tr := range loadFixtures(t) {
		it := Analyze(tr, 1)

		conn := eventByKind(it, KindReflection)
		if conn == nil {
			t.Fatalf("%s: 未检出反射连接器", tr.Code)
		}
		if d := math.Abs(conn.DistanceM - FixtureConnectorM); d > 15 {
			t.Fatalf("%s: 连接器距离 %.1f 偏离 %.0fm", tr.Code, conn.DistanceM, FixtureConnectorM)
		}
		if conn.LossDB < 0.35 || conn.LossDB > 0.8 {
			t.Fatalf("%s: 连接器损耗 %.3f 不在合理区间", tr.Code, conn.LossDB)
		}
		if conn.SpikeDB < 3 {
			t.Fatalf("%s: 连接器峰高 %.2f 过低", tr.Code, conn.SpikeDB)
		}

		ghost := eventByKind(it, KindGhost)
		if ghost == nil {
			t.Fatalf("%s: 未检出幽灵候选", tr.Code)
		}
		if math.Abs(ghost.DistanceM-FixtureGhostM) > 15 {
			t.Fatalf("%s: 幽灵距离 %.1f 偏离 %.0fm", tr.Code, ghost.DistanceM, FixtureGhostM)
		}
		if ghost.LossDB != 0 {
			t.Fatalf("%s: 幽灵不应贡献台阶损耗，得到 %.3f", tr.Code, ghost.LossDB)
		}
		if ghost.ParentIndex != conn.AnchorIndex {
			t.Fatalf("%s: 幽灵父锚点应为连接器 %d，得到 %d",
				tr.Code, conn.AnchorIndex, ghost.ParentIndex)
		}
		if math.Abs(ghost.Ratio-2.0) > 0.05 {
			t.Fatalf("%s: 幽灵距离比 %.3f 不近似 2", tr.Code, ghost.Ratio)
		}

		splice := eventByKind(it, KindSplice)
		if splice == nil {
			t.Fatalf("%s: 未检出熔接", tr.Code)
		}
		if math.Abs(splice.DistanceM-FixtureSpliceM) > 20 {
			t.Fatalf("%s: 熔接距离 %.1f 偏离 %.0fm", tr.Code, splice.DistanceM, FixtureSpliceM)
		}
		if splice.LossDB < 0.3 || splice.LossDB > 0.7 {
			t.Fatalf("%s: 熔接损耗 %.3f 不合理", tr.Code, splice.LossDB)
		}

		trunc := eventByKind(it, KindTruncation)
		if trunc == nil {
			t.Fatalf("%s: 未检出截断", tr.Code)
		}
		if math.Abs(it.LowerBoundM-FixtureTruncateM) > 5 {
			t.Fatalf("%s: 长度下界 %.1f 偏离 %.0fm", tr.Code, it.LowerBoundM, FixtureTruncateM)
		}
		if got := eventByKind(it, KindFiberEnd); got != nil {
			t.Fatalf("%s: 截断轨迹不得伪造光纤末端", tr.Code)
		}
	}
}

// TestRawSamplesImmutableUnderRefractiveIndex 修改折射率后原始采样与
// 锚点采样索引保持不变，只有米制距离改变。
func TestRawSamplesImmutableUnderRefractiveIndex(t *testing.T) {
	tr := loadFixtures(t)[0]
	before := append([]float64(nil), tr.SamplePower...)

	it1 := Analyze(tr, 1)
	conn1 := eventByKind(it1, KindReflection)
	d1 := conn1.DistanceM

	tr.Params.GroupIndex = 1.6000
	it2 := Analyze(tr, 2)
	conn2 := eventByKind(it2, KindReflection)

	for i := range before {
		if before[i] != tr.SamplePower[i] {
			t.Fatalf("原始采样在改折射率后被修改，索引 %d", i)
		}
	}
	if conn1.AnchorIndex != conn2.AnchorIndex {
		t.Fatalf("锚点采样索引身份改变: %d -> %d", conn1.AnchorIndex, conn2.AnchorIndex)
	}
	want := d1 * (1.4680 / 1.6000)
	if math.Abs(conn2.DistanceM-want) > 0.5 {
		t.Fatalf("改折射率后距离应按 n 反比缩放: %.2f vs %.2f", conn2.DistanceM, want)
	}
	if it2.Rev != 2 {
		t.Fatalf("解释 rev 应为 2，得到 %d", it2.Rev)
	}
}

// TestOverlappingEventsNotDoubleCounted 两个事件窗重叠时，累计损耗必须
// 按并集只计一次净下降，不得重复计损。
func TestOverlappingEventsNotDoubleCounted(t *testing.T) {
	// 合成平坦电平：两个相距仅 5 采样、各 0.3dB 的相邻台阶，使事件窗重叠。
	params := TraceParams{
		PulseWidthNS: 100, GroupIndex: 1.468, AvgCount: 1 << 30,
		SampleInterval: 5.0, LaunchDeadzone: 0,
	}
	const n = 1200
	level := make([]float64, n)
	for i := 0; i < n; i++ {
		v := 0.0
		if i >= 400 {
			v -= 0.3
		}
		if i >= 405 {
			v -= 0.3
		}
		level[i] = -5.0 + v
	}
	pulse := int(params.PulseWidthNS/params.SampleInterval + 0.5)
	e1 := Event{
		AnchorIndex: 400, StartIndex: 400 - pulse/2, EndIndex: 400 + pulse/2,
		Kind: KindSplice, LossDB: 0.3,
	}
	e2 := Event{
		AnchorIndex: 405, StartIndex: 405 - pulse/2, EndIndex: 405 + pulse/2,
		Kind: KindSplice, LossDB: 0.3,
	}
	fillDistances(&e1, params)
	fillDistances(&e2, params)
	events := []Event{e1, e2}

	if wins := unionWindows(events, n); len(wins) != 1 {
		t.Fatalf("重叠窗应合并为 1 个并集窗，得到 %d", len(wins))
	}
	_, total := cumulativeLoss(events, level, pulse*2, n)
	// 并集窗只统计一次净下降（≈0.6），绝不能把两个 0.3 再各自独立加一遍（1.2）。
	if total > 0.7 {
		t.Fatalf("重叠事件被重复计损: total=%.3f", total)
	}
	if total < 0.5 {
		t.Fatalf("并集窗净下降被错误抹掉: total=%.3f", total)
	}

	// 对照：在独立的平坦电平上放两个远离的 0.3dB 台阶，应合计 0.6dB。
	sepLevel := make([]float64, n)
	for i := 0; i < n; i++ {
		v := 0.0
		if i >= 300 {
			v -= 0.3
		}
		if i >= 900 {
			v -= 0.3
		}
		sepLevel[i] = -5.0 + v
	}
	f1 := Event{AnchorIndex: 300, StartIndex: 300 - pulse/2, EndIndex: 300 + pulse/2, Kind: KindSplice}
	f2 := Event{AnchorIndex: 900, StartIndex: 900 - pulse/2, EndIndex: 900 + pulse/2, Kind: KindSplice}
	fillDistances(&f1, params)
	fillDistances(&f2, params)
	_, totalSep := cumulativeLoss([]Event{f1, f2}, sepLevel, pulse*2, n)
	if math.Abs(totalSep-0.6) > 0.05 {
		t.Fatalf("分离事件应合计 0.6dB，得到 %.3f", totalSep)
	}
}

// TestTruncationIsOnlyLowerBound 截断轨迹只报告长度下界，绝不产生末端损耗。
func TestTruncationIsOnlyLowerBound(t *testing.T) {
	tr := loadFixtures(t)[0]
	it := Analyze(tr, 1)
	trunc := eventByKind(it, KindTruncation)
	if trunc == nil || trunc.LossDB != 0 {
		t.Fatalf("截断事件不存在或带损耗: %+v", trunc)
	}
	if it.LowerBoundM <= 0 {
		t.Fatalf("截断时必须给出长度下界")
	}
	// 累积损耗在截断之后不得继续增长。
	tail := it.CumLoss[len(it.CumLoss)-1]
	if math.Abs(tail-it.TotalLossDB) > 1e-9 {
		t.Fatalf("截断后累积损耗不应增长: tail=%.3f total=%.3f", tail, it.TotalLossDB)
	}
}

// TestDistanceCalibrationRoundTrip 距离/索引换算在不同折射率下自洽。
func TestDistanceCalibrationRoundTrip(t *testing.T) {
	for _, tr := range loadFixtures(t) {
		p := tr.Params
		for idx := 0; idx < len(tr.SamplePower); idx += 137 {
			d := SampleDistanceM(p, idx)
			back := DistanceToSampleIndex(p, d)
			if back != idx {
				t.Fatalf("%s idx=%d roundtrip=%d", tr.Code, idx, back)
			}
		}
	}
}

// TestBaselineAnchors 每次解释都必须保留分段基线的拐点（锚点），
// 且拐点采样索引随折射率修订保持稳定。
func TestBaselineAnchors(t *testing.T) {
	tr := loadFixtures(t)[0]
	it1 := Analyze(tr, 1)
	if len(it1.Anchors) < 2 {
		t.Fatalf("基线拐点数量过少: %d", len(it1.Anchors))
	}
	for _, a := range it1.Anchors {
		if a.Index <= 0 || a.DistanceM <= 0 {
			t.Fatalf("非法基线拐点: %+v", a)
		}
	}
	tr.Params.GroupIndex = 1.5555
	it2 := Analyze(tr, 2)
	if len(it2.Anchors) != len(it1.Anchors) {
		t.Fatalf("改折射率后拐点数量改变: %d -> %d",
			len(it1.Anchors), len(it2.Anchors))
	}
	for i := range it1.Anchors {
		if it1.Anchors[i].Index != it2.Anchors[i].Index {
			t.Fatalf("拐点采样索引身份改变: %d -> %d",
				it1.Anchors[i].Index, it2.Anchors[i].Index)
		}
		if it1.Anchors[i].DistanceM == it2.Anchors[i].DistanceM {
			t.Fatalf("改折射率后拐点距离未重算")
		}
	}
}

// TestGhostRequiresLeadingReflectionAndRatio 幽灵判定规则单测：
// 只有峰高、没有前导强反射或 2 倍距离关系时不得判幽灵。
func TestGhostRequiresLeadingReflectionAndRatio(t *testing.T) {
	tr := loadFixtures(t)[0]
	it := Analyze(tr, 1)
	ghost := eventByKind(it, KindGhost)
	if ghost == nil {
		t.Fatal("缺少幽灵候选")
	}
	if ghost.ParentIndex < 0 || ghost.Ratio <= 0 {
		t.Fatal("幽灵证据必须引用前导反射与距离比")
	}
}
