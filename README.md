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
- [ ] CLI scaffolding
- [ ] Config parser
- [ ] SSM secret sync
- [ ] Docker build/push
- [ ] ECS deployment
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

## License

MIT
