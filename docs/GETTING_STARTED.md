# Getting Started with Citadel

## Quick Start

### 1. Install Citadel

```bash
go install github.com/ClusterBox/citadel/cmd/citadel@latest
```

### 2. Create `citadel.yml`

```yaml
name: my-api
region: us-east-1

container:
  port: 8080
  cpu: 256
  memory: 512
  health_check_path: /health
  health_check_grace_period: 60

environments:
  dev:
    account: "123456789012"
    min_capacity: 1
    max_capacity: 2
    fargate_spot: true
  prod:
    account: "123456789012"
    min_capacity: 2
    max_capacity: 10
    fargate_spot: false

secrets:
  - DATABASE_URL
  - API_KEY
  - JWT_SECRET
```

### 3. Create `.env`

```bash
DATABASE_URL=postgres://localhost:5432/mydb
API_KEY=secret-key-here
JWT_SECRET=jwt-secret-here
```

### 4. Create `Dockerfile`

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o server .

FROM alpine:latest
COPY --from=builder /app/server /server
EXPOSE 8080
CMD ["/server"]
```

### 5. Deploy!

```bash
# First time: deploy infrastructure + app
citadel deploy --env dev --deploy-infra

# Subsequent deploys: just app
citadel deploy --env dev
```

## What Just Happened?

Citadel just:
1. ✅ Synced secrets from `.env` → AWS SSM Parameter Store
2. ✅ Deployed complete ECS infrastructure via CDK
3. ✅ Built your Docker image
4. ✅ Pushed to ECR with git SHA tag
5. ✅ Updated ECS service with new image

All from one command. All from one config file.

## Full Example: Migrating from Manual CDK

See `examples/legolas/` for a real production service migration.

### Before Citadel

**Scattered config across 3+ files:**
- `scripts/deploy.sh` - 250 lines, manual secret list
- `cdk/cdk.go` - 400 lines, duplicate secret definitions
- `.env` - secret values
- `Dockerfile` - build config

**Pain points:**
- Add `INSTAGRAM_APP_ID` → update 2 files
- Miss one → deployment fails with cryptic error
- Stack enters `UPDATE_ROLLBACK_FAILED` → manual recovery

### After Citadel

**Single source of truth:**
- `citadel.yml` - 50 lines, defines everything
- `.env` - secret values
- `Dockerfile` - build config

**Benefits:**
- Add `INSTAGRAM_APP_ID` → update 1 file (citadel.yml)
- Automatic secret sync + CDK wiring
- Zero drift between config and infrastructure

## Commands

```bash
# Full pipeline (sync + infra + build + deploy)
citadel deploy --env dev --deploy-infra

# Just sync secrets
citadel deploy --env dev --skip-build

# Deploy with wait for stability
citadel deploy --env dev --wait

# Dry run (show what would happen)
citadel deploy --env dev --dry-run
```

## Next Steps

- Read [citadel.yml Reference](citadel-yml-reference.md)
- See [CDK Construct Usage](../pkg/constructs/deployable/README.md)
- Check [Migration Guide](migration-guide.md)
