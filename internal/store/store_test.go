package store

import (
	"context"
	"path/filepath"
	"testing"

	"otdrroom/internal/otdr"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(context.Background(), filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestInsertAndReviseParams(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	spec := otdr.FixtureSpecs()[0]
	tr := otdr.GenerateTrace(1, spec)
	if err := st.InsertTrace(ctx, tr); err != nil {
		t.Fatal(err)
	}

	n := 1.55
	rev, err := st.UpdateParams(ctx, 1, ParamUpdate{GroupIndex: &n})
	if err != nil || rev != 2 {
		t.Fatalf("更新折射率失败: rev=%d err=%v", rev, err)
	}
	dz := 55.0
	rev, err = st.UpdateParams(ctx, 1, ParamUpdate{DeadzoneM: &dz})
	if err != nil || rev != 3 {
		t.Fatalf("更新死区失败: rev=%d err=%v", rev, err)
	}

	back, err := st.GetTrace(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if back.Params.GroupIndex != 1.55 || back.Params.LaunchDeadzone != 55 {
		t.Fatalf("参数未持久化: %+v", back.Params)
	}
	if len(back.SamplePower) != len(tr.SamplePower) {
		t.Fatal("采样数量改变")
	}
	for i := range tr.SamplePower {
		if tr.SamplePower[i] != back.SamplePower[i] {
			t.Fatalf("原始采样在参数修订后被改动 idx=%d", i)
		}
	}
}

func TestVerdictsAndLinksAnchorIdentity(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	for i, spec := range otdr.FixtureSpecs() {
		if err := st.InsertTrace(ctx, otdr.GenerateTrace(int64(i+1), spec)); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertVerdict(ctx, Verdict{
		TraceID: 1, AnchorIdx: 586, Verdict: "ghost_confirm", ParentIdx: 293, Rev: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertLink(ctx, EventLink{
		TraceA: 1, AnchorA: 293, TraceB: 2, AnchorB: 244, RevA: 1, RevB: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// 幂等：重复确认同一对锚点不应产生第二条。
	if err := st.InsertLink(ctx, EventLink{
		TraceA: 1, AnchorA: 293, TraceB: 2, AnchorB: 244, RevA: 2, RevB: 1,
	}); err != nil {
		t.Fatal(err)
	}
	links, err := st.ListLinks(ctx)
	if err != nil || len(links) != 1 {
		t.Fatalf("对应关系应幂等唯一: %d err=%v", len(links), err)
	}
	vs, err := st.ListVerdicts(ctx, 1)
	if err != nil || vs[586].ParentIdx != 293 {
		t.Fatalf("判定读取错误: %+v err=%v", vs, err)
	}
}

func TestResetAndExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	for i, spec := range otdr.FixtureSpecs() {
		tr := otdr.GenerateTrace(int64(i+1), spec)
		if err := st.InsertTrace(ctx, tr); err != nil {
			t.Fatal(err)
		}
		it := otdr.Analyze(tr, tr.CreatedRev)
		if err := st.SaveInterpretation(ctx, it); err != nil {
			t.Fatal(err)
		}
	}

	rec1, err := st.ExportRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := rec1.Traces[0].SamplePower

	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.TraceCount(ctx); n != 0 {
		t.Fatal("Reset 后应无轨迹")
	}

	if err := st.ImportRun(ctx, rec1); err != nil {
		t.Fatal(err)
	}
	rec2, err := st.ExportRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec2.Traces) != len(rec1.Traces) {
		t.Fatal("导入后轨迹数不一致")
	}
	after := rec2.Traces[0].SamplePower
	if len(after) != len(before) {
		t.Fatal("导入后采样数不一致")
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("导出-清空-导入后原始采样身份被破坏 idx=%d", i)
		}
	}
}
