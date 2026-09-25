package aws

import (
	"context"
	"fmt"
	"io"

	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

// CurrentTaskDefinitionARN returns the task definition the service runs now —
// the rollback snapshot taken before a deploy changes it.
func (ec *ECSClient) CurrentTaskDefinitionARN(ctx context.Context, cfg *config.DeployConfig, env string) (string, error) {
	return currentTaskDefinitionARN(ctx, ec.client, resolveCluster(cfg, env), resolveService(cfg, env))
}

func currentTaskDefinitionARN(ctx context.Context, api ecsDeployAPI, cluster, service string) (string, error) {
	desc, err := api.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  aws.String(cluster),
		Services: []string{service},
	})
	if err != nil {
		return "", fmt.Errorf("failed to describe service: %w", err)
	}
	if len(desc.Services) == 0 || desc.Services[0].TaskDefinition == nil {
		return "", fmt.Errorf("ECS service %q not found in cluster %q", service, cluster)
	}
	return aws.ToString(desc.Services[0].TaskDefinition), nil
}

// RollbackService points the service back at taskDefARN (a previous revision).
func (ec *ECSClient) RollbackService(ctx context.Context, w io.Writer, cfg *config.DeployConfig, env, taskDefARN string) error {
	return rollbackService(ctx, ec.client, w, resolveCluster(cfg, env), resolveService(cfg, env), taskDefARN)
}

func rollbackService(ctx context.Context, api ecsDeployAPI, w io.Writer, cluster, service, taskDefARN string) error {
	out, err := api.UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:        aws.String(cluster),
		Service:        aws.String(service),
		TaskDefinition: aws.String(taskDefARN),
	})
	if err != nil {
		return fmt.Errorf("failed to update service: %w", err)
	}
	name := service
	if out.Service != nil {
		name = aws.ToString(out.Service.ServiceName)
	}
	fmt.Fprintf(w, "✅ Service %s set back to %s\n", name, taskDefARN)
	return nil
}
