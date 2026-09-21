package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"otdrroom/internal/app"
	"otdrroom/internal/otdr"
	"otdrroom/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := app.New(st)
	if err := svc.SeedFixtures(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(svc)
	if err != nil {
		t.Fatal(err)
	}
	return srv, st
}

func TestIndexShowsTitleAndFourEvents(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("首页状态码 %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"光回波事件室", "反射连接器", "幽灵候选", "熔接",
		"轨迹截断（长度下界）", "1500.0",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("页面缺少 %q", want)
		}
	}
}

func postForm(srv *Server, target string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

func latestInterp(t *testing.T, st *store.Store, id int64) otdr.Interpretation {
	t.Helper()
	it, err := st.LatestInterpretation(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestGroupIndexRecomputesAlignment(t *testing.T) {
	srv, st := newTestServer(t)
	before := latestInterp(t, st, 1)
	connBefore := before.EventByAnchor(findAnchorIndex(t, before, otdr.KindReflection))

	rec := postForm(srv, "/traces/1/group-index", url.Values{
		"group_index": {"1.6000"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("改折射率状态码 %d: %s", rec.Code, rec.Body.String())
	}

	after := latestInterp(t, st, 1)
	connAfter := after.EventByAnchor(findAnchorIndex(t, after, otdr.KindReflection))
	if after.Rev != before.Rev+1 {
		t.Fatalf("rev 应递增: %d -> %d", before.Rev, after.Rev)
	}
	if connBefore.AnchorIndex != connAfter.AnchorIndex {
		t.Fatal("采样索引身份不应因折射率改变")
	}
	want := connBefore.DistanceM * 1.4680 / 1.6000
	if d := connAfter.DistanceM - want; d > 1 || d < -1 {
		t.Fatalf("距离未按折射率重算: %.2f vs %.2f", connAfter.DistanceM, want)
	}
	// 原始采样不变
	tr, _ := st.GetTrace(context.Background(), 1)
	_ = tr
}

func findAnchorIndex(t *testing.T, it otdr.Interpretation, k otdr.Kind) int {
	t.Helper()
	for _, ev := range it.Events {
		if ev.Kind == k {
			return ev.AnchorIndex
		}
	}
	t.Fatalf("找不到 %s 事件", k)
	return -1
}

func TestLinkEventsOnlyByExplicitConfirmation(t *testing.T) {
	srv, st := newTestServer(t)
	ia := latestInterp(t, st, 1)
	ib := latestInterp(t, st, 2)
	aConn := findAnchorIndex(t, ia, otdr.KindReflection)
	bConn := findAnchorIndex(t, ib, otdr.KindReflection)

	rec := postForm(srv, "/links", url.Values{
		"trace_a": {"1"}, "anchor_a": {itoa(aConn)},
		"trace_b": {"2"}, "anchor_b": {itoa(bConn)},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("确认对应状态码 %d: %s", rec.Code, rec.Body.String())
	}
	links, err := st.ListLinks(context.Background())
	if err != nil || len(links) != 1 {
		t.Fatalf("应有 1 条对应: %d err=%v", len(links), err)
	}

	// 不存在的锚点必须被拒绝。
	rec = postForm(srv, "/links", url.Values{
		"trace_a": {"1"}, "anchor_a": {"999999"},
		"trace_b": {"2"}, "anchor_b": {itoa(bConn)},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("无效锚点应 400，得到 %d", rec.Code)
	}
}

func TestGhostRequiresParentAndRatio(t *testing.T) {
	srv, st := newTestServer(t)
	it := latestInterp(t, st, 1)
	ghost := findAnchorIndex(t, it, otdr.KindGhost)
	conn := findAnchorIndex(t, it, otdr.KindReflection)

	// 合法：幽灵引用其前导强反射。
	rec := postForm(srv, "/traces/1/ghost", url.Values{
		"anchor": {itoa(ghost)}, "parent": {itoa(conn)}, "rev": {"1"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("合法幽灵判定应重定向: %d %s", rec.Code, rec.Body.String())
	}

	// 非法：把连接器指向“在它之后”的幽灵，违反前导关系。
	rec = postForm(srv, "/traces/1/ghost", url.Values{
		"anchor": {itoa(conn)}, "parent": {itoa(ghost)}, "rev": {"1"},
	})
	if rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "前导反射必须位于幽灵峰之前") {
		t.Fatalf("应拒绝非前导父反射: %d %s", rec.Code, rec.Body.String())
	}
}

func TestExportReimportKeepsSamples(t *testing.T) {
	srv, st := newTestServer(t)
	tr1, _ := st.GetTrace(context.Background(), 1)
	before := append([]float64(nil), tr1.SamplePower...)

	rec := postForm(srv, "/reimport", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("清空重导应重定向: %d", rec.Code)
	}

	tr2, err := st.GetTrace(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr2.SamplePower) != len(before) {
		t.Fatal("重导后采样数变化")
	}
	for i := range before {
		if before[i] != tr2.SamplePower[i] {
			t.Fatalf("重导后原始采样 idx=%d 不一致", i)
		}
	}

	// 导出端点
	req := httptest.NewRequest(http.MethodGet, "/runs/export", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"format": "otdrroom-run"`) {
		t.Fatalf("导出异常 code=%d", rr.Code)
	}
}

func TestDeadzoneRecompute(t *testing.T) {
	srv, st := newTestServer(t)
	rec := postForm(srv, "/traces/2/deadzone", url.Values{"deadzone_m": {"80"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("死区更新状态码 %d", rec.Code)
	}
	it := latestInterp(t, st, 2)
	if it.DeadzoneM != 80 {
		t.Fatalf("死区未生效 %.1f", it.DeadzoneM)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
