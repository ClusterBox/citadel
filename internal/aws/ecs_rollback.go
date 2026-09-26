package aws

import (
	"context"
	"fmt"
	"io"

	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
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

// rollbackService points the service at taskDefARN. A CDK deploy may have
// deregistered that revision (INACTIVE), which ECS will not start new tasks
// from, so an identical copy is registered and used instead.
func rollbackService(ctx context.Context, api ecsDeployAPI, w io.Writer, cluster, service, taskDefARN string) error {
	desc, err := api.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefARN),
		Include:        []ecstypes.TaskDefinitionField{ecstypes.TaskDefinitionFieldTags},
	})
	if err != nil {
		return fmt.Errorf("failed to describe task definition %s: %w", taskDefARN, err)
	}
	if desc.TaskDefinition == nil {
		return fmt.Errorf("task definition %q not found", taskDefARN)
	}
	target := taskDefARN
	if desc.TaskDefinition.Status == ecstypes.TaskDefinitionStatusInactive {
		reg, err := api.RegisterTaskDefinition(ctx, copyTaskDefinition(desc.TaskDefinition, desc.Tags))
		if err != nil {
			return fmt.Errorf("failed to re-register inactive task definition %s: %w", taskDefARN, err)
		}
		if reg.TaskDefinition == nil {
			return fmt.Errorf("register task definition returned no task definition")
		}
		target = aws.ToString(reg.TaskDefinition.TaskDefinitionArn)
		fmt.Fprintf(w, "   %s is inactive; registered a copy as %s:%d\n", taskDefARN,
			aws.ToString(reg.TaskDefinition.Family), reg.TaskDefinition.Revision)
	}

	out, err := api.UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:        aws.String(cluster),
		Service:        aws.String(service),
		TaskDefinition: aws.String(target),
	})
	if err != nil {
		return fmt.Errorf("failed to update service: %w", err)
	}
	name := service
	if out.Service != nil {
		name = aws.ToString(out.Service.ServiceName)
	}
	fmt.Fprintf(w, "✅ Service %s set back to %s\n", name, target)
	return nil
}
