package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"otdrroom/internal/otdr"
	"otdrroom/internal/store"
)

func setup(t *testing.T) (*Service, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := New(st)
	if err := svc.SeedFixtures(ctx); err != nil {
		t.Fatal(err)
	}
	return svc, ctx
}

func findKind(t *testing.T, it otdr.Interpretation, k otdr.Kind) otdr.Event {
	t.Helper()
	for _, ev := range it.Events {
		if ev.Kind == k {
			return ev
		}
	}
	t.Fatalf("缺少事件 %s", k)
	return otdr.Event{}
}

func TestRecomputeAndMarkGhostEvidence(t *testing.T) {
	svc, ctx := setup(t)
	it, err := svc.Store().LatestInterpretation(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	ghost := findKind(t, it, otdr.KindGhost)
	conn := findKind(t, it, otdr.KindReflection)

	// 没有前导关系的判定必须失败（不能只凭峰高）。
	if err := svc.MarkGhost(ctx, 1, conn.AnchorIndex, ghost.AnchorIndex, 1); err == nil {
		t.Fatal("把连接器指向其后的幽灵，应被拒绝")
	}
	// 合法判定。
	if err := svc.MarkGhost(ctx, 1, ghost.AnchorIndex, conn.AnchorIndex, 1); err != nil {
		t.Fatalf("合法幽灵判定失败: %v", err)
	}
	got, _ := svc.Store().LatestInterpretation(ctx, 1)
	g := got.EventByAnchor(ghost.AnchorIndex)
	if g.Kind != otdr.KindGhost || g.ParentIndex != conn.AnchorIndex {
		t.Fatalf("幽灵判定未生效: %+v", g)
	}
	if !strings.Contains(g.Evidence, "二次回波") {
		t.Fatalf("幽灵证据必须引用距离关系: %s", g.Evidence)
	}

	// 否决后恢复为普通反射。
	if err := svc.RejectGhost(ctx, 1, ghost.AnchorIndex, 1); err != nil {
		t.Fatal(err)
	}
	got2, _ := svc.Store().LatestInterpretation(ctx, 1)
	if got2.EventByAnchor(ghost.AnchorIndex).Kind != otdr.KindReflection {
		t.Fatal("否决幽灵后应恢复为普通反射")
	}
}

func TestGroupIndexChangeKeepsAnchorMovesDistance(t *testing.T) {
	svc, ctx := setup(t)
	before, _ := svc.Store().LatestInterpretation(ctx, 2)
	c := findKind(t, before, otdr.KindReflection)
	if err := svc.UpdateGroupIndex(ctx, 2, 1.5000); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.Store().LatestInterpretation(ctx, 2)
	c2 := after.EventByAnchor(c.AnchorIndex)
	if c2 == nil {
		t.Fatal("改折射率后锚点身份丢失")
	}
	if c2.DistanceM >= c.DistanceM {
		t.Fatalf("折射率变大距离应变小: %.2f -> %.2f", c.DistanceM, c2.DistanceM)
	}
}

func TestLinkAndReimport(t *testing.T) {
	svc, ctx := setup(t)
	ia, _ := svc.Store().LatestInterpretation(ctx, 1)
	ib, _ := svc.Store().LatestInterpretation(ctx, 2)
	ca := findKind(t, ia, otdr.KindReflection).AnchorIndex
	cb := findKind(t, ib, otdr.KindReflection).AnchorIndex
	if err := svc.LinkEvents(ctx, 1, ca, 2, cb); err != nil {
		t.Fatal(err)
	}
	pair, err := svc.LoadPair(ctx, 1, 2)
	if err != nil || len(pair.Links) != 1 {
		t.Fatalf("对齐视图应含 1 条对应: %v", err)
	}

	// 重新播种：采样身份不变，库重置为初始 2 条轨迹。
	if err := svc.ReimportFixtures(ctx); err != nil {
		t.Fatal(err)
	}
	n, err := svc.Store().TraceCount(ctx)
	if err != nil || n != 2 {
		t.Fatalf("重导后轨迹数异常: %d %v", n, err)
	}
	links, _ := svc.Store().ListLinks(ctx)
	if len(links) != 0 {
		t.Fatal("重导后人工对应应被清空")
	}
	it, _ := svc.Store().LatestInterpretation(ctx, 1)
	if it.Rev != 1 {
		t.Fatalf("重导后应回到 rev=1，得到 %d", it.Rev)
	}
}

func TestDeadzoneChange(t *testing.T) {
	svc, ctx := setup(t)
	if err := svc.UpdateDeadzone(ctx, 1, 90); err != nil {
		t.Fatal(err)
	}
	it, _ := svc.Store().LatestInterpretation(ctx, 1)
	if it.DeadzoneM != 90 {
		t.Fatalf("死区未更新 %.1f", it.DeadzoneM)
	}
}
