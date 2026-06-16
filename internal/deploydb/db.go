// Package deploydb is the SQLite persistence layer for citadel deployment
// history. It records every non-dry-run deploy (success or failure) so the
// `citadel dashboard` command can list them. The database lives at
// ~/.citadel/deployments.db on the developer's machine.
package deploydb

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// DB wraps *sql.DB with deployment-history queries.
type DB struct {
	*sql.DB
}

// DefaultPath returns ~/.citadel/deployments.db, creating ~/.citadel if needed.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, ".citadel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return filepath.Join(dir, "deployments.db"), nil
}

// Open creates or opens the SQLite database at path and applies the schema.
// path may be ":memory:" for tests.
func Open(path string) (*DB, error) {
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	if path == ":memory:" {
		dsn = ":memory:"
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

// Deployment is a row in the deployments table.
type Deployment struct {
	ID         string
	Project    string
	Env        string
	Runtime    string
	Region     string
	GitSHA     string
	ImageURI   string
	Message    string
	Status     string
	Error      string
	DeployedBy string
	Target     string
	StartedAt  int64
	FinishedAt *int64
	DurationMS *int64
}

// Filter narrows List results. Zero-value fields are ignored.
type Filter struct {
	Project string
	Env     string
	Limit   int
}

// Insert writes a new row with status="in_progress" and returns its id.
func (d *DB) Insert(ctx context.Context, rec Deployment) (string, error) {
	id := newID()
	now := time.Now().UnixMilli()
	_, err := d.ExecContext(ctx, `
		INSERT INTO deployments
			(id, project, env, runtime, region, git_sha, image_uri, message,
			 status, deployed_by, target, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'in_progress', ?, ?, ?)`,
		id, rec.Project, rec.Env, rec.Runtime, rec.Region, rec.GitSHA,
		rec.ImageURI, rec.Message, rec.DeployedBy, rec.Target, now)
	if err != nil {
		return "", fmt.Errorf("insert deployment: %w", err)
	}
	return id, nil
}

// MarkSuccess sets status=success and stamps finished_at/duration_ms.
func (d *DB) MarkSuccess(ctx context.Context, id string) error {
	return d.finish(ctx, id, "success", "")
}

// MarkFailed sets status=failed, records errMsg, and stamps finished_at.
func (d *DB) MarkFailed(ctx context.Context, id, errMsg string) error {
	return d.finish(ctx, id, "failed", errMsg)
}

func (d *DB) finish(ctx context.Context, id, status, errMsg string) error {
	now := time.Now().UnixMilli()
	// Retrieve started_at to compute duration in Go, avoiding any ambiguity
	// with how the modernc SQLite driver evaluates arithmetic expressions in
	// UPDATE statements when scanning back into *int64.
	var startedAt int64
	if err := d.QueryRowContext(ctx, `SELECT started_at FROM deployments WHERE id = ?`, id).Scan(&startedAt); err != nil {
		return fmt.Errorf("fetch started_at for %s: %w", id, err)
	}
	durationMS := now - startedAt
	_, err := d.ExecContext(ctx, `
		UPDATE deployments
		SET status = ?, error = ?, finished_at = ?, duration_ms = ?
		WHERE id = ?`,
		status, nullable(errMsg), now, durationMS, id)
	if err != nil {
		return fmt.Errorf("finish deployment %s: %w", id, err)
	}
	return nil
}

// List returns deployments newest-first, optionally filtered.
func (d *DB) List(ctx context.Context, f Filter) ([]Deployment, error) {
	q := `SELECT id, project, env, runtime, region, git_sha, image_uri, message,
	             status, COALESCE(error,''), deployed_by, target,
	             started_at, finished_at, duration_ms
	      FROM deployments`
	var where []string
	var args []any
	if f.Project != "" {
		where = append(where, "project = ?")
		args = append(args, f.Project)
	}
	if f.Env != "" {
		where = append(where, "env = ?")
		args = append(args, f.Env)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY started_at DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()

	var out []Deployment
	for rows.Next() {
		var r Deployment
		if err := rows.Scan(&r.ID, &r.Project, &r.Env, &r.Runtime, &r.Region,
			&r.GitSHA, &r.ImageURI, &r.Message, &r.Status, &r.Error,
			&r.DeployedBy, &r.Target, &r.StartedAt, &r.FinishedAt,
			&r.DurationMS); err != nil {
			return nil, fmt.Errorf("scan deployment: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
