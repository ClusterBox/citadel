package aws

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ecsDeployAPI is the subset of the ECS API DeployImage uses; tests fake it.
type ecsDeployAPI interface {
	DescribeServices(ctx context.Context, in *ecs.DescribeServicesInput, optFns ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error)
	DescribeTaskDefinition(ctx context.Context, in *ecs.DescribeTaskDefinitionInput, optFns ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error)
	RegisterTaskDefinition(ctx context.Context, in *ecs.RegisterTaskDefinitionInput, optFns ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error)
	UpdateService(ctx context.Context, in *ecs.UpdateServiceInput, optFns ...func(*ecs.Options)) (*ecs.UpdateServiceOutput, error)
}

// DeployImage rolls the service onto imageURI. It registers a new revision of
// the service's current task definition, with imageURI on every container that
// uses the same repository, and points the service at it. Forcing a new
// deployment is not enough: the citadel construct pins the task definition to
// an image tag, so a forced deployment would re-run the previous image.
func (ec *ECSClient) DeployImage(ctx context.Context, w io.Writer, cfg *config.DeployConfig, env, imageURI string) error {
	return deployImage(ctx, ec.client, w, resolveCluster(cfg, env), resolveService(cfg, env), imageURI)
}

func deployImage(ctx context.Context, api ecsDeployAPI, w io.Writer, cluster, service, imageURI string) error {
	desc, err := api.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  aws.String(cluster),
		Services: []string{service},
	})
	if err != nil {
		return fmt.Errorf("failed to describe service: %w", err)
	}
	if len(desc.Services) == 0 || desc.Services[0].TaskDefinition == nil {
		return fmt.Errorf("ECS service %q not found in cluster %q", service, cluster)
	}

	current, err := api.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: desc.Services[0].TaskDefinition,
		Include:        []ecstypes.TaskDefinitionField{ecstypes.TaskDefinitionFieldTags},
	})
	if err != nil {
		return fmt.Errorf("failed to describe task definition: %w", err)
	}
	if current.TaskDefinition == nil {
		return fmt.Errorf("task definition %q not found", aws.ToString(desc.Services[0].TaskDefinition))
	}

	in, err := revisionWithImage(current.TaskDefinition, current.Tags, imageURI)
	if err != nil {
		return err
	}
	reg, err := api.RegisterTaskDefinition(ctx, in)
	if err != nil {
		return fmt.Errorf("failed to register task definition: %w", err)
	}
	if reg.TaskDefinition == nil {
		return fmt.Errorf("register task definition returned no task definition")
	}
	fmt.Fprintf(w, "✅ Registered task definition: %s:%d\n", aws.ToString(reg.TaskDefinition.Family), reg.TaskDefinition.Revision)

	out, err := api.UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:        aws.String(cluster),
		Service:        aws.String(service),
		TaskDefinition: reg.TaskDefinition.TaskDefinitionArn,
	})
	if err != nil {
		return fmt.Errorf("failed to update service: %w", err)
	}
	if out.Service == nil {
		return fmt.Errorf("service update returned nil service")
	}

	fmt.Fprintf(w, "✅ Deployment triggered for service: %s\n", aws.ToString(out.Service.ServiceName))
	fmt.Fprintf(w, "   Desired tasks: %d\n", out.Service.DesiredCount)
	fmt.Fprintf(w, "   Running tasks: %d\n", out.Service.RunningCount)
	return nil
}

// revisionWithImage copies every registrable field of td into a
// RegisterTaskDefinitionInput, replacing the image of each container whose
// repository matches imageURI's. td itself is not modified.
func revisionWithImage(td *ecstypes.TaskDefinition, tags []ecstypes.Tag, imageURI string) (*ecs.RegisterTaskDefinitionInput, error) {
	repo := imageRepository(imageURI)
	containers := make([]ecstypes.ContainerDefinition, len(td.ContainerDefinitions))
	copy(containers, td.ContainerDefinitions)

	matched := 0
	var seen []string
	for i := range containers {
		img := aws.ToString(containers[i].Image)
		seen = append(seen, img)
		if imageRepository(img) == repo {
			containers[i].Image = aws.String(imageURI)
			matched++
		}
	}
	if matched == 0 {
		return nil, fmt.Errorf("no container in task definition %s uses repository %s (images: %s)",
			aws.ToString(td.Family), repo, strings.Join(seen, ", "))
	}

	if len(tags) == 0 {
		tags = nil
	}
	return &ecs.RegisterTaskDefinitionInput{
		Family:                  td.Family,
		ContainerDefinitions:    containers,
		TaskRoleArn:             td.TaskRoleArn,
		ExecutionRoleArn:        td.ExecutionRoleArn,
		NetworkMode:             td.NetworkMode,
		RequiresCompatibilities: td.RequiresCompatibilities,
		Cpu:                     td.Cpu,
		Memory:                  td.Memory,
		Volumes:                 td.Volumes,
		PlacementConstraints:    td.PlacementConstraints,
		RuntimePlatform:         td.RuntimePlatform,
		EphemeralStorage:        td.EphemeralStorage,
		PidMode:                 td.PidMode,
		IpcMode:                 td.IpcMode,
		ProxyConfiguration:      td.ProxyConfiguration,
		InferenceAccelerators:   td.InferenceAccelerators,
		EnableFaultInjection:    td.EnableFaultInjection,
		Tags:                    tags,
	}, nil
}

// imageRepository strips an image reference's tag or digest:
// "host/repo:tag" and "host/repo@sha256:…" both become "host/repo". A colon
// before the last slash is a registry port, not a tag.
func imageRepository(image string) string {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	slash := strings.LastIndex(image, "/")
	if c := strings.LastIndex(image, ":"); c > slash {
		return image[:c]
	}
	return image
}
