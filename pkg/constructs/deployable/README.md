# Deployable Service CDK Construct

A CDK construct that reads `citadel.yml` and automatically generates complete ECS Fargate infrastructure.

## The Magic: Zero-Drift Secret Management

**The problem this solves:** Traditionally, you define secrets in two places:
1. Your deployment script (list of secret names)
2. Your CDK code (mapping secrets to ECS task definition)

When you add `INSTAGRAM_APP_ID`, you must update **both** manually. Miss one → deployment fails.

**The solution:** This construct reads `citadel.yml` once and auto-generates the entire secrets map:

```go
// AUTO-GENERATE SECRETS MAP from citadel.yml
secrets := make(map[string]awsecs.Secret)
for _, secretName := range cfg.Secrets {
    param := awsssm.StringParameter_FromSecureStringParameterAttributes(
        stack,
        jsii.String(fmt.Sprintf("Param-%s", secretName)),
        &awsssm.SecureStringParameterAttributes{
            ParameterName: jsii.String(fmt.Sprintf("/%s/%s", cfg.Name, secretName)),
        },
    )
    secrets[secretName] = awsecs.Secret_FromSsmParameter(param)
}
```

**Result:** Write the secret name once in `citadel.yml`, used everywhere. Zero drift. Zero manual sync.

## Usage

### 1. Create `citadel.yml`

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

secrets:
  - DATABASE_URL
  - API_KEY
  - JWT_SECRET
```

### 2. Create CDK App

```go
package main

import (
    "github.com/ClusterBox/citadel/pkg/constructs/deployable"
    "github.com/aws/aws-cdk-go/awscdk/v2"
    "github.com/aws/jsii-runtime-go"
)

func main() {
    defer jsii.Close()
    
    app := awscdk.NewApp(nil)
    
    env := app.Node().TryGetContext(jsii.String("env")).(string)
    
    deployable.NewDeployableService(app, "my-api-stack", &deployable.DeployableServiceProps{
        StackProps: awscdk.StackProps{
            StackName: jsii.String("my-api-" + env),
            Env: &awscdk.Environment{
                Region: jsii.String("us-east-1"),
            },
        },
        ConfigPath:  "../citadel.yml",
        Environment: env,
    })
    
    app.Synth(nil)
}
```

### 3. Deploy

```bash
cdk deploy --context env=dev
```

## What It Creates

- **VPC**: Public-only (dev) or private+NAT (prod)
- **ECR Repository**: References existing `{name}-repo`
- **ECS Cluster**: Fargate-enabled with Container Insights (prod only)
- **IAM Roles**: Execution role (with SSM access) + task role
- **CloudWatch Logs**: `/ecs/{name}` with 1-week retention
- **Task Definition**: Auto-generated secrets from `citadel.yml`
- **Fargate Service**: ALB + health checks + circuit breaker
- **CloudFront**: Optional HTTPS termination

## Environment-Specific Behavior

### Dev
- Public subnets only (no NAT Gateway → $32/month savings)
- Container Insights disabled
- Fargate Spot enabled (70% cost savings)
- Public IP assignment

### Prod
- Private subnets with NAT Gateway
- Enhanced Container Insights
- On-demand Fargate (reliability)
- No public IPs

## Adding a New Secret

**Before (manual, error-prone):**
1. Add to `deploy.sh` secret list
2. Add to `cdk.go` secrets map
3. Miss one → broken deployment

**After (automatic, zero-drift):**
1. Add to `citadel.yml` secrets list
2. Done. ✨

## Advanced Configuration

### Custom VPC Settings

```yaml
vpc:
  max_azs: 3
  nat_gateways: 2  # prod only
```

### CloudFront

```yaml
cloudfront:
  enabled: true
  comment: "My API HTTPS distribution"
```

## Example Projects

See `examples/legolas/` for a real-world production example.
