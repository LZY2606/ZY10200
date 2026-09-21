package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"otdrroom/internal/otdr"
)

// ErrNotFound 表示实体不存在。
var ErrNotFound = errors.New("记录不存在")

// TraceRow 是 traces 表的一行（不含采样数组）。
type TraceRow struct {
	ID         int64
	Code       string
	Name       string
	Rev        int
	Params     otdr.TraceParams
	CreatedRev int
}

func scanTrace(row interface {
	Scan(...any) error
}) (TraceRow, error) {
	var tr TraceRow
	err := row.Scan(&tr.ID, &tr.Code, &tr.Name, &tr.Rev,
		&tr.Params.PulseWidthNS, &tr.Params.GroupIndex, &tr.Params.AvgCount,
		&tr.Params.SampleInterval, &tr.Params.LaunchDeadzone, &tr.CreatedRev)
	return tr, err
}

const traceCols = `id, code, name, param_rev, pulse_ns, group_index, avg_count, sample_ns, deadzone_m, created_rev`

// ListTraces 按编码顺序返回全部轨迹。
func (s *Store) ListTraces(ctx context.Context) ([]TraceRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+traceCols+` FROM traces ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TraceRow
	for rows.Next() {
		tr, err := scanTrace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

// GetTrace 读取单条轨迹（含原始采样）。
func (s *Store) GetTrace(ctx context.Context, id int64) (otdr.Trace, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+traceCols+` FROM traces WHERE id = ?`, id)
	r, err := scanTrace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return otdr.Trace{}, ErrNotFound
	}
	if err != nil {
		return otdr.Trace{}, err
	}
	power, err := s.loadSamples(ctx, id)
	if err != nil {
		return otdr.Trace{}, err
	}
	return otdr.Trace{
		ID: r.ID, Code: r.Code, Name: r.Name, Params: r.Params,
		SamplePower: power, CreatedRev: r.CreatedRev,
	}, nil
}

func (s *Store) loadSamples(ctx context.Context, id int64) ([]float64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT power FROM samples WHERE trace_id = ? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// InsertTrace 事务性写入一条轨迹及其不可变原始采样。
func (s *Store) InsertTrace(ctx context.Context, tr otdr.Trace) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO traces(id, code, name, param_rev, pulse_ns, group_index,
		                   avg_count, sample_ns, deadzone_m, created_rev)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		tr.ID, tr.Code, tr.Name, tr.CreatedRev,
		tr.Params.PulseWidthNS, tr.Params.GroupIndex, tr.Params.AvgCount,
		tr.Params.SampleInterval, tr.Params.LaunchDeadzone, tr.CreatedRev); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO samples(trace_id, idx, power) VALUES (?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, p := range tr.SamplePower {
		if _, err := stmt.ExecContext(ctx, tr.ID, i, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TraceCount 返回轨迹数量。
func (s *Store) TraceCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM traces`).Scan(&n)
	return n, err
}

// ParamUpdate 描述一次参数修订。
type ParamUpdate struct {
	GroupIndex *float64
	DeadzoneM  *float64
}

// UpdateParams 修改折射率或发射端死区，param_rev 加 1。
// 原始采样不参与写入，身份（采样索引）完全不变。
func (s *Store) UpdateParams(ctx context.Context, id int64, u ParamUpdate) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var rev int
	if err := tx.QueryRowContext(ctx,
		`SELECT param_rev FROM traces WHERE id = ?`, id).Scan(&rev); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if u.GroupIndex != nil {
		if *u.GroupIndex <= 1 {
			return 0, fmt.Errorf("群折射率必须大于 1")
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE traces SET group_index = ? WHERE id = ?`, *u.GroupIndex, id); err != nil {
			return 0, err
		}
	}
	if u.DeadzoneM != nil {
		if *u.DeadzoneM < 0 {
			return 0, fmt.Errorf("发射端死区不能为负")
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE traces SET deadzone_m = ? WHERE id = ?`, *u.DeadzoneM, id); err != nil {
			return 0, err
		}
	}
	rev++
	if _, err := tx.ExecContext(ctx,
		`UPDATE traces SET param_rev = ? WHERE id = ?`, rev, id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return rev, nil
}

// SaveInterpretation 按 (trace, rev) upsert 一份解释快照。
func (s *Store) SaveInterpretation(ctx context.Context, it otdr.Interpretation) error {
	raw, err := json.Marshal(it)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO interpretations(trace_id, rev, group_index, deadzone_m, payload)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT(trace_id, rev) DO UPDATE SET
		   group_index = excluded.group_index,
		   deadzone_m  = excluded.deadzone_m,
		   payload     = excluded.payload`,
		it.TraceID, it.Rev, it.GroupIndex, it.DeadzoneM, string(raw))
	return err
}

// LatestInterpretation 返回某轨迹最新修订的解释快照。
func (s *Store) LatestInterpretation(ctx context.Context, traceID int64) (otdr.Interpretation, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT payload FROM interpretations WHERE trace_id = ?
		 ORDER BY rev DESC LIMIT 1`, traceID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return otdr.Interpretation{}, ErrNotFound
	}
	if err != nil {
		return otdr.Interpretation{}, err
	}
	var it otdr.Interpretation
	if err := json.Unmarshal([]byte(raw), &it); err != nil {
		return otdr.Interpretation{}, err
	}
	return it, nil
}

// Verdict 是用户对单个事件（按采样索引锚定）的判定。
type Verdict struct {
	TraceID   int64  `json:"trace_id"`
	AnchorIdx int    `json:"anchor_idx"`
	Verdict   string `json:"verdict"`
	ParentIdx int    `json:"parent_idx"`
	Note      string `json:"note"`
	Rev       int    `json:"rev"`
}

// UpsertVerdict 保存/更新用户对某事件的判定。
func (s *Store) UpsertVerdict(ctx context.Context, v Verdict) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO event_verdicts(trace_id, anchor_idx, verdict, parent_idx, note, rev)
		 VALUES (?,?,?,?,?,?)
		 ON CONFLICT(trace_id, anchor_idx) DO UPDATE SET
		   verdict = excluded.verdict,
		   parent_idx = excluded.parent_idx,
		   note = excluded.note,
		   rev = excluded.rev,
		   updated_at = datetime('now')`,
		v.TraceID, v.AnchorIdx, v.Verdict, v.ParentIdx, v.Note, v.Rev)
	return err
}

// ListVerdicts 返回某轨迹的全部判定（按锚点）。
func (s *Store) ListVerdicts(ctx context.Context, traceID int64) (map[int]Verdict, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT trace_id, anchor_idx, verdict, parent_idx, note, rev
		 FROM event_verdicts WHERE trace_id = ? ORDER BY anchor_idx`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]Verdict{}
	for rows.Next() {
		var v Verdict
		if err := rows.Scan(&v.TraceID, &v.AnchorIdx, &v.Verdict,
			&v.ParentIdx, &v.Note, &v.Rev); err != nil {
			return nil, err
		}
		out[v.AnchorIdx] = v
	}
	return out, rows.Err()
}

// EventLink 是用户显式确认的跨轨迹事件对应。
type EventLink struct {
	ID      int64 `json:"id"`
	TraceA  int64 `json:"trace_a"`
	AnchorA int   `json:"anchor_a"`
	TraceB  int64 `json:"trace_b"`
	AnchorB int   `json:"anchor_b"`
	RevA    int   `json:"rev_a"`
	RevB    int   `json:"rev_b"`
}

// InsertLink 保存一条跨轨迹对应；同一对锚点重复确认幂等。
func (s *Store) InsertLink(ctx context.Context, l EventLink) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO event_links(trace_a, anchor_a, trace_b, anchor_b, rev_a, rev_b)
		 VALUES (?,?,?,?,?,?)
		 ON CONFLICT(trace_a, anchor_a, trace_b, anchor_b) DO UPDATE SET
		   rev_a = excluded.rev_a, rev_b = excluded.rev_b`,
		l.TraceA, l.AnchorA, l.TraceB, l.AnchorB, l.RevA, l.RevB)
	return err
}

// DeleteLink 删除一条跨轨迹对应。
func (s *Store) DeleteLink(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM event_links WHERE id = ?`, id)
	return err
}

// ListLinks 返回全部跨轨迹对应。
func (s *Store) ListLinks(ctx context.Context) ([]EventLink, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, trace_a, anchor_a, trace_b, anchor_b, rev_a, rev_b
		 FROM event_links ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventLink
	for rows.Next() {
		var l EventLink
		if err := rows.Scan(&l.ID, &l.TraceA, &l.AnchorA, &l.TraceB,
			&l.AnchorB, &l.RevA, &l.RevB); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Reset 清空全部业务数据，便于重新导入复核（保留表结构）。
func (s *Store) Reset(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range []string{
		"event_links", "event_verdicts", "interpretations", "samples", "traces", "run_log",
	} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+t); err != nil {
			return err
		}
	}
	return tx.Commit()
}
