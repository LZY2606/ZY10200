// Package app 编排存储层与 OTDR 分析：重算解释、应用用户判定、
// 跨轨迹对应管理以及对齐视图构建。
package app

import (
	"context"
	"fmt"
	"math"
	"sort"

	"otdrroom/internal/otdr"
	"otdrroom/internal/store"
)

// Service 是应用服务。
type Service struct{ st *store.Store }

func New(st *store.Store) *Service { return &Service{st: st} }

// Store 暴露底层存储（供 HTTP 层做导出/重置）。
func (s *Service) Store() *store.Store { return s.st }

// RecomputeAll 用当前参数重新解释所有轨迹，并保存对应 rev 的快照。
// 折射率改变后必须重算：事件身份（锚点采样索引）保持，米制距离刷新。
func (s *Service) RecomputeAll(ctx context.Context) ([]otdr.Interpretation, error) {
	rows, err := s.st.ListTraces(ctx)
	if err != nil {
		return nil, err
	}
	var out []otdr.Interpretation
	for _, r := range rows {
		tr, err := s.st.GetTrace(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		it, err := s.RecomputeTrace(ctx, tr, r.Rev)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, nil
}

// RecomputeTrace 解释单条轨迹、应用已有判定，并持久化快照。
func (s *Service) RecomputeTrace(ctx context.Context, tr otdr.Trace, rev int) (otdr.Interpretation, error) {
	it := otdr.Analyze(tr, rev)
	verdicts, err := s.st.ListVerdicts(ctx, tr.ID)
	if err != nil {
		return it, err
	}
	applyVerdicts(&it, verdicts)
	sort.SliceStable(it.Events, func(i, j int) bool {
		return it.Events[i].AnchorIndex < it.Events[j].AnchorIndex
	})
	if err := s.st.SaveInterpretation(ctx, it); err != nil {
		return it, err
	}
	return it, nil
}

// applyVerdicts 把用户判定叠加到自动解释上。
// 判定锚定采样索引，折射率修订后仍然适用。
func applyVerdicts(it *otdr.Interpretation, verdicts map[int]store.Verdict) {
	for idx, v := range verdicts {
		ev := it.EventByAnchor(idx)
		switch v.Verdict {
		case "ghost_confirm":
			if ev != nil {
				ev.Kind = otdr.KindGhost
				if parent := it.EventByAnchor(v.ParentIdx); parent != nil {
					ev.ParentIndex = parent.AnchorIndex
					ev.ParentDistM = parent.DistanceM
					ev.Ratio = ev.DistanceM / parent.DistanceM
					ev.LossDB = 0
					ev.Evidence = "用户确认幽灵：引用前导强反射 " +
						fmt.Sprintf("%.1fm（峰高 %.2fdB），距离比 %.2f，二次回波等距关系",
							parent.DistanceM, parent.SpikeDB, ev.Ratio)
				}
			}
		case "ghost_reject":
			if ev != nil && ev.Kind == otdr.KindGhost {
				ev.Kind = otdr.KindReflection
				ev.ParentIndex = -1
				ev.Ratio = 0
				ev.Evidence = "用户否决幽灵判定，按普通反射事件保留"
			}
		case "confirmed_facility":
			if ev != nil {
				ev.Evidence = ev.Evidence + "（用户确认设施）"
			}
		}
	}
}

// MarkGhost 把一个事件判为幽灵。必须引用同轨迹的前导强反射锚点，
// 且距离关系近似 2 倍；仅凭峰高不允许判幽灵。
func (s *Service) MarkGhost(ctx context.Context, traceID int64, anchor, parent int, rev int) error {
	it, err := s.st.LatestInterpretation(ctx, traceID)
	if err != nil {
		return err
	}
	ev := it.EventByAnchor(anchor)
	p := it.EventByAnchor(parent)
	if ev == nil || p == nil {
		return fmt.Errorf("事件或前导反射不存在")
	}
	if p.SpikeDB < 1.0 {
		return fmt.Errorf("被引用的前导峰不够强，不能作为幽灵的父反射")
	}
	if p.DistanceM <= 0 || ev.DistanceM <= p.DistanceM {
		return fmt.Errorf("前导反射必须位于幽灵峰之前")
	}
	ratio := ev.DistanceM / p.DistanceM
	if math.Abs(ratio-2.0) > 0.20 {
		return fmt.Errorf("距离比 %.2f 不满足二次回波近似 2 倍关系，拒绝幽灵判定", ratio)
	}
	if err := s.st.UpsertVerdict(ctx, store.Verdict{
		TraceID: traceID, AnchorIdx: anchor,
		Verdict: "ghost_confirm", ParentIdx: parent, Rev: rev,
	}); err != nil {
		return err
	}
	if err := s.st.LogAction(ctx, "mark_ghost", map[string]any{
		"trace_id": traceID, "anchor": anchor, "parent": parent,
		"ratio": ratio, "rev": rev,
	}); err != nil {
		return err
	}
	_, err = s.recomputeOne(ctx, traceID)
	return err
}

// RejectGhost 否决幽灵判定，恢复为普通反射事件。
func (s *Service) RejectGhost(ctx context.Context, traceID int64, anchor, rev int) error {
	if err := s.st.UpsertVerdict(ctx, store.Verdict{
		TraceID: traceID, AnchorIdx: anchor, Verdict: "ghost_reject", Rev: rev,
	}); err != nil {
		return err
	}
	if err := s.st.LogAction(ctx, "reject_ghost",
		map[string]any{"trace_id": traceID, "anchor": anchor, "rev": rev}); err != nil {
		return err
	}
	_, err := s.recomputeOne(ctx, traceID)
	return err
}

// LinkEvents 显式确认两条轨迹的两个事件为同一物理设施。
// 不会因为峰“看起来近”自动建立任何对应。
func (s *Service) LinkEvents(ctx context.Context, aTrace int64, aAnchor int,
	bTrace int64, bAnchor int) error {
	if aTrace == bTrace {
		return fmt.Errorf("只能在不同轨迹之间确认对应")
	}
	ia, err := s.st.LatestInterpretation(ctx, aTrace)
	if err != nil {
		return err
	}
	ib, err := s.st.LatestInterpretation(ctx, bTrace)
	if err != nil {
		return err
	}
	if ia.EventByAnchor(aAnchor) == nil || ib.EventByAnchor(bAnchor) == nil {
		return fmt.Errorf("待对应的事件不存在，请先确认两个锚点都在当前解释中")
	}
	if err := s.st.InsertLink(ctx, store.EventLink{
		TraceA: aTrace, AnchorA: aAnchor, TraceB: bTrace, AnchorB: bAnchor,
		RevA: ia.Rev, RevB: ib.Rev,
	}); err != nil {
		return err
	}
	return s.st.LogAction(ctx, "link_events", map[string]any{
		"trace_a": aTrace, "anchor_a": aAnchor,
		"trace_b": bTrace, "anchor_b": bAnchor,
	})
}

// Unlink 删除一条对应。
func (s *Service) Unlink(ctx context.Context, id int64) error {
	if err := s.st.DeleteLink(ctx, id); err != nil {
		return err
	}
	return s.st.LogAction(ctx, "unlink_events", map[string]any{"id": id})
}

// UpdateGroupIndex 修改折射率并重新解释（触发重新对齐）。
func (s *Service) UpdateGroupIndex(ctx context.Context, id int64, n float64) error {
	rev, err := s.st.UpdateParams(ctx, id, store.ParamUpdate{GroupIndex: &n})
	if err != nil {
		return err
	}
	if err := s.st.LogAction(ctx, "update_group_index",
		map[string]any{"trace_id": id, "group_index": n, "rev": rev}); err != nil {
		return err
	}
	_, err = s.recomputeOneWithRev(ctx, id, rev)
	return err
}

// UpdateDeadzone 修改发射端死区并重新解释。
func (s *Service) UpdateDeadzone(ctx context.Context, id int64, meters float64) error {
	rev, err := s.st.UpdateParams(ctx, id, store.ParamUpdate{DeadzoneM: &meters})
	if err != nil {
		return err
	}
	if err := s.st.LogAction(ctx, "update_deadzone",
		map[string]any{"trace_id": id, "deadzone_m": meters, "rev": rev}); err != nil {
		return err
	}
	_, err = s.recomputeOneWithRev(ctx, id, rev)
	return err
}

func (s *Service) recomputeOne(ctx context.Context, id int64) (otdr.Interpretation, error) {
	rows, err := s.st.ListTraces(ctx)
	if err != nil {
		return otdr.Interpretation{}, err
	}
	for _, r := range rows {
		if r.ID == id {
			return s.recomputeOneWithRev(ctx, id, r.Rev)
		}
	}
	return otdr.Interpretation{}, store.ErrNotFound
}

func (s *Service) recomputeOneWithRev(ctx context.Context, id int64, rev int) (otdr.Interpretation, error) {
	tr, err := s.st.GetTrace(ctx, id)
	if err != nil {
		return otdr.Interpretation{}, err
	}
	return s.RecomputeTrace(ctx, tr, rev)
}

// AlignedPair 是两条轨迹在米制坐标上对齐后的展示模型。
type AlignedPair struct {
	A, B         otdr.Interpretation
	Links        []store.EventLink
	MaxDistanceM float64
}

// LoadPair 读取两条最新解释及它们之间的对应关系。
func (s *Service) LoadPair(ctx context.Context, idA, idB int64) (AlignedPair, error) {
	ia, err := s.st.LatestInterpretation(ctx, idA)
	if err != nil {
		return AlignedPair{}, err
	}
	ib, err := s.st.LatestInterpretation(ctx, idB)
	if err != nil {
		return AlignedPair{}, err
	}
	all, err := s.st.ListLinks(ctx)
	if err != nil {
		return AlignedPair{}, err
	}
	var links []store.EventLink
	for _, l := range all {
		if (l.TraceA == idA && l.TraceB == idB) || (l.TraceA == idB && l.TraceB == idA) {
			links = append(links, l)
		}
	}
	max := math.Max(ia.LastM, ib.LastM)
	return AlignedPair{A: ia, B: ib, Links: links, MaxDistanceM: max}, nil
}
