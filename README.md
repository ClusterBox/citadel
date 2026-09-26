# Citadel

A declarative CDK deployment framework for AWS ECS/Fargate.

## Overview

Citadel makes container deployments as simple as the Serverless Framework experience — but targeting ECS/Fargate instead of Lambda. You declare your app once in a `citadel.yml`. The tool handles SSM secret syncing, CDK infrastructure deployment, Docker build/push, and ECS service rollout in a single command.

```bash
citadel deploy --env dev --deploy-infra
```

## The Problem

Deploying ECS applications involves managing configuration across multiple files:
- Secret lists in deployment scripts
- Duplicate secret definitions in CDK code
- Infrastructure config scattered across multiple files


## The Solution

**Single source of truth:** `citadel.yml` defines everything about your deployment.

Both the CLI tool and CDK constructs read from the same file. Write a secret name once, use it everywhere.

## Installation

```bash
# Go
go install github.com/ClusterBox/citadel/cmd/citadel@latest

# From source
git clone https://github.com/ClusterBox/citadel.git
cd citadel
make install
```

## Usage

### Full deployment pipeline

```bash
citadel deploy --env dev --deploy-infra
```

### Start a project

```bash
citadel init --ecs       # or --lambda; prompts for anything not passed as a flag
citadel init --lambda --yes --account 123456789012   # non-interactive
```

`init` writes a commented `citadel.yml` and a `.citadel/` directory next to it.
If `citadel.yml` already exists it is left alone and only `.citadel/` is
created (pass `--force` to regenerate the config). `citadel.yaml` is accepted
when `citadel.yml` is absent.

### The `.citadel/` directory

| Path | Committed | Contents |
|---|---|---|
| `.citadel/project.yml` | yes | project id, name, runtime, citadel version that created it |
| `.citadel/state/<env>.json` | no | last deploy of each env from this checkout (shown by `citadel status`) |
| `.citadel/runs/<id>/` | no | one folder per deploy: `run.json` + a log file per step (newest 50 kept) |

`citadel deploy` creates `.citadel/` automatically if it is missing. Dry runs
record nothing.

`citadel` always excludes `.citadel/` from the Docker build context it sends
to the daemon, the same way it excludes `.git/`. Tools that hash or copy the
repo themselves instead of going through `citadel deploy` — e.g. a CDK
`DockerImageFunction`/`fromImageAsset` pointing at the repo root — should add
`.citadel/` to their own `.dockerignore`.

### Deploy from GitHub Actions

```yaml
on:
  push:
    branches: [main, development]

jobs:
  deploy:
    runs-on: ubuntu-latest
    environment: ${{ github.ref_name == 'main' && 'prod' || 'dev' }}
    permissions:
      contents: read
    concurrency: { group: citadel-${{ github.ref_name }}, cancel-in-progress: false }
    steps:
      - uses: actions/checkout@v4
      - uses: aws-actions/configure-aws-credentials@v4
        with:
          aws-access-key-id: ${{ secrets.AWS_ACCESS_KEY_ID }}
          aws-secret-access-key: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
          aws-region: us-east-1
      - uses: ClusterBox/citadel/action@v0.3.0
        with:
          deploy-infra: true
          env-file: ${{ secrets.CITADEL_ENV_FILE }}
```

`main` deploys `prod` and `development` deploys `dev` (override with
`environment` or `branch-map`). See [action/README.md](action/README.md).

### Pipeline

With no `pipeline:` key, `citadel deploy` runs its built-in sequence
(`ssm-sync, build, cdk, deploy, config-sync (lambda only), wait`) exactly as
before. To customize the order, mix in shell commands, run a one-off ECS
task, or add a health check, declare the deploy as an explicit list of
steps. `pipeline:` needs citadel v0.4.0 or later (GitHub Action:
`ClusterBox/citadel/action@v0.4.0` or later); older versions ignore the key
and run the default pipeline.

```yaml
pipeline:
  - uses: citadel/ssm-sync
  - name: test
    run: npm test
  - uses: citadel/build
  - name: migrate
    task: npx prisma migrate deploy
    timeout: 15m
  - uses: citadel/cdk
  - uses: citadel/deploy
  - uses: citadel/wait
  - name: smoke
    http: https://api-${env}.example.com/health
    rollback_on_failure: true
```

Each step gets its own log under `.citadel/runs/<id>/` and its own line in
`run.json`. Validation runs when the config loads (`citadel deploy`,
`citadel status`, `citadel init`, and the GitHub Action all get it), so a
bad pipeline is rejected before any AWS call. Every error names the step's
position and name: `pipeline[<i>] "<name>": <problem>`. An unknown key in a
step (e.g. a misspelled `environment:` or `rollback-on-failure:`) is an
error too.

**Step kinds.** Exactly one of `uses`, `run`, `task`, `http` must be set per
step.

| Field | Applies to | Default | Meaning |
|---|---|---|---|
| `uses` | built-in | — | `citadel/ssm-sync`, `citadel/build`, `citadel/cdk`, `citadel/deploy`, `citadel/config-sync`, `citadel/wait` |
| `run` | shell | — | Command string |
| `shell` | run | `bash -euo pipefail -c` | Interpreter, given as a list, e.g. `[sh, -c]` |
| `working_directory` | run | the config's directory | Relative to the config's directory; `${var}`s are expanded |
| `env` | run | — | `map[string]string`; values get `${var}` substitution |
| `task` | task | — | A string, run as `sh -c <string>`, or a YAML list passed as the command as-is |
| `container` | task | the container whose image repository is the service's ECR repository | Which container gets the command override |
| `http` | http | — | URL |
| `expect_status` | http | `200` | Must be 100–599 |
| `retries` | http | `10` | Total attempts; at least 1 |
| `interval` | http | `6s` | Go duration, between attempts |
| `request_timeout` | http | `5s` | Go duration, per attempt |
| `rollback_on_failure` | http | `false` | Only valid on an `http` step placed after `citadel/deploy` |
| `name` | all | built-ins: the action name (e.g. `ssm-sync`) | Required for `run`/`task`/`http`; must be unique and match `^[a-z0-9][a-z0-9-]*$` (it becomes the step's log file name); may not be a built-in name or `rollback` |
| `envs` | all | all environments | Only run this step in these environments; others print `⏭️  Skipping <name> (not for <env>)` |
| `continue_on_error` | all | `false` | The step's failure is recorded but the run continues. Not allowed on `citadel/build`, `citadel/cdk` or `citadel/deploy`, nor together with `rollback_on_failure` |
| `timeout` | run, task | `30m` | Go duration |

**Variables**, expanded in `run`, `env` values, `working_directory`, `task`,
`container` and `http`:

| Variable | Value |
|---|---|
| `${env}` | the environment being deployed |
| `${name}` | the project name |
| `${image}` | the pushed image URI (empty before `citadel/build` runs) |
| `${sha}` | the short git SHA |
| `${region}` | the config's region |
| `${account}` | the environment's account |

Only a lowercase `${word}` is a citadel variable — an unknown one is a
config load error. Anything else, such as `${HOME}` or `$PATH`, passes
through untouched for the shell (or the receiving server, for `http`) to
resolve.

`run:` steps also get the variables as environment variables:
`CITADEL_ENV`, `CITADEL_NAME`, `CITADEL_IMAGE`, `CITADEL_SHA`,
`CITADEL_REGION` and `CITADEL_ACCOUNT`. They win over a key of the same
name in the step's `env:`.

**Ordering rules**, enforced at load time:
- `citadel/build` must come before `citadel/cdk`, `citadel/deploy`, and
  every `task:` step.
- `citadel/cdk` must come before `citadel/deploy` (when present — it's
  optional).
- `citadel/deploy`, `citadel/config-sync` and `citadel/wait` may each
  appear at most once.
- `citadel/config-sync` and `citadel/wait` must come after
  `citadel/deploy`.
- When `citadel/build` has `envs:`, `citadel/cdk`, `citadel/deploy` and
  every `task:` step need the image it pushes, so each must list `envs:`
  that are a subset of build's (an omitted `envs:` means every environment
  and is rejected).
- A pipeline without `citadel/build` or `citadel/deploy` is valid, e.g. one
  that only runs migrations.

**Built-ins keep their CLI flag gates:** `citadel/ssm-sync` syncs from the
env file given by `--env-file` (default `.env`) and skips with `--skip-ssm`
or `--env-file ""`; `citadel/cdk` runs only with `--deploy-infra`;
`citadel/config-sync` skips with `--skip-config`; `citadel/wait` runs only
with `--wait`. `citadel/config-sync` is valid only for the `lambda`
runtime; `task:` is valid only for the `ecs` runtime.

Because `citadel/wait` only runs with `--wait`, an `http:` check placed
after it runs as soon as the rollout starts when `--wait` is not given, and
may probe the old tasks that are still serving traffic.

**Failure handling:**
- A step with `continue_on_error: true` that fails prints a warning and
  stays `failed` in `run.json`, but the run keeps going.
- An `http` step with `rollback_on_failure: true` that fails instead runs
  an automatic rollback, recorded as its own `rollback` step: ECS rolls
  the service back to the task-definition revision it ran before this
  deploy; Lambda rolls the function back to its previous image. The
  snapshot of "what ran before" is taken right before the first
  `citadel/cdk` or `citadel/deploy` step that actually runs — but **only**
  when some step in the pipeline has `rollback_on_failure: true`, so a
  pipeline without one makes no extra AWS call. If the snapshot could not
  be taken, the deploy still proceeds; a rollback attempted after that has
  nothing to restore and fails with "cannot roll back: no snapshot of the
  previous deployment". A deploy cancelled with Ctrl-C/SIGTERM never rolls
  back: it prints `↩️  Not rolling back: deploy was cancelled` and stops.
  If the revision to restore was deregistered in the meantime (e.g. by a
  CDK deploy), ECS rollback registers an identical copy and uses that.
- Otherwise a step's failure stops the pipeline; the run, the state file
  and the deploydb row all record it.

**`task:` is ECS-only.** It runs a one-off task in the freshly built
image, on the service's own network (subnets, security groups, launch
type), overriding the command on the target container. It needs
`ecs:RunTask`, `ecs:DescribeTasks`, `ecs:StopTask`, `logs:GetLogEvents`,
and `iam:PassRole` on the task and execution roles. `rollback_on_failure`
needs `ecs:DescribeServices`/`ecs:DescribeTaskDefinition`/`ecs:UpdateService`
(plus `ecs:RegisterTaskDefinition` when the old revision was deregistered)
(ECS) or
`lambda:UpdateFunctionCode`/`lambda:GetFunction` (Lambda) — see
[action/README.md](action/README.md#required-aws-permissions) for the full
list.

**`citadel deploy --dry-run`** prints the plan without changing anything
(it still reads from AWS): each
built-in prints its existing dry-run line, `run:` prints
`[dry-run] Would run: <cmd>`, `task:` prints
`[dry-run] Would run task: <cmd>`, and `http:` prints
`[dry-run] Would check: <url>`. Nothing is recorded in `.citadel/runs/`.

## citadel-logs daemon

Citadel ships a separate always-on binary, `citadel-logs`, that watches
CloudWatch log groups for every registered  service and surfaces
500-class errors at <http://localhost:5500/logs>.

It works across runtimes:

- `runtime: ecs` (default)  NestJS backends
- `runtime: lambda` — clusterbox Go Lambdas like smaug. Requires a
  `lambda: { functionName: ... }` block.

### Run it as a service (Linux)

```bash
# 1. Install both binaries (citadel + citadel-logs)
make install

# 2. Register a repo
cd ~/Documents/github/my-backend
citadel logs-daemon register --env dev

# 3. Start the daemon as a systemd user service
citadel logs-daemon start

# 4. Open the dashboard
open http://localhost:5500/logs
```

`citadel logs-daemon start` installs a systemd `--user` unit
(`~/.config/systemd/user/citadel-logs.service`), enables it, and turns on user
lingering so it survives reboots and starts before you log in. It captures
`AWS_PROFILE` / `AWS_REGION` from your shell (override with `--profile` /
`--region`). The daemon stores its SQLite db under
`~/.local/share/citadel/citadel-logs.db` and serves the dashboard on
`127.0.0.1:5500`.

Other commands:

```bash
citadel logs-daemon status            # is it running?
citadel logs-daemon logs              # tail the journal (-n N, --no-follow)
citadel logs-daemon restart           # re-install unit + restart
citadel logs-daemon stop [--disable]  # stop (and optionally disable autostart)
```

On non-Linux platforms, use the Docker path instead:
`docker compose -f docker-compose.logs.yml up -d`.

## Roadmap

- [x] Project architecture
- [x] CLI scaffolding
- [x] Config parser
- [x] SSM secret sync
- [x] Docker build/push
- [x] ECS deployment
- [ ] CDK construct library

## Configuration

### `queues:` — SQS access (optional)

Grants the ECS task role least-privilege access to existing SQS queues.
Queues are split by intent:

```yaml
queues:
  consume:
    - arn:aws:sqs:us-east-1:123456789012:incoming
  produce:
    - arn:aws:sqs:us-east-1:123456789012:outgoing
```

- `consume` queues are granted `sqs:ReceiveMessage`, `sqs:DeleteMessage`,
  `sqs:GetQueueAttributes`, and `sqs:ChangeMessageVisibility`.
- `produce` queues are granted `sqs:SendMessage` and `sqs:GetQueueAttributes`.

A queue ARN may appear in both lists if the service both reads and writes it.
Citadel does not create the queues — they must already exist.

## Releasing

Push a tag `vX.Y.Z` on `main`. `.github/workflows/release.yml` runs the tests and
publishes `citadel_<version>_<os>_<arch>.tar.gz` archives plus `checksums.txt`.
The GitHub Action at the same tag (`ClusterBox/citadel/action@vX.Y.Z`) installs
exactly that release. `make release-snapshot` builds the archives locally.

## License

MIT
