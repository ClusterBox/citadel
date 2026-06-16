# Lambda Deploys + Deploy Messages + Deployment History — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let citadel deploy container-image Lambda services, require a `-m` message on every deploy, persist all deploys to `~/.citadel/deployments.db`, and show them in a local web dashboard — plus Makefile/scripts to install the binary.

**Architecture:** A `Deployer` interface abstracts the final update step so the pipeline branches on `cfg.ResolvedRuntime()` (ECS `UpdateService` vs Lambda `UpdateFunctionCode`). A new `internal/deploydb` SQLite store (mirroring `internal/logsdb`) records every deploy; the pipeline inserts an `in_progress` row and a `defer` marks success/failure. A new `webui.DeployServer` reuses the embedded-template/htmx assets to serve a deployments page that `citadel dashboard` runs locally on `:5500`.

**Tech Stack:** Go 1.25, cobra, aws-sdk-go-v2 (`service/lambda`), modernc.org/sqlite, html/template + htmx.

**Branch:** `feat/lambda-deploys-and-history` (already created).

---

## File structure

- `pkg/config/schema.go` — add `ResolveFunctionName`, relax lambda validation (modify)
- `internal/deploydb/db.go` — new SQLite store for deployments (create)
- `internal/deploydb/schema.sql` — deployments table (create)
- `internal/deploydb/db_test.go` — store tests (create)
- `internal/aws/lambda.go` — add `UpdateFunctionCode`, `WaitForFunctionUpdated` (modify)
- `pkg/pipeline/deployer.go` — `Deployer` interface + ECS/Lambda impls + `selectDeployer` (create)
- `pkg/pipeline/deployer_test.go` — runtime selection test (create)
- `pkg/pipeline/deploy.go` — branch on runtime, record to deploydb, `Message` option (modify)
- `cmd/citadel/main.go` — required `-m/--message`, new `dashboard` command (modify)
- `internal/webui/deployments.go` — `DeployServer` (create)
- `internal/webui/templates/deployments.html` — page + rows fragment (create)
- `internal/webui/deployments_test.go` — handler render test (create)
- `Makefile` — build/install/uninstall/update/test/fmt (create)
- `scripts/install.sh`, `scripts/uninstall.sh`, `scripts/update.sh` — wrappers (create)
- `examples/smaug-citadel.yml` — reference lambda config (create)

---

## Task 1: Config — `ResolveFunctionName` + relaxed lambda validation

**Files:**
- Modify: `pkg/config/schema.go` (validation around lines 142-145; add method near `ResolvedRuntime`)
- Test: `pkg/config/schema_test.go`

- [ ] **Step 1: Write the failing test**

Add to `pkg/config/schema_test.go`:

```go
func TestResolveFunctionName(t *testing.T) {
	// convention default: <name>-<env>
	cfg := &DeployConfig{Name: "smaug"}
	if got := cfg.ResolveFunctionName("dev"); got != "smaug-dev" {
		t.Errorf("convention: got %q, want smaug-dev", got)
	}
	// explicit name wins
	cfg.Lambda = &LambdaConfig{FunctionName: "custom-fn"}
	if got := cfg.ResolveFunctionName("dev"); got != "custom-fn" {
		t.Errorf("explicit: got %q, want custom-fn", got)
	}
	// {env} placeholder substitution
	cfg.Lambda = &LambdaConfig{FunctionName: "smaug-{env}-fn"}
	if got := cfg.ResolveFunctionName("prod"); got != "smaug-prod-fn" {
		t.Errorf("placeholder: got %q, want smaug-prod-fn", got)
	}
}

func TestValidateLambdaWithoutFunctionName(t *testing.T) {
	cfg := &DeployConfig{Name: "smaug", Region: "us-east-1", Runtime: RuntimeLambda}
	if err := cfg.Validate(); err != nil {
		t.Errorf("lambda without functionName should be valid (convention), got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/config/ -run 'ResolveFunctionName|ValidateLambdaWithoutFunctionName' -v`
Expected: FAIL — `cfg.ResolveFunctionName undefined` and the validate test failing on the current "lambda.functionName is required" error.

- [ ] **Step 3: Add `ResolveFunctionName` and relax validation**

In `pkg/config/schema.go`, add after `ResolvedRuntime` (around line 49):

```go
// ResolveFunctionName returns the Lambda function name for env. When
// lambda.functionName is set it is used verbatim with "{env}" substituted;
// otherwise the "<name>-<env>" convention is used (e.g. smaug-dev), mirroring
// the ECS "<name>-cluster"/"<name>-service" conventions.
func (c *DeployConfig) ResolveFunctionName(env string) string {
	if c.Lambda != nil && c.Lambda.FunctionName != "" {
		return strings.ReplaceAll(c.Lambda.FunctionName, "{env}", env)
	}
	return fmt.Sprintf("%s-%s", c.Name, env)
}
```

Replace the `case RuntimeLambda:` block in `Validate()` (lines 142-145) with:

```go
	case RuntimeLambda:
		// functionName is optional: when unset we use the "<name>-<env>"
		// convention (see ResolveFunctionName).
		if len(c.Environments) == 0 {
			return fmt.Errorf("at least one environment is required")
		}
```

(`strings` and `fmt` are already imported in this file.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/config/ -v`
Expected: PASS (all config tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/config/schema.go pkg/config/schema_test.go
git commit -m "feat(config): resolve lambda function name by convention; relax lambda validation"
```

---

## Task 2: `internal/deploydb` — deployment history store

**Files:**
- Create: `internal/deploydb/schema.sql`
- Create: `internal/deploydb/db.go`
- Test: `internal/deploydb/db_test.go`

- [ ] **Step 1: Write the schema**

Create `internal/deploydb/schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS deployments (
    id           TEXT PRIMARY KEY,
    project      TEXT NOT NULL,
    env          TEXT NOT NULL,
    runtime      TEXT NOT NULL,
    region       TEXT NOT NULL,
    git_sha      TEXT NOT NULL,
    image_uri    TEXT NOT NULL,
    message      TEXT NOT NULL,
    status       TEXT NOT NULL,            -- in_progress | success | failed
    error        TEXT,
    deployed_by  TEXT NOT NULL,
    target       TEXT NOT NULL,            -- resolved service or function name
    started_at   INTEGER NOT NULL,         -- epoch ms
    finished_at  INTEGER,
    duration_ms  INTEGER
);

CREATE INDEX IF NOT EXISTS idx_deployments_started_at ON deployments (started_at DESC);
CREATE INDEX IF NOT EXISTS idx_deployments_project_env ON deployments (project, env);
```

- [ ] **Step 2: Write the failing test**

Create `internal/deploydb/db_test.go`:

```go
package deploydb

import (
	"context"
	"testing"
)

func mustOpen(t *testing.T) *DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func sample() Deployment {
	return Deployment{
		Project: "smaug", Env: "dev", Runtime: "lambda", Region: "us-east-1",
		GitSHA: "a1b2c3d", ImageURI: "123.dkr.ecr/smaug-repo:a1b2c3d",
		Message: "fix reminder scan", DeployedBy: "alice", Target: "smaug-dev",
	}
}

func TestInsertAndMarkSuccess(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()

	id, err := db.Insert(ctx, sample())
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}
	if err := db.MarkSuccess(ctx, id); err != nil {
		t.Fatalf("mark success: %v", err)
	}

	rows, err := db.List(ctx, Filter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Status != "success" {
		t.Errorf("status = %q, want success", rows[0].Status)
	}
	if rows[0].FinishedAt == nil || rows[0].DurationMS == nil {
		t.Error("expected finished_at and duration_ms to be set")
	}
}

func TestMarkFailed(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()
	id, _ := db.Insert(ctx, sample())
	if err := db.MarkFailed(ctx, id, "boom"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	rows, _ := db.List(ctx, Filter{Limit: 10})
	if rows[0].Status != "failed" || rows[0].Error != "boom" {
		t.Errorf("got status=%q error=%q, want failed/boom", rows[0].Status, rows[0].Error)
	}
}

func TestListFilters(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()
	a := sample() // smaug/dev
	b := sample()
	b.Project = "legolas"
	b.Env = "prod"
	db.Insert(ctx, a)
	db.Insert(ctx, b)

	rows, _ := db.List(ctx, Filter{Project: "legolas", Limit: 10})
	if len(rows) != 1 || rows[0].Project != "legolas" {
		t.Fatalf("project filter failed: %+v", rows)
	}
	rows, _ = db.List(ctx, Filter{Env: "dev", Limit: 10})
	if len(rows) != 1 || rows[0].Env != "dev" {
		t.Fatalf("env filter failed: %+v", rows)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/deploydb/ -v`
Expected: FAIL — package/`Open`/`Deployment`/`Filter` undefined.

- [ ] **Step 4: Write the store**

Create `internal/deploydb/db.go`:

```go
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
	_, err := d.ExecContext(ctx, `
		UPDATE deployments
		SET status = ?, error = ?, finished_at = ?,
		    duration_ms = ? - started_at
		WHERE id = ?`,
		status, nullable(errMsg), now, now, id)
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
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/deploydb/ -v`
Expected: PASS (TestInsertAndMarkSuccess, TestMarkFailed, TestListFilters).

- [ ] **Step 6: Commit**

```bash
git add internal/deploydb/
git commit -m "feat(deploydb): SQLite store for deployment history"
```

---

## Task 3: AWS Lambda client — `UpdateFunctionCode` + waiter

**Files:**
- Modify: `internal/aws/lambda.go`

- [ ] **Step 1: Add the methods**

Append to `internal/aws/lambda.go` (the file already imports `context`, `fmt`, `aws`, and `lambda`):

```go
// UpdateFunctionCode points the Lambda function at a new container image and
// returns once AWS accepts the update request (status is then polled via
// WaitForFunctionUpdated).
func (lc *LambdaClient) UpdateFunctionCode(ctx context.Context, functionName, imageURI string) error {
	if functionName == "" {
		return fmt.Errorf("lambda function name is empty")
	}
	_, err := lc.client.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
		FunctionName: aws.String(functionName),
		ImageUri:     aws.String(imageURI),
	})
	if err != nil {
		return fmt.Errorf("update function code %s: %w", functionName, err)
	}
	return nil
}

// WaitForFunctionUpdated blocks until the function's LastUpdateStatus is
// Successful (or returns an error if it becomes Failed or the timeout elapses).
func (lc *LambdaClient) WaitForFunctionUpdated(ctx context.Context, functionName string) error {
	waiter := lambda.NewFunctionUpdatedV2Waiter(lc.client)
	if err := waiter.Wait(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String(functionName),
	}, 5*time.Minute); err != nil {
		return fmt.Errorf("wait for function %s update: %w", functionName, err)
	}
	return nil
}
```

Add `"time"` to the import block in `internal/aws/lambda.go`.

- [ ] **Step 2: Verify it compiles and tidy modules**

Run: `go build ./internal/aws/ && go mod tidy`
Expected: builds clean; `go mod tidy` promotes `github.com/aws/aws-sdk-go-v2/service/lambda` from indirect to a direct dependency in `go.mod`.

- [ ] **Step 3: Commit**

```bash
git add internal/aws/lambda.go go.mod go.sum
git commit -m "feat(aws): Lambda UpdateFunctionCode and update waiter"
```

---

## Task 4: Pipeline — `Deployer` interface + runtime selection

**Files:**
- Create: `pkg/pipeline/deployer.go`
- Test: `pkg/pipeline/deployer_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/pipeline/deployer_test.go`:

```go
package pipeline

import (
	"testing"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/pkg/config"
)

func TestSelectDeployerByRuntime(t *testing.T) {
	awsClient := &aws.Client{}

	ecsCfg := &config.DeployConfig{Name: "legolas", Runtime: config.RuntimeECS}
	if _, ok := selectDeployer(ecsCfg, awsClient).(ecsDeployer); !ok {
		t.Errorf("ecs runtime: expected ecsDeployer")
	}

	lambdaCfg := &config.DeployConfig{Name: "smaug", Runtime: config.RuntimeLambda}
	if _, ok := selectDeployer(lambdaCfg, awsClient).(lambdaDeployer); !ok {
		t.Errorf("lambda runtime: expected lambdaDeployer")
	}

	// default (unset runtime) resolves to ecs
	defCfg := &config.DeployConfig{Name: "legolas"}
	if _, ok := selectDeployer(defCfg, awsClient).(ecsDeployer); !ok {
		t.Errorf("default runtime: expected ecsDeployer")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/pipeline/ -run TestSelectDeployerByRuntime -v`
Expected: FAIL — `selectDeployer`, `ecsDeployer`, `lambdaDeployer` undefined.

- [ ] **Step 3: Write the deployer abstraction**

Create `pkg/pipeline/deployer.go`:

```go
package pipeline

import (
	"context"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/pkg/config"
)

// Deployer performs the runtime-specific "update to new image" step and an
// optional wait for the deployment to stabilize.
type Deployer interface {
	Update(ctx context.Context, cfg *config.DeployConfig, env, imageURI string) error
	WaitStable(ctx context.Context, cfg *config.DeployConfig, env string) error
}

// ecsDeployer forces a new ECS deployment (the task definition already
// references the :latest image just pushed).
type ecsDeployer struct{ c *aws.ECSClient }

func (d ecsDeployer) Update(ctx context.Context, cfg *config.DeployConfig, _ , _ string) error {
	return d.c.UpdateService(ctx, cfg)
}

func (d ecsDeployer) WaitStable(ctx context.Context, cfg *config.DeployConfig, _ string) error {
	return d.c.WaitForStableService(ctx, cfg)
}

// lambdaDeployer points the function at the freshly pushed image.
type lambdaDeployer struct{ c *aws.LambdaClient }

func (d lambdaDeployer) Update(ctx context.Context, cfg *config.DeployConfig, env, imageURI string) error {
	return d.c.UpdateFunctionCode(ctx, cfg.ResolveFunctionName(env), imageURI)
}

func (d lambdaDeployer) WaitStable(ctx context.Context, cfg *config.DeployConfig, env string) error {
	return d.c.WaitForFunctionUpdated(ctx, cfg.ResolveFunctionName(env))
}

// selectDeployer returns the Deployer matching cfg's resolved runtime.
func selectDeployer(cfg *config.DeployConfig, awsClient *aws.Client) Deployer {
	if cfg.ResolvedRuntime() == config.RuntimeLambda {
		return lambdaDeployer{c: awsClient.NewLambdaClient()}
	}
	return ecsDeployer{c: awsClient.NewECSClient()}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/pipeline/ -run TestSelectDeployerByRuntime -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/pipeline/deployer.go pkg/pipeline/deployer_test.go
git commit -m "feat(pipeline): Deployer interface with ECS/Lambda implementations"
```

---

## Task 5: Pipeline — branch on runtime + record deploys

**Files:**
- Modify: `pkg/pipeline/deploy.go`

- [ ] **Step 1: Add `Message` to options and a recorder helper**

In `pkg/pipeline/deploy.go`, add a field to `DeployOptions` (after `TailLines int`):

```go
	Message     string
```

Add these imports to the file's import block: `"os/user"`, and `"github.com/ClusterBox/citadel/internal/deploydb"`.

Add this helper at the end of `pkg/pipeline/deploy.go`:

```go
// deployRecorder opens the local deployment-history DB and returns an insert
// id plus a finish func. All failures degrade gracefully (warn, no-op) so
// history never blocks a deploy. Returns a no-op finisher on any error.
func deployRecorder(ctx context.Context, cfg *config.DeployConfig, opts *DeployOptions, imageURI, target string) func(err error) {
	noop := func(error) {}
	dbPath, err := deploydb.DefaultPath()
	if err != nil {
		fmt.Printf("   ⚠️  deployment history disabled: %v\n", err)
		return noop
	}
	db, err := deploydb.Open(dbPath)
	if err != nil {
		fmt.Printf("   ⚠️  deployment history disabled: %v\n", err)
		return noop
	}

	who := "unknown"
	if u, uerr := user.Current(); uerr == nil {
		who = u.Username
	}
	gitSHA, _ := getGitSHA()

	id, err := db.Insert(ctx, deploydb.Deployment{
		Project: cfg.Name, Env: opts.Environment, Runtime: string(cfg.ResolvedRuntime()),
		Region: cfg.Region, GitSHA: gitSHA, ImageURI: imageURI, Message: opts.Message,
		DeployedBy: who, Target: target,
	})
	if err != nil {
		fmt.Printf("   ⚠️  could not record deployment: %v\n", err)
		db.Close()
		return noop
	}

	return func(deployErr error) {
		defer db.Close()
		if deployErr != nil {
			_ = db.MarkFailed(ctx, id, deployErr.Error())
			return
		}
		_ = db.MarkSuccess(ctx, id)
	}
}
```

- [ ] **Step 2: Replace the ECS-only update step (lines ~97-119) with a runtime branch + recording**

Replace this block in `Deploy`:

```go
	// 5. Update ECS service
	if !opts.DryRun {
		fmt.Printf("🚀 Deploying to ECS...\n")

		awsClient, err := aws.NewClient(ctx, cfg.Region)
		if err != nil {
			return fmt.Errorf("failed to create AWS client: %w", err)
		}

		ecsClient := awsClient.NewECSClient()
		if err := ecsClient.UpdateService(ctx, cfg); err != nil {
			return fmt.Errorf("failed to update ECS service: %w", err)
		}

		// Wait for stable if requested
		if opts.Wait {
			if err := ecsClient.WaitForStableService(ctx, cfg); err != nil {
				return fmt.Errorf("service did not stabilize: %w", err)
			}
		}

		fmt.Printf("\n")
	}
```

with:

```go
	// 5. Update the running service (runtime-specific) and record the deploy.
	if !opts.DryRun {
		runtime := cfg.ResolvedRuntime()
		fmt.Printf("🚀 Deploying to %s...\n", runtime)

		awsClient, err := aws.NewClient(ctx, cfg.Region)
		if err != nil {
			return fmt.Errorf("failed to create AWS client: %w", err)
		}

		target := resolveTarget(cfg, opts.Environment)
		finish := deployRecorder(ctx, cfg, opts, imageTag, target)

		deployer := selectDeployer(cfg, awsClient)
		if err := deployer.Update(ctx, cfg, opts.Environment, imageTag); err != nil {
			finish(err)
			return fmt.Errorf("failed to update %s: %w", runtime, err)
		}
		if opts.Wait {
			if err := deployer.WaitStable(ctx, cfg, opts.Environment); err != nil {
				finish(err)
				return fmt.Errorf("deployment did not stabilize: %w", err)
			}
		}
		finish(nil)
		fmt.Printf("\n")
	}
```

Add this helper next to `deployRecorder`:

```go
// resolveTarget returns the human-facing deploy target: the Lambda function
// name for lambda runtime, otherwise the ECS service name.
func resolveTarget(cfg *config.DeployConfig, env string) string {
	if cfg.ResolvedRuntime() == config.RuntimeLambda {
		return cfg.ResolveFunctionName(env)
	}
	if cfg.ECS != nil && cfg.ECS.Service != "" {
		return cfg.ECS.Service
	}
	return fmt.Sprintf("%s-service", cfg.Name)
}
```

Note: `imageTag` is the variable returned by `buildAndPushImage` earlier in `Deploy` (the full ECR image URI). It is in scope at this point.

- [ ] **Step 3: Verify build and existing tests**

Run: `go build ./... && go test ./pkg/pipeline/ ./internal/deploydb/ -v`
Expected: builds clean; pipeline and deploydb tests PASS.

- [ ] **Step 4: Commit**

```bash
git add pkg/pipeline/deploy.go
git commit -m "feat(pipeline): runtime-aware deploy + record deployment history"
```

---

## Task 6: Deploy command — required `-m/--message`

**Files:**
- Modify: `cmd/citadel/main.go` (deploy command block, lines ~37-76)

- [ ] **Step 1: Read the message flag and pass it through**

In the deploy command's `RunE`, after the existing `tailLines, _ := cmd.Flags().GetInt("tail")` line, add:

```go
			message, _ := cmd.Flags().GetString("message")
```

Add `Message: message,` to the `pipeline.DeployOptions{...}` literal (next to `TailLines: tailLines,`).

- [ ] **Step 2: Register the flag and mark it required**

After `deployCmd.Flags().Int("tail", 100, ...)`, add:

```go
	deployCmd.Flags().StringP("message", "m", "", "Description of what this deploy does (required)")
	_ = deployCmd.MarkFlagRequired("message")
```

- [ ] **Step 3: Verify the flag is enforced**

Run: `go build -o /tmp/citadel ./cmd/citadel && /tmp/citadel deploy --env dev`
Expected: exits non-zero with `Error: required flag(s) "message" not set` (no AWS calls made).

Run: `/tmp/citadel deploy --help`
Expected: help lists `-m, --message string` as required.

- [ ] **Step 4: Commit**

```bash
git add cmd/citadel/main.go
git commit -m "feat(cmd): require -m/--message on deploy"
```

---

## Task 7: Web dashboard — `DeployServer` + template

**Files:**
- Create: `internal/webui/templates/deployments.html`
- Create: `internal/webui/deployments.go`
- Test: `internal/webui/deployments_test.go`

- [ ] **Step 1: Write the template**

Create `internal/webui/templates/deployments.html`:

```html
{{define "deployments_page"}}<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Citadel — Deployments</title>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/htmx.min.js"></script>
</head>
<body>
  <h1>🏰 Deployments</h1>
  <form hx-get="/deployments/rows" hx-target="#rows" hx-trigger="change, submit">
    <input type="text" name="project" placeholder="project" value="{{.Project}}">
    <input type="text" name="env" placeholder="env" value="{{.Env}}">
    <button type="submit">Filter</button>
  </form>
  <table>
    <thead>
      <tr><th>When</th><th>Project</th><th>Env</th><th>Runtime</th><th>Target</th>
          <th>SHA</th><th>Status</th><th>Message</th><th>By</th><th>Took</th></tr>
    </thead>
    <tbody id="rows" hx-get="/deployments/rows" hx-trigger="load, every 5s">
      {{template "deployments_rows" .}}
    </tbody>
  </table>
</body>
</html>{{end}}

{{define "deployments_rows"}}{{range .Rows}}<tr class="status-{{.Status}}">
  <td>{{fmtTime .StartedAt}}</td><td>{{.Project}}</td><td>{{.Env}}</td>
  <td>{{.Runtime}}</td><td>{{.Target}}</td><td>{{.GitSHA}}</td>
  <td>{{.Status}}</td><td>{{.Message}}</td><td>{{.DeployedBy}}</td>
  <td>{{.Took}}</td>
</tr>{{else}}<tr><td colspan="10">No deployments recorded yet.</td></tr>{{end}}{{end}}
```

- [ ] **Step 2: Write the failing test**

Create `internal/webui/deployments_test.go`:

```go
package webui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ClusterBox/citadel/internal/deploydb"
)

func seededDeployDB(t *testing.T) *deploydb.DB {
	t.Helper()
	db, err := deploydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	id, _ := db.Insert(context.Background(), deploydb.Deployment{
		Project: "smaug", Env: "dev", Runtime: "lambda", Region: "us-east-1",
		GitSHA: "a1b2c3d", ImageURI: "uri", Message: "fix reminder scan",
		DeployedBy: "alice", Target: "smaug-dev",
	})
	_ = db.MarkSuccess(context.Background(), id)
	return db
}

func TestDeployServerRendersRows(t *testing.T) {
	srv, err := NewDeployServer(seededDeployDB(t))
	if err != nil {
		t.Fatalf("NewDeployServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/deployments")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if !strings.Contains(body, "smaug") || !strings.Contains(body, "fix reminder scan") {
		t.Errorf("expected seeded deploy in page, got:\n%s", body)
	}
	if !strings.Contains(body, "status-success") {
		t.Errorf("expected status class in page")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/webui/ -run TestDeployServerRendersRows -v`
Expected: FAIL — `NewDeployServer` undefined.

- [ ] **Step 4: Write the DeployServer**

Create `internal/webui/deployments.go`:

```go
package webui

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/ClusterBox/citadel/internal/deploydb"
)

// DeployServer serves the local deployment-history dashboard. It reuses the
// embedded htmx/static assets but is backed by deploydb instead of logsdb.
type DeployServer struct {
	db        *deploydb.DB
	templates *template.Template
}

// NewDeployServer parses the deployments template and returns a server.
func NewDeployServer(db *deploydb.DB) (*DeployServer, error) {
	tpl, err := template.New("").Funcs(template.FuncMap{
		"fmtTime": func(ms int64) string {
			return time.UnixMilli(ms).Format("2006-01-02 15:04")
		},
	}).ParseFS(assetsFS, "templates/deployments.html")
	if err != nil {
		return nil, fmt.Errorf("parse deployments template: %w", err)
	}
	return &DeployServer{db: db, templates: tpl}, nil
}

// Handler wires the routes:
//
//	GET /                  -> redirect to /deployments
//	GET /static/...        -> embedded assets
//	GET /healthz           -> "ok"
//	GET /deployments       -> full page
//	GET /deployments/rows  -> htmx tbody fragment (filterable)
func (s *DeployServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS())))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/deployments", http.StatusFound)
	})
	mux.HandleFunc("GET /deployments", s.handlePage)
	mux.HandleFunc("GET /deployments/rows", s.handleRows)
	return mux
}

type deployRow struct {
	deploydb.Deployment
	Took string
}

type deployView struct {
	Project string
	Env     string
	Rows    []deployRow
}

func (s *DeployServer) view(ctx context.Context, project, env string) (deployView, error) {
	recs, err := s.db.List(ctx, deploydb.Filter{Project: project, Env: env, Limit: 200})
	if err != nil {
		return deployView{}, err
	}
	rows := make([]deployRow, 0, len(recs))
	for _, r := range recs {
		took := "—"
		if r.DurationMS != nil {
			took = (time.Duration(*r.DurationMS) * time.Millisecond).Round(time.Second).String()
		}
		rows = append(rows, deployRow{Deployment: r, Took: took})
	}
	return deployView{Project: project, Env: env, Rows: rows}, nil
}

func (s *DeployServer) handlePage(w http.ResponseWriter, r *http.Request) {
	v, err := s.view(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("env"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.templates.ExecuteTemplate(w, "deployments_page", v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *DeployServer) handleRows(w http.ResponseWriter, r *http.Request) {
	v, err := s.view(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("env"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.templates.ExecuteTemplate(w, "deployments_rows", v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

Note: `staticFS()` and `assetsFS` already exist in `internal/webui/server.go` and are reused here.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/webui/ -v`
Expected: PASS (existing logs tests + TestDeployServerRendersRows).

- [ ] **Step 6: Commit**

```bash
git add internal/webui/deployments.go internal/webui/deployments_test.go internal/webui/templates/deployments.html
git commit -m "feat(webui): deployments dashboard server and template"
```

---

## Task 8: `citadel dashboard` command

**Files:**
- Modify: `cmd/citadel/main.go`

- [ ] **Step 1: Add the command**

In `cmd/citadel/main.go`, ensure these imports are present: `"errors"`, `"net/http"`, `"github.com/ClusterBox/citadel/internal/deploydb"`, `"github.com/ClusterBox/citadel/internal/webui"`. Then add a command builder before `rootCmd.AddCommand(...)` calls:

```go
	// dashboard command — local deployment-history web UI
	dashboardCmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Serve the local deployment-history dashboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, _ := cmd.Flags().GetString("addr")

			dbPath, err := deploydb.DefaultPath()
			if err != nil {
				return err
			}
			db, err := deploydb.Open(dbPath)
			if err != nil {
				return fmt.Errorf("open deployments db: %w", err)
			}
			defer db.Close()

			srv, err := webui.NewDeployServer(db)
			if err != nil {
				return err
			}

			httpSrv := &http.Server{Addr: addr, Handler: srv.Handler()}
			fmt.Printf("🏰 Deployments dashboard: http://%s/deployments\n", addr)
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	dashboardCmd.Flags().String("addr", "localhost:5500", "Address to serve the dashboard on")
```

Register it with the others:

```go
	rootCmd.AddCommand(dashboardCmd)
```

- [ ] **Step 2: Verify it builds and serves**

Run: `go build -o /tmp/citadel ./cmd/citadel && /tmp/citadel dashboard --addr localhost:5599 &`
Then: `sleep 1 && curl -s localhost:5599/healthz`
Expected: prints `ok`. Then stop it: `kill %1`.

- [ ] **Step 3: Commit**

```bash
git add cmd/citadel/main.go
git commit -m "feat(cmd): citadel dashboard serves deployment history locally"
```

---

## Task 9: Makefile + install scripts

**Files:**
- Create: `Makefile`
- Create: `scripts/install.sh`, `scripts/uninstall.sh`, `scripts/update.sh`

- [ ] **Step 1: Write the Makefile**

Create `Makefile`:

```makefile
.PHONY: build install uninstall update test fmt

GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

build:
	go build -o bin/citadel ./cmd/citadel

install:
	go install ./cmd/citadel
	@echo "Installed citadel to $(GOBIN)/citadel"
	@case ":$$PATH:" in *":$(GOBIN):"*) ;; *) echo "⚠️  $(GOBIN) is not on your PATH";; esac

uninstall:
	rm -f $(GOBIN)/citadel
	@echo "Removed $(GOBIN)/citadel"

update:
	git pull --ff-only
	go install ./cmd/citadel
	@echo "Updated citadel in $(GOBIN)"

test:
	go test ./...

fmt:
	go fmt ./...
```

- [ ] **Step 2: Write the wrapper scripts**

Create `scripts/install.sh`:

```sh
#!/usr/bin/env sh
set -e
cd "$(dirname "$0")/.."
make install
```

Create `scripts/uninstall.sh`:

```sh
#!/usr/bin/env sh
set -e
cd "$(dirname "$0")/.."
make uninstall
```

Create `scripts/update.sh`:

```sh
#!/usr/bin/env sh
set -e
cd "$(dirname "$0")/.."
make update
```

- [ ] **Step 3: Make scripts executable and verify install**

Run:
```bash
chmod +x scripts/install.sh scripts/uninstall.sh scripts/update.sh
make build && ls bin/citadel
make install && command -v citadel
```
Expected: `bin/citadel` exists; `make install` reports the install path (and `command -v citadel` resolves it if GOBIN is on PATH).

- [ ] **Step 4: Commit**

```bash
git add Makefile scripts/
git commit -m "build: Makefile and install/uninstall/update scripts"
```

---

## Task 10: Smaug reference config + adoption docs

**Files:**
- Create: `examples/smaug-citadel.yml`

- [ ] **Step 1: Discover smaug's real values**

Run (read-only):
```bash
grep -n "account\|region\|FunctionName\|stage" /home/alphauser/Documents/github/clusterbox/backend/smaug/cdk/cdk.go | head
grep -rn "ssm\|PARAM\|secret" /home/alphauser/Documents/github/clusterbox/backend/smaug/scripts/sync-secrets.sh | head
```
Use the account id(s), region, and secret names found to fill the file below. The function name is left to the `<name>-<env>` convention (`smaug-dev`/`smaug-prod`), so no `functionName` is set.

- [ ] **Step 2: Write the reference config**

Create `examples/smaug-citadel.yml` (replace `<ACCOUNT_ID>` and the `secrets:` list with the values discovered in Step 1):

```yaml
name: smaug
region: us-east-1
runtime: lambda

# functionName omitted on purpose: citadel uses the "<name>-<env>" convention,
# resolving to smaug-dev / smaug-prod. Set lambda.functionName (supports {env})
# only to override.

environments:
  dev:
    account: "<ACCOUNT_ID>"
  prod:
    account: "<ACCOUNT_ID>"

# Secret env-var names synced to SSM. Pull these from smaug's sync-secrets.sh.
secrets:
  - DATABASE_URL
```

- [ ] **Step 3: Validate the example parses**

Run:
```bash
go build -o /tmp/citadel ./cmd/citadel
/tmp/citadel deploy --config examples/smaug-citadel.yml --env dev -m "noop" --dry-run --skip-ssm
```
Expected: gets past config load/validation (runtime lambda accepted, no functionName required); dry-run prints the build/push plan and does not call AWS update.

- [ ] **Step 4: Commit**

```bash
git add examples/smaug-citadel.yml
git commit -m "docs: smaug lambda citadel.yml reference config"
```

---

## Final verification

- [ ] **Run the full suite**

Run: `go build ./... && go test ./... && go vet ./...`
Expected: builds, all tests PASS, vet clean.

- [ ] **Manual smoke (optional, requires AWS + smaug repo/function)**

Run: `citadel deploy --config examples/smaug-citadel.yml --env dev -m "first citadel lambda deploy"`
Then: `citadel dashboard` and open http://localhost:5500/deployments — the deploy appears with status `success`.
