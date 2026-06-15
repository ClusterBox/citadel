// Package logsdb is the SQLite persistence layer for the citadel-logs daemon.
//
// The store is intentionally small: services, ingest cursors, and error
// events. Raw, non-error log lines are not persisted (errors carry the raw
// line in their own column). The daemon owns the lifecycle of the database
// file at /data/citadel-logs.db inside the container.
package logsdb

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// DB wraps *sql.DB with the domain-specific queries the daemon needs.
type DB struct {
	*sql.DB
}

// Open creates or opens the SQLite database at path and applies the schema.
// path may be ":memory:" for tests.
func Open(path string) (*DB, error) {
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	if path == ":memory:" {
		dsn = ":memory:?_pragma=foreign_keys(1)"
	}
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := sqldb.Exec(schemaSQL); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &DB{sqldb}, nil
}

// Service is the row stored in the services table.
type Service struct {
	ID       string
	Name     string
	Env      string
	Region   string
	Runtime  string
	LogGroup string
	RepoPath string
}

// UpsertService replaces the row for s.ID. Called at daemon startup for every
// registered service after log group resolution.
func (d *DB) UpsertService(ctx context.Context, s Service) error {
	_, err := d.ExecContext(ctx, `
		INSERT INTO services (id, name, env, region, runtime, log_group, repo_path)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  name=excluded.name,
		  env=excluded.env,
		  region=excluded.region,
		  runtime=excluded.runtime,
		  log_group=excluded.log_group,
		  repo_path=excluded.repo_path
	`, s.ID, s.Name, s.Env, s.Region, s.Runtime, s.LogGroup, s.RepoPath)
	return err
}

// ListServices returns services ordered by ID for stable UI rendering.
func (d *DB) ListServices(ctx context.Context) ([]Service, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT id, name, env, region, runtime, log_group, repo_path
		FROM services ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Service
	for rows.Next() {
		var s Service
		if err := rows.Scan(&s.ID, &s.Name, &s.Env, &s.Region, &s.Runtime, &s.LogGroup, &s.RepoPath); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PruneServices removes service rows whose IDs are not in keep. The FK cascade
// also clears their cursors and error events. Used on registry hot-reload.
func (d *DB) PruneServices(ctx context.Context, keep []string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE keep(id TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	for _, id := range keep {
		if _, err := tx.ExecContext(ctx, `INSERT INTO keep(id) VALUES(?)`, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM services WHERE id NOT IN (SELECT id FROM keep)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE keep`); err != nil {
		return err
	}
	return tx.Commit()
}

// LoadCursor returns the last persisted polling cursor for serviceID. Returns
// (0, false, nil) when no cursor exists yet.
func (d *DB) LoadCursor(ctx context.Context, serviceID string) (int64, bool, error) {
	var ts int64
	err := d.QueryRowContext(ctx, `SELECT last_ts FROM ingest_cursor WHERE service_id = ?`, serviceID).Scan(&ts)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return ts, true, nil
}

// SaveCursor persists the polling cursor for serviceID.
func (d *DB) SaveCursor(ctx context.Context, serviceID string, lastTS int64) error {
	_, err := d.ExecContext(ctx, `
		INSERT INTO ingest_cursor (service_id, last_ts, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(service_id) DO UPDATE SET
		  last_ts=excluded.last_ts,
		  updated_at=excluded.updated_at
	`, serviceID, lastTS, time.Now().UnixMilli())
	return err
}

// ErrorEvent is one persisted 500-class event.
type ErrorEvent struct {
	ID         int64
	ServiceID  string
	TS         int64
	Status     sql.NullInt64
	Level      sql.NullString
	Message    string
	RequestID  sql.NullString
	Stack      sql.NullString
	Raw        string
	LogStream  sql.NullString
	CWEventID  sql.NullString
}

// InsertErrorEvent writes an event; ON CONFLICT(cw_event_id) DO NOTHING dedupes
// across daemon restarts that re-poll the same window. Returns true if a new
// row was actually inserted.
func (d *DB) InsertErrorEvent(ctx context.Context, e ErrorEvent) (bool, error) {
	res, err := d.ExecContext(ctx, `
		INSERT INTO error_events
		  (service_id, ts, status, level, message, request_id, stack, raw, log_stream, cw_event_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cw_event_id) DO NOTHING
	`, e.ServiceID, e.TS, e.Status, e.Level, e.Message, e.RequestID, e.Stack, e.Raw, e.LogStream, e.CWEventID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RecentErrors returns up to limit events for serviceID, newest first.
func (d *DB) RecentErrors(ctx context.Context, serviceID string, limit int) ([]ErrorEvent, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT id, service_id, ts, status, level, message, request_id, stack, raw, log_stream, cw_event_id
		FROM error_events
		WHERE service_id = ?
		ORDER BY ts DESC
		LIMIT ?
	`, serviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ErrorEvent
	for rows.Next() {
		var e ErrorEvent
		if err := rows.Scan(&e.ID, &e.ServiceID, &e.TS, &e.Status, &e.Level, &e.Message, &e.RequestID, &e.Stack, &e.Raw, &e.LogStream, &e.CWEventID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ErrorByID returns a single event for the detail view.
func (d *DB) ErrorByID(ctx context.Context, id int64) (*ErrorEvent, error) {
	var e ErrorEvent
	err := d.QueryRowContext(ctx, `
		SELECT id, service_id, ts, status, level, message, request_id, stack, raw, log_stream, cw_event_id
		FROM error_events WHERE id = ?
	`, id).Scan(&e.ID, &e.ServiceID, &e.TS, &e.Status, &e.Level, &e.Message, &e.RequestID, &e.Stack, &e.Raw, &e.LogStream, &e.CWEventID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// CountByService returns a map of serviceID → 7-day error count, used for the
// left-rail badges.
func (d *DB) CountByService(ctx context.Context, since int64) (map[string]int, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT service_id, COUNT(*) FROM error_events
		WHERE ts >= ? GROUP BY service_id
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// Sweep deletes events older than olderThan (ms epoch). Returns rows removed.
func (d *DB) Sweep(ctx context.Context, olderThan int64) (int64, error) {
	res, err := d.ExecContext(ctx, `DELETE FROM error_events WHERE ts < ?`, olderThan)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
