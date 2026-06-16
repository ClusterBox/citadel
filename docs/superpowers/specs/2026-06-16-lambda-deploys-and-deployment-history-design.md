# Citadel: Lambda deploys, required deploy messages, and deployment history

**Date:** 2026-06-16
**Status:** Approved design, ready for implementation planning
**Branch:** `feat/lambda-deploys-and-history`

## Summary

Extend citadel beyond ECS so it can deploy container-image Lambda services
(e.g. smaug), require a human-written description on every deploy, persist a
record of every deployment to a local SQLite database, and surface that history
in the existing web dashboard. Ship developer tooling (Makefile + scripts) to
install, uninstall, and update the `citadel` binary.

## Goals

- Deploy container-image Lambda services via build → push → `UpdateFunctionCode`.
- Require a `-m/--message` describing each deploy; refuse to deploy without it.
- Record every deploy (success or failure) to `~/.citadel/deployments.db`.
- Add a deployments page to the existing webui dashboard, filterable by
  project/env.
- Provide `make install|uninstall|update` (via `go install`) plus thin scripts.

## Non-goals

- Migrating smaug's CDK away from `DockerImageCode_FromImageAsset` (flagged as a
  caveat, not built here).
- Creating ECR repos or Lambda functions from citadel (assumed to exist, created
  once via CDK / `--deploy-infra`).
- Multi-line `$EDITOR` message capture or interactive prompts (explicitly
  rejected — `-m` is required with no fallback).
- Authenticated/remote dashboard. The dashboard is local-only on `localhost`.

## Key decisions

| Decision | Choice |
|---|---|
| Lambda deploy mechanism | Build/push image → `lambda.UpdateFunctionCode` → wait for `LastUpdateStatus=Successful` |
| Deploy message | `-m/--message` **required**, no fallback; errors before any AWS work |
| History DB location | Global `~/.citadel/deployments.db` (all projects, one developer) |
| Dashboard scope | Record + web dashboard page now |
| Install tooling | `go install ./cmd/citadel` (GOBIN) |

## Architecture

### 1. Runtime-aware deploy pipeline

`pkg/pipeline/deploy.go` currently hardcodes the ECS update step. The build/push
step (git-SHA tag, push to `<name>-repo` ECR) stays unchanged and shared. After
build/push, branch on `cfg.ResolvedRuntime()`:

- `ecs` → existing `ecsClient.UpdateService` + optional `WaitForStableService`.
- `lambda` → `lambdaClient.UpdateFunctionCode(fnName, imageURI)` then poll
  `LastUpdateStatus=Successful`.

Extract the final update behind a small interface so the branch is unit-testable
with fakes:

```go
type Deployer interface {
    Update(ctx context.Context, cfg *config.DeployConfig, imageURI string) error
    WaitStable(ctx context.Context, cfg *config.DeployConfig) error
}
```

`selectDeployer(cfg, awsClient)` returns an ECS-backed or Lambda-backed
implementation based on the resolved runtime.

### 2. Lambda function naming

Smaug's function is per-env (`smaug-dev`, `smaug-prod`), but
`LambdaConfig.FunctionName` is a single string. Add to `pkg/config`:

```go
// ResolveFunctionName returns the Lambda function name for env.
// If lambda.functionName is set, it is used with "{env}" substituted.
// Otherwise the convention "<name>-<env>" is used (e.g. smaug-dev).
func (c *DeployConfig) ResolveFunctionName(env string) string
```

This mirrors the existing `<name>-cluster` / `<name>-service` ECS convention.
The logs daemon continues to use the raw `FunctionName` field, so existing
behaviour is unchanged when the field is set.

### 3. Lambda deploy client

`internal/aws/lambda.go` gains, alongside the existing `ResolveLogGroup`:

- `UpdateFunctionCode(ctx, fnName, imageURI string) error` — calls
  `lambda.UpdateFunctionCode` with `ImageUri`.
- `WaitForFunctionUpdated(ctx, fnName string) error` — polls
  `GetFunctionConfiguration` until `LastUpdateStatus` is `Successful` (error on
  `Failed`, with the status reason).

### 4. Required deploy message

`deployCmd` adds `-m/--message` and marks it required
(`MarkFlagRequired("message")`). An empty/missing value errors before any AWS
work. The value flows through `DeployOptions.Message`.

### 5. Deployment history store — `internal/deploydb`

New package mirroring `internal/logsdb` (an `Open(path)` that applies an embedded
`schema.sql` using the modernc sqlite driver, WAL pragmas). DB lives at
`~/.citadel/deployments.db`; the directory is created on first use.

Schema — single `deployments` table:

| Column | Notes |
|---|---|
| `id` | UUID/text PK |
| `project` | `cfg.Name` |
| `env` | target env |
| `runtime` | `ecs` / `lambda` |
| `region` | `cfg.Region` |
| `git_sha` | short SHA used as image tag |
| `image_uri` | full ECR URI pushed |
| `message` | required deploy description |
| `status` | `in_progress` / `success` / `failed` |
| `error` | failure text, nullable |
| `deployed_by` | OS user (`os/user`) |
| `target` | resolved service or function name |
| `started_at` | epoch ms |
| `finished_at` | epoch ms, nullable |
| `duration_ms` | nullable |

API:

```go
func (d *DB) Insert(ctx, rec Deployment) (id string, err error) // status=in_progress
func (d *DB) MarkSuccess(ctx, id string) error
func (d *DB) MarkFailed(ctx, id, errMsg string) error
func (d *DB) List(ctx, filter Filter) ([]Deployment, error)     // filter: project, env, limit
```

### 6. Pipeline records every deploy

After the message validates and the run is not a dry-run, the pipeline:

1. Inserts an `in_progress` row (capturing project/env/runtime/region/sha/
   image/message/deployed_by/target/started_at).
2. Uses a `defer` that calls `MarkSuccess` or `MarkFailed(err)` based on the
   pipeline's outcome, so even a build/push failure is recorded.

Dry-runs are not recorded. Recording failures are logged as warnings and never
abort a deploy.

### 7. Deployments dashboard

A new `citadel dashboard` command opens `~/.citadel/deployments.db` and serves a
deployments page on `localhost:5500`, reusing `internal/webui`'s embedded
templates + htmx approach. Because the logs dashboard runs in-container against
`logsdb` while this runs locally against `deploydb`, the data source is
decoupled: a constructor variant builds a `Server` backed by `deploydb` with no
`ingest` factory.

The page renders a table of recent deployments (project, env, runtime, target,
SHA, status, message, deployed_by, when, duration), color-coded by status, with
project/env filter controls and an htmx fragment endpoint for refresh.

### 8. Makefile + scripts

`Makefile` targets:

- `build` — `go build ./...` (and a local `./bin/citadel`).
- `install` — `go install ./cmd/citadel`.
- `uninstall` — `rm -f $(shell go env GOPATH)/bin/citadel`.
- `update` — `git pull && go install ./cmd/citadel`.
- `test` — `go test ./...`.
- `fmt` — `go fmt ./...`.

Thin `scripts/install.sh`, `scripts/uninstall.sh`, `scripts/update.sh` wrap the
corresponding targets and warn if the Go bin dir (`$GOBIN` or `$(go env
GOPATH)/bin`) is not on `PATH`.

## Data flow

```
citadel deploy --env <e> -m "<msg>"
  → load config + validate env
  → require message (else error)
  → sync SSM (unless --skip-ssm)
  → [--deploy-infra] cdk deploy
  → build image (git SHA) → push <name>-repo ECR
  → deploydb.Insert(in_progress)        # skipped on --dry-run
  → selectDeployer(runtime).Update(imageURI)
       ecs:    UpdateService [+ WaitForStableService]
       lambda: UpdateFunctionCode + WaitForFunctionUpdated
  → defer: MarkSuccess | MarkFailed(err)
  → [--stream-logs] stream CloudWatch

citadel dashboard
  → open ~/.citadel/deployments.db
  → serve localhost:5500 (deployments list + htmx refresh)
```

## Error handling

- Missing `-m` → fast cobra error, no AWS calls.
- Lambda function missing / image invalid → `UpdateFunctionCode` errors; the
  deferred recorder marks the row `failed` with the error; non-zero exit.
- `WaitForFunctionUpdated` sees `Failed` → error with status reason; recorded.
- deploydb open/write errors → warn and continue; never block a deploy.
- `~/.citadel` missing → created on first use.

## Testing

- `deploydb`: in-memory SQLite tests for `Insert` → `MarkSuccess`/`MarkFailed`
  and `List` filters (project/env/limit, ordering).
- `config`: `ResolveFunctionName` (explicit, `{env}` placeholder, convention
  default) and lambda-runtime validation.
- pipeline: runtime branch selects the right `Deployer` (fake ECS/Lambda),
  records success and failure rows.
- dashboard: handler renders a seeded in-memory DB (status colors, filters).

## Migration caveat (smaug)

Smaug's CDK uses `DockerImageCode_FromImageAsset`, so CDK owns the function's
image. Once citadel drives app deploys via `UpdateFunctionCode` against
`smaug-repo`, a later `cdk deploy` reverts the function image. Recommendation
(documented, not built here): use CDK / `--deploy-infra` for infra only and let
citadel own code updates — the same split already used for ECS. Prerequisites: a
`smaug-repo` ECR repository and an existing `smaug-<env>` function. A
`smaug/citadel.yml` with `runtime: lambda` (no explicit `functionName`, relying
on the `<name>-<env>` convention) is added as part of adoption.
