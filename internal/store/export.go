package store

import (
	"context"
	"encoding/json"
)

// RunRecord 是一次完整运行的可导出快照。
type RunRecord struct {
	Format          string             `json:"format"`
	Version         int                `json:"version"`
	Traces          []otdrTracePayload `json:"traces"`
	Interpretations []interpPayload    `json:"interpretations"`
	Verdicts        []Verdict          `json:"verdicts"`
	Links           []EventLink        `json:"links"`
	RunLog          []runLogEntry      `json:"run_log"`
}

type otdrTracePayload struct {
	ID          int64           `json:"id"`
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	CreatedRev  int             `json:"created_rev"`
	ParamRev    int             `json:"param_rev"`
	Params      traceParamsJSON `json:"params"`
	SamplePower []float64       `json:"sample_power"`
}

type traceParamsJSON struct {
	PulseWidthNS   float64 `json:"pulse_width_ns"`
	GroupIndex     float64 `json:"group_index"`
	AvgCount       int     `json:"avg_count"`
	SampleInterval float64 `json:"sample_interval_ns"`
	LaunchDeadzone float64 `json:"launch_deadzone_m"`
}

type interpPayload struct {
	TraceID int64  `json:"trace_id"`
	Rev     int    `json:"rev"`
	Payload string `json:"payload"`
}

type runLogEntry struct {
	ID     int64  `json:"id"`
	TS     string `json:"ts"`
	Action string `json:"action"`
	Detail string `json:"detail"`
}

// ExportRun 导出当前全部运行记录为 JSON。
func (s *Store) ExportRun(ctx context.Context) (RunRecord, error) {
	rec := RunRecord{Format: "otdrroom-run", Version: 1}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+traceCols+` FROM traces ORDER BY id`)
	if err != nil {
		return rec, err
	}
	for rows.Next() {
		tr, err := scanTrace(rows)
		if err != nil {
			rows.Close()
			return rec, err
		}
		power, err := s.loadSamples(ctx, tr.ID)
		if err != nil {
			rows.Close()
			return rec, err
		}
		rec.Traces = append(rec.Traces, otdrTracePayload{
			ID: tr.ID, Code: tr.Code, Name: tr.Name,
			CreatedRev: tr.CreatedRev, ParamRev: tr.Rev,
			Params: traceParamsJSON{
				PulseWidthNS:   tr.Params.PulseWidthNS,
				GroupIndex:     tr.Params.GroupIndex,
				AvgCount:       tr.Params.AvgCount,
				SampleInterval: tr.Params.SampleInterval,
				LaunchDeadzone: tr.Params.LaunchDeadzone,
			},
			SamplePower: power,
		})
	}
	rows.Close()

	irows, err := s.db.QueryContext(ctx,
		`SELECT trace_id, rev, payload FROM interpretations ORDER BY trace_id, rev`)
	if err != nil {
		return rec, err
	}
	for irows.Next() {
		var p interpPayload
		if err := irows.Scan(&p.TraceID, &p.Rev, &p.Payload); err != nil {
			irows.Close()
			return rec, err
		}
		rec.Interpretations = append(rec.Interpretations, p)
	}
	irows.Close()

	vrows, err := s.db.QueryContext(ctx,
		`SELECT trace_id, anchor_idx, verdict, parent_idx, note, rev
		 FROM event_verdicts ORDER BY trace_id, anchor_idx`)
	if err != nil {
		return rec, err
	}
	for vrows.Next() {
		var v Verdict
		if err := vrows.Scan(&v.TraceID, &v.AnchorIdx, &v.Verdict,
			&v.ParentIdx, &v.Note, &v.Rev); err != nil {
			vrows.Close()
			return rec, err
		}
		rec.Verdicts = append(rec.Verdicts, v)
	}
	vrows.Close()

	links, err := s.ListLinks(ctx)
	if err != nil {
		return rec, err
	}
	rec.Links = links

	lrows, err := s.db.QueryContext(ctx,
		`SELECT id, ts, action, detail FROM run_log ORDER BY id`)
	if err != nil {
		return rec, err
	}
	for lrows.Next() {
		var e runLogEntry
		if err := lrows.Scan(&e.ID, &e.TS, &e.Action, &e.Detail); err != nil {
			lrows.Close()
			return rec, err
		}
		rec.RunLog = append(rec.RunLog, e)
	}
	lrows.Close()
	return rec, lrows.Err()
}

// ExportRunJSON 导出并序列化为带缩进的 JSON。
func (s *Store) ExportRunJSON(ctx context.Context) ([]byte, error) {
	rec, err := s.ExportRun(ctx)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(rec, "", "  ")
}

// ImportRun 从导出快照恢复全部数据（调用前通常先 Reset）。
// 原始采样按快照原样写回，采样索引身份保持不变。
func (s *Store) ImportRun(ctx context.Context, rec RunRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, t := range rec.Traces {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO traces(id, code, name, param_rev, pulse_ns, group_index,
			                   avg_count, sample_ns, deadzone_m, created_rev)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			t.ID, t.Code, t.Name, t.ParamRev,
			t.Params.PulseWidthNS, t.Params.GroupIndex, t.Params.AvgCount,
			t.Params.SampleInterval, t.Params.LaunchDeadzone, t.CreatedRev); err != nil {
			return err
		}
		for i, p := range t.SamplePower {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO samples(trace_id, idx, power) VALUES (?,?,?)`,
				t.ID, i, p); err != nil {
				return err
			}
		}
	}
	for _, p := range rec.Interpretations {
		var q map[string]any
		if err := json.Unmarshal([]byte(p.Payload), &q); err != nil {
			return err
		}
		gi, _ := q["group_index"].(float64)
		dz, _ := q["deadzone_m"].(float64)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO interpretations(trace_id, rev, group_index, deadzone_m, payload)
			 VALUES (?,?,?,?,?)`, p.TraceID, p.Rev, gi, dz, p.Payload); err != nil {
			return err
		}
	}
	for _, v := range rec.Verdicts {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO event_verdicts(trace_id, anchor_idx, verdict, parent_idx, note, rev)
			 VALUES (?,?,?,?,?,?)`,
			v.TraceID, v.AnchorIdx, v.Verdict, v.ParentIdx, v.Note, v.Rev); err != nil {
			return err
		}
	}
	for _, l := range rec.Links {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO event_links(trace_a, anchor_a, trace_b, anchor_b, rev_a, rev_b)
			 VALUES (?,?,?,?,?,?)`,
			l.TraceA, l.AnchorA, l.TraceB, l.AnchorB, l.RevA, l.RevB); err != nil {
			return err
		}
	}
	for _, e := range rec.RunLog {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO run_log(id, action, detail) VALUES (?,?,?)`,
			e.ID, e.Action, e.Detail); err != nil {
			return err
		}
	}
	return tx.Commit()
}
