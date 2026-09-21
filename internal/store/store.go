// Package store 用 SQLite 持久化轨迹原始采样、参数修订、解释快照、
// 用户判定（幽灵确认/否决）、跨轨迹事件对应以及操作运行记录。
//
// 关键不变量：
//   - samples.power 为原始线性采样，一经写入不再修改；
//   - 改折射率/脉宽/死区只产生新的 param_rev，解释快照全部带 rev；
//   - 事件判定与对应以（轨迹, 锚点采样索引）为身份，跨修订保持。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store 封装数据库句柄。
type Store struct {
	db *sql.DB
}

// Open 打开（必要时创建）数据库并执行建表迁移。
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB 暴露底层句柄供高级仓储方法复用。
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS traces (
  id           INTEGER PRIMARY KEY,
  code         TEXT NOT NULL UNIQUE,
  name         TEXT NOT NULL,
  param_rev    INTEGER NOT NULL DEFAULT 1,
  pulse_ns     REAL NOT NULL,
  group_index  REAL NOT NULL,
  avg_count    INTEGER NOT NULL,
  sample_ns    REAL NOT NULL,
  deadzone_m   REAL NOT NULL,
  created_rev  INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS samples (
  trace_id  INTEGER NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
  idx       INTEGER NOT NULL,
  power     REAL NOT NULL,           -- 不可变原始线性采样
  PRIMARY KEY (trace_id, idx)
);

CREATE TABLE IF NOT EXISTS interpretations (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  trace_id     INTEGER NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
  rev          INTEGER NOT NULL,
  group_index  REAL NOT NULL,
  deadzone_m   REAL NOT NULL,
  payload      TEXT NOT NULL,        -- 完整解释快照 JSON
  created_at   TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (trace_id, rev)
);

CREATE TABLE IF NOT EXISTS event_verdicts (
  trace_id   INTEGER NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
  anchor_idx INTEGER NOT NULL,       -- 采样索引即事件身份
  verdict    TEXT NOT NULL,          -- ghost_confirm | ghost_reject | confirmed_facility
  parent_idx INTEGER NOT NULL DEFAULT -1,
  note       TEXT NOT NULL DEFAULT '',
  rev        INTEGER NOT NULL,       -- 最近一次操作时的参数修订号
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (trace_id, anchor_idx)
);

CREATE TABLE IF NOT EXISTS event_links (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  trace_a       INTEGER NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
  anchor_a      INTEGER NOT NULL,
  trace_b       INTEGER NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
  anchor_b      INTEGER NOT NULL,
  rev_a         INTEGER NOT NULL,
  rev_b         INTEGER NOT NULL,
  created_at    TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (trace_a, anchor_a, trace_b, anchor_b)
);

CREATE TABLE IF NOT EXISTS run_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  ts         TEXT NOT NULL DEFAULT (datetime('now')),
  action     TEXT NOT NULL,
  detail     TEXT NOT NULL DEFAULT '{}'
);
`

// LogAction 记录一次用户操作（可随运行记录导出）。
func (s *Store) LogAction(ctx context.Context, action string, detail any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("编码操作明细: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO run_log(action, detail) VALUES (?, ?)`, action, string(raw))
	return err
}
