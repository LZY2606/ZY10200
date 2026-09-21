package web

import (
	"net/http"
	"strconv"

	"otdrroom/internal/app"
	"otdrroom/internal/otdr"
	"otdrroom/internal/store"
)

// EventRow 是页面上单事件的展示行。
type EventRow struct {
	Event    otdr.Event
	Label    string
	Verdict  string
	ParentID string
}

// TraceView 聚合一条轨迹的页面信息。
type TraceView struct {
	Row      store.TraceRow
	Interp   otdr.Interpretation
	SVG      string
	Events   []EventRow
	Verdicts map[int]store.Verdict
}

// PageData 是首页模板数据。
type PageData struct {
	Traces  []TraceView
	Pair    app.AlignedPair
	Links   []store.EventLink
	HasPair bool
	Notice  string
}

func eventLabelZh(k string) string {
	return otdr.EventLabel(otdr.Kind(k))
}

func (s *Server) loadPageData(r *http.Request) (PageData, error) {
	svc := s.svc
	rows, err := svc.Store().ListTraces(r.Context())
	if err != nil {
		return PageData{}, err
	}
	pd := PageData{}
	for _, row := range rows {
		it, err := svc.Store().LatestInterpretation(r.Context(), row.ID)
		if err != nil {
			return pd, err
		}
		verdicts, err := svc.Store().ListVerdicts(r.Context(), row.ID)
		if err != nil {
			return pd, err
		}
		tv := TraceView{
			Row: row, Interp: it, SVG: RenderTraceSVG(it), Verdicts: verdicts,
		}
		for _, ev := range it.Events {
			v := ""
			parent := ""
			if vd, ok := verdicts[ev.AnchorIndex]; ok {
				v = vd.Verdict
				if vd.ParentIdx >= 0 {
					parent = strconv.Itoa(vd.ParentIdx)
				}
			}
			tv.Events = append(tv.Events, EventRow{
				Event: ev, Label: otdr.EventLabel(ev.Kind),
				Verdict: v, ParentID: parent,
			})
		}
		pd.Traces = append(pd.Traces, tv)
	}
	if len(rows) >= 2 {
		pair, err := svc.LoadPair(r.Context(), rows[0].ID, rows[1].ID)
		if err != nil {
			return pd, err
		}
		pd.Pair = pair
		pd.HasPair = true
		pd.Links = pair.Links
	}
	return pd, nil
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	pd, err := s.loadPageData(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pd.Notice = r.URL.Query().Get("notice")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, "index.html", pd); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func parseID(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }

func (s *Server) handleTraceAction(w http.ResponseWriter, r *http.Request) {
	// /traces/{id}/... 动作
	id, err := parseID(firstSeg(r.URL.Path, "traces"))
	if err != nil {
		http.Error(w, "无效的轨迹 ID", http.StatusBadRequest)
		return
	}
	action := lastSeg(r.URL.Path)
	if r.Method != http.MethodPost {
		http.Error(w, "仅支持 POST", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch action {
	case "group-index":
		n, err := strconv.ParseFloat(r.FormValue("group_index"), 64)
		if err != nil {
			http.Error(w, "折射率必须是数字", http.StatusBadRequest)
			return
		}
		if err := s.svc.UpdateGroupIndex(r.Context(), id, n); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.redirect(w, r, "已按新折射率重新校准距离并重算对齐")
	case "deadzone":
		m, err := strconv.ParseFloat(r.FormValue("deadzone_m"), 64)
		if err != nil {
			http.Error(w, "死区必须是数字（米）", http.StatusBadRequest)
			return
		}
		if err := s.svc.UpdateDeadzone(r.Context(), id, m); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.redirect(w, r, "发射端死区已更新并重新解释")
	case "ghost":
		anchor, err := strconv.Atoi(r.FormValue("anchor"))
		if err != nil {
			http.Error(w, "锚点采样索引无效", http.StatusBadRequest)
			return
		}
		parent, err := strconv.Atoi(r.FormValue("parent"))
		if err != nil {
			http.Error(w, "前导反射锚点无效", http.StatusBadRequest)
			return
		}
		rev, _ := strconv.Atoi(r.FormValue("rev"))
		if err := s.svc.MarkGhost(r.Context(), id, anchor, parent, rev); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.redirect(w, r, "已依据前导强反射与 2 倍距离关系判为幽灵候选")
	case "reject-ghost":
		anchor, err := strconv.Atoi(r.FormValue("anchor"))
		if err != nil {
			http.Error(w, "锚点采样索引无效", http.StatusBadRequest)
			return
		}
		rev, _ := strconv.Atoi(r.FormValue("rev"))
		if err := s.svc.RejectGhost(r.Context(), id, anchor, rev); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.redirect(w, r, "已否决幽灵判定")
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "仅支持 POST", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	aT, _ := parseID(r.FormValue("trace_a"))
	bT, _ := parseID(r.FormValue("trace_b"))
	aA, err1 := strconv.Atoi(r.FormValue("anchor_a"))
	bA, err2 := strconv.Atoi(r.FormValue("anchor_b"))
	if err1 != nil || err2 != nil {
		http.Error(w, "事件锚点无效", http.StatusBadRequest)
		return
	}
	if err := s.svc.LinkEvents(r.Context(), aT, aA, bT, bA); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.redirect(w, r, "已确认两条轨迹的事件对应（不会自动合并其它相近峰）")
}

func (s *Server) handleLinkDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "仅支持 POST", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, _ := parseID(r.FormValue("id"))
	if err := s.svc.Unlink(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.redirect(w, r, "已取消事件对应")
}

func (s *Server) handleReimport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "仅支持 POST", http.StatusMethodNotAllowed)
		return
	}
	if err := s.svc.ReimportFixtures(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.redirect(w, r, "数据库已清空并重新导入固定夹具，可重新复核")
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	raw, err := s.svc.Store().ExportRunJSON(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="otdrroom-run.json"`)
	_, _ = w.Write(raw)
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request, notice string) {
	http.Redirect(w, r, "/?notice="+urlQuery(notice), http.StatusSeeOther)
}
