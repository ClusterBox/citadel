package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ECSClient wraps ECS operations
type ECSClient struct {
	client *ecs.Client
}

// NewECSClient creates a new ECS client
func (c *Client) NewECSClient() *ECSClient {
	if c.Config.Region == "" {
		c.Config.Region = "us-east-1"
	}
	return &ECSClient{
		client: ecs.NewFromConfig(c.Config),
	}
}

// resolveCluster returns the ECS cluster name for a project in a given env. It
// uses the explicit ecs.cluster from citadel.yml when set, otherwise falls back
// to the env-namespaced "<name>-<env>-cluster" convention (see
// DeployConfig.ResolvedName) used by Citadel-deployed services. The explicit
// override wins regardless of env, for adopting non-Citadel resources.
func resolveCluster(cfg *config.DeployConfig, env string) string {
	if cfg.ECS != nil && cfg.ECS.Cluster != "" {
		return cfg.ECS.Cluster
	}
	return fmt.Sprintf("%s-cluster", cfg.ResolvedName(env))
}

// resolveService returns the ECS service name for a project in a given env. It
// uses the explicit ecs.service from citadel.yml when set, otherwise falls back
// to the env-namespaced "<name>-<env>-service" convention. The explicit
// override wins regardless of env.
func resolveService(cfg *config.DeployConfig, env string) string {
	if cfg.ECS != nil && cfg.ECS.Service != "" {
		return cfg.ECS.Service
	}
	return fmt.Sprintf("%s-service", cfg.ResolvedName(env))
}

// UpdateService triggers a new deployment for an ECS service
func (ec *ECSClient) UpdateService(ctx context.Context, cfg *config.DeployConfig, env string) error {
	input := &ecs.UpdateServiceInput{
		Cluster:            aws.String(resolveCluster(cfg, env)),
		Service:            aws.String(resolveService(cfg, env)),
		ForceNewDeployment: true,
	}

	output, err := ec.client.UpdateService(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to update service: %w", err)
	}

	if output.Service == nil {
		return fmt.Errorf("service update returned nil service")
	}

	fmt.Printf("✅ Deployment triggered for service: %s\n", *output.Service.ServiceName)
	fmt.Printf("   Desired tasks: %d\n", output.Service.DesiredCount)
	fmt.Printf("   Running tasks: %d\n", output.Service.RunningCount)

	return nil
}

// GetServiceStatus returns the current status of an ECS service
func (ec *ECSClient) GetServiceStatus(ctx context.Context, cfg *config.DeployConfig, env string) error {
	input := &ecs.DescribeServicesInput{
		Cluster:  aws.String(resolveCluster(cfg, env)),
		Services: []string{resolveService(cfg, env)},
	}

	output, err := ec.client.DescribeServices(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to describe service: %w", err)
	}

	if len(output.Services) == 0 {
		return fmt.Errorf("service not found")
	}

	service := output.Services[0]
	fmt.Printf("   Service: %s\n", *service.ServiceName)
	fmt.Printf("   Status: %s\n", *service.Status)
	fmt.Printf("   Desired: %d | Running: %d | Pending: %d\n",
		service.DesiredCount,
		service.RunningCount,
		service.PendingCount)

	return nil
}

// WaitForStableService waits for a service to reach a stable state
func (ec *ECSClient) WaitForStableService(ctx context.Context, cfg *config.DeployConfig, env string) error {
	fmt.Printf("⏳ Waiting for service to stabilize...\n")

	waiter := ecs.NewServicesStableWaiter(ec.client)

	input := &ecs.DescribeServicesInput{
		Cluster:  aws.String(resolveCluster(cfg, env)),
		Services: []string{resolveService(cfg, env)},
	}

	maxDuration := 10 * time.Minute
	err := waiter.Wait(ctx, input, maxDuration)
	if err != nil {
		return fmt.Errorf("failed waiting for service to stabilize: %w", err)
	}

	fmt.Printf("✅ Service is stable\n")
	return nil
}

// DiscoverLogGroup resolves the CloudWatch log group an ECS service writes to
// by inspecting its task definition's awslogs log driver. This works for any
// running service regardless of whether Citadel deployed it.
func (ec *ECSClient) DiscoverLogGroup(ctx context.Context, cfg *config.DeployConfig, env string) (string, error) {
	cluster := resolveCluster(cfg, env)
	service := resolveService(cfg, env)

	descOut, err := ec.client.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  aws.String(cluster),
		Services: []string{service},
	})
	if err != nil {
		return "", fmt.Errorf("failed to describe service %q in cluster %q: %w", service, cluster, err)
	}
	if len(descOut.Services) == 0 || descOut.Services[0].TaskDefinition == nil {
		return "", fmt.Errorf("ECS service %q not found in cluster %q; set the ecs: block in citadel.yml to point at the right cluster/service", service, cluster)
	}

	taskDefArn := descOut.Services[0].TaskDefinition
	tdOut, err := ec.client.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: taskDefArn,
	})
	if err != nil {
		return "", fmt.Errorf("failed to describe task definition %q: %w", *taskDefArn, err)
	}
	if tdOut.TaskDefinition == nil {
		return "", fmt.Errorf("task definition %q not found", *taskDefArn)
	}

	group, err := extractLogGroup(tdOut.TaskDefinition.ContainerDefinitions)
	if err != nil {
		return "", fmt.Errorf("could not determine log group for service %q: %w", service, err)
	}
	return group, nil
}

// extractLogGroup returns the awslogs-group value from the first container
// that uses the awslogs log driver. It returns an error when no container
// declares an awslogs log group.
func extractLogGroup(containers []ecstypes.ContainerDefinition) (string, error) {
	for _, c := range containers {
		lc := c.LogConfiguration
		if lc == nil || lc.LogDriver != ecstypes.LogDriverAwslogs {
			continue
		}
		if group := lc.Options["awslogs-group"]; group != "" {
			return group, nil
		}
	}
	return "", fmt.Errorf("no container uses the awslogs log driver")
}
