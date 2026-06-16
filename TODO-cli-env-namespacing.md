# TODO: Env-namespace the `citadel deploy` CLI path

## Context

The CDK construct (`pkg/constructs/deployable`) was updated so every per-environment
ECS resource is named `<name>-<env>` (e.g. `legolas-dev-cluster`) via the new
`DeployConfig.ResolvedName(env)` helper. This keeps dev and prod isolated even when
they share one AWS account. The image tag is also no longer hardcoded to `latest`:
the task definition reads the `imageTag` CDK context (defaulting to `latest`), so
deploys can pin an immutable `<sha>` revision.

The **`citadel deploy` CLI deployer** (`pkg/pipeline` + `internal/aws`) was **not**
updated. It still resolves cluster/service/SSM names from bare `cfg.Name` and is not
env-aware. As a result it is now **inconsistent with the construct**: the construct
creates `legolas-dev-cluster`, but the CLI would target `legolas-cluster`, and it
deploys `:latest` while the task def may be pinned to a `<sha>`.

This path is currently **not used by legolas** (legolas deploys via its own
`scripts/deploy.sh` + `cdk deploy`). The work below brings the CLI to parity so
`citadel deploy` can deploy env-namespaced, SHA-pinned services for any project.

There is a `TODO(env-namespacing)` marker at `internal/aws/ecs.go` (above
`resolveCluster`) pointing here.

## Goal

`citadel deploy --env <e>` should:

1. Push the image to the env-namespaced ECR repo (`<name>-<env>-repo`).
2. Write secrets to the env-namespaced SSM prefix (`/<name>-<env>/<KEY>`).
3. Target the env-namespaced cluster/service (`<name>-<env>-cluster` / `-service`).
4. Pass `--context imageTag=<sha>` to `cdk deploy` so the rolled task def pins the
   immutable image (matching the construct's `imageTag` context).

## Changes required

### 1. Thread `env` into the ECS deploy path

The `Deployer` interface already receives `env`, but `ecsDeployer` drops it and the
`ECSClient` methods never take it.

- **`pkg/pipeline/deployer.go`**
  - `ecsDeployer.Update` / `WaitStable`: stop discarding `env` (the `_` params);
    pass it to the `ECSClient` calls.
- **`internal/aws/ecs.go`**
  - `resolveCluster(cfg, env)` → `fmt.Sprintf("%s-cluster", cfg.ResolvedName(env))`
    (keep the explicit `cfg.ECS.Cluster` override taking precedence).
  - `resolveService(cfg, env)` → `fmt.Sprintf("%s-service", cfg.ResolvedName(env))`
    (keep the explicit `cfg.ECS.Service` override).
  - `UpdateService`, `GetServiceStatus`, `WaitForStableService`, `DiscoverLogGroup`:
    add an `env string` param and pass it to `resolveCluster`/`resolveService`.
  - Remove the `TODO(env-namespacing)` comment once done.

### 2. Env-namespace the ECR repo in the image build

- **`pkg/pipeline/deploy.go`** (`buildAndPushImage`, ~line 219)
  - `repoName := fmt.Sprintf("%s-repo", cfg.Name)` →
    `fmt.Sprintf("%s-repo", cfg.ResolvedName(opts.Environment))`.

### 3. Env-namespace the SSM prefix

- **`internal/aws/ssm.go`** (`SyncSecrets`, ~line 41)
  - `paramName := fmt.Sprintf("/%s/%s", cfg.Name, secretName)` →
    `fmt.Sprintf("/%s/%s", cfg.ResolvedName(env), secretName)`.
  - `SyncSecrets` needs an `env string` param; thread it from the call site in
    `pkg/pipeline/deploy.go` (~line 63), which already has `opts.Environment`.

### 4. Pin the image tag when invoking CDK

- **`pkg/pipeline/deploy.go`** (the `cdk deploy` invocation, ~lines 167–177)
  - Add `--context imageTag=<sha>` alongside the existing `--context env=<e>` so the
    rolled task def references the immutable image instead of `latest`. The git SHA
    is already computed in `buildAndPushImage` (`getGitSHA`); make it available here
    (e.g. return it / compute once and pass through).
  - Update the dry-run print to include the `imageTag` context.

### 5. `resolveTarget` (human-facing target string)

- **`pkg/pipeline/deploy.go`** (~line 285)
  - ECS branch: `fmt.Sprintf("%s-service", cfg.Name)` →
    `fmt.Sprintf("%s-service", cfg.ResolvedName(env))` (keep `cfg.ECS.Service`
    override). `env` is already a param here.

### 6. Update tests

- **`internal/aws/ecs_test.go`**
  - `resolveCluster` / `resolveService` calls now take `env`. Update the convention
    assertions: with `env="dev"`, expect `legolas-dev-cluster` / `legolas-dev-service`.
  - Keep the explicit-override cases (`cfg.ECS.Cluster` / `cfg.ECS.Service`) asserting
    the override still wins regardless of env.
  - The log-group extraction test (`/ecs/my-service`) is unaffected.
- Add/adjust any `SyncSecrets` test to assert the `/<name>-<env>/<KEY>` path.

## Design notes

- The `ecs.cluster` / `ecs.service` overrides in `ECSConfig` exist for adopting ECS
  resources **not** created by Citadel. Preserve them: env-namespacing is only the
  *fallback* convention. Do not env-suffix an explicit override.
- Use `cfg.ResolvedName(env)` everywhere rather than re-deriving `<name>-<env>` inline,
  so the convention has a single source of truth (already used by the construct).
- `env` is validated/sourced upstream as `opts.Environment`; it is always non-empty on
  the deploy path. `ResolvedName` does not guard against empty env (matching
  `ResolveFunctionName`).

## Out of scope / follow-ups

- Migrating existing un-namespaced resources (e.g. an already-deployed
  `legolas-cluster`) is a per-project operational task, not a code change.
- `legolas` does not use this CLI path; verifying the change end-to-end needs a
  project that deploys via `citadel deploy`.
