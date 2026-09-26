package aws

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// TaskSpec describes a one-off task started by a pipeline `task:` step.
type TaskSpec struct {
	Command   []string
	Container string        // "" = the container that uses the service's image repository
	Timeout   time.Duration // 0 = 30m
}

type ecsTaskAPI interface {
	ecsDeployAPI
	RunTask(ctx context.Context, in *ecs.RunTaskInput, optFns ...func(*ecs.Options)) (*ecs.RunTaskOutput, error)
	DescribeTasks(ctx context.Context, in *ecs.DescribeTasksInput, optFns ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error)
	StopTask(ctx context.Context, in *ecs.StopTaskInput, optFns ...func(*ecs.Options)) (*ecs.StopTaskOutput, error)
}

type taskLogsAPI interface {
	GetLogEvents(ctx context.Context, in *cloudwatchlogs.GetLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error)
}

// RunOneOffTask runs spec.Command once inside imageURI on the service's own
// network (e.g. DB migrations that need private access), streams the
// container's logs to w and fails unless the container exits 0. It uses the
// service's task definition, registering a revision with imageURI first when
// the service still runs an older image.
func (ec *ECSClient) RunOneOffTask(ctx context.Context, w io.Writer, logs *LogsClient, cfg *config.DeployConfig, env, imageURI string, spec TaskSpec) error {
	return runOneOffTask(ctx, ec.client, logs.client, w, resolveCluster(cfg, env), resolveService(cfg, env), imageURI, spec, 6*time.Second)
}

func runOneOffTask(ctx context.Context, api ecsTaskAPI, logs taskLogsAPI, w io.Writer, cluster, service, imageURI string, spec TaskSpec, poll time.Duration) error {
	desc, err := api.DescribeServices(ctx, &ecs.DescribeServicesInput{Cluster: aws.String(cluster), Services: []string{service}})
	if err != nil {
		return fmt.Errorf("failed to describe service: %w", err)
	}
	if len(desc.Services) == 0 || desc.Services[0].TaskDefinition == nil {
		return fmt.Errorf("ECS service %q not found in cluster %q", service, cluster)
	}
	svc := desc.Services[0]

	current, err := api.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: svc.TaskDefinition,
		Include:        []ecstypes.TaskDefinitionField{ecstypes.TaskDefinitionFieldTags},
	})
	if err != nil {
		return fmt.Errorf("failed to describe task definition: %w", err)
	}
	if current.TaskDefinition == nil {
		return fmt.Errorf("task definition %q not found", aws.ToString(svc.TaskDefinition))
	}
	td := current.TaskDefinition
	taskDefARN := aws.ToString(svc.TaskDefinition)
	containers := td.ContainerDefinitions

	if !definitionRunsImage(td, imageURI) {
		in, err := revisionWithImage(td, current.Tags, imageURI)
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
		taskDefARN = aws.ToString(reg.TaskDefinition.TaskDefinitionArn)
		containers = in.ContainerDefinitions
		fmt.Fprintf(w, "   Registered task definition: %s:%d\n", aws.ToString(reg.TaskDefinition.Family), reg.TaskDefinition.Revision)
	}

	target, err := taskContainer(containers, spec.Container, imageURI)
	if err != nil {
		return err
	}

	in := &ecs.RunTaskInput{
		Cluster:              aws.String(cluster),
		TaskDefinition:       aws.String(taskDefARN),
		Count:                aws.Int32(1),
		NetworkConfiguration: svc.NetworkConfiguration,
		PlatformVersion:      svc.PlatformVersion,
		StartedBy:            aws.String("citadel"),
		Overrides: &ecstypes.TaskOverride{ContainerOverrides: []ecstypes.ContainerOverride{
			{Name: target.Name, Command: spec.Command},
		}},
	}
	if len(svc.CapacityProviderStrategy) > 0 {
		in.CapacityProviderStrategy = svc.CapacityProviderStrategy
	} else {
		in.LaunchType = svc.LaunchType
	}
	out, err := api.RunTask(ctx, in)
	if err != nil {
		return fmt.Errorf("failed to run task: %w", err)
	}
	if len(out.Failures) > 0 {
		f := out.Failures[0]
		return fmt.Errorf("failed to run task: %s (%s)", aws.ToString(f.Reason), aws.ToString(f.Arn))
	}
	if len(out.Tasks) == 0 || out.Tasks[0].TaskArn == nil {
		return fmt.Errorf("failed to run task: no task returned")
	}
	arn := aws.ToString(out.Tasks[0].TaskArn)
	fmt.Fprintf(w, "   Started task %s\n", taskID(arn))

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	follower := newLogFollower(logs, target, arn)

	for {
		follower.drain(runCtx, w)
		t, err := describeTask(runCtx, api, cluster, arn)
		if err == nil && aws.ToString(t.LastStatus) == "STOPPED" {
			follower.drain(context.WithoutCancel(ctx), w)
			return taskOutcome(t, aws.ToString(target.Name))
		}
		select {
		case <-runCtx.Done():
			stopTask(ctx, api, w, cluster, arn, "citadel: task step timed out or was cancelled")
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("task timed out after %s", timeout)
		case <-time.After(poll):
		}
	}
}

// stopTask asks ECS to stop the task. It runs even when ctx is cancelled
// (Ctrl-C must not leave the task running) but gives up after 30s; a failure
// is reported, not returned, since the step already failed.
func stopTask(ctx context.Context, api ecsTaskAPI, w io.Writer, cluster, arn, reason string) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_, err := api.StopTask(sctx, &ecs.StopTaskInput{
		Cluster: aws.String(cluster), Task: aws.String(arn), Reason: aws.String(reason),
	})
	if err != nil {
		fmt.Fprintf(w, "   ⚠️  could not stop task %s: %v — it may still be running\n", taskID(arn), err)
	}
}

func definitionRunsImage(td *ecstypes.TaskDefinition, imageURI string) bool {
	for _, c := range td.ContainerDefinitions {
		if aws.ToString(c.Image) == imageURI {
			return true
		}
	}
	return false
}

// taskContainer picks the container that receives the command override:
// the named one, or the one using imageURI's repository.
func taskContainer(containers []ecstypes.ContainerDefinition, name, imageURI string) (ecstypes.ContainerDefinition, error) {
	var names []string
	for _, c := range containers {
		names = append(names, aws.ToString(c.Name))
		if name != "" && aws.ToString(c.Name) == name {
			return c, nil
		}
	}
	if name != "" {
		return ecstypes.ContainerDefinition{}, fmt.Errorf("container %q is not in the task definition (containers: %s)", name, strings.Join(names, ", "))
	}
	repo := imageRepository(imageURI)
	for _, c := range containers {
		if imageRepository(aws.ToString(c.Image)) == repo {
			return c, nil
		}
	}
	return ecstypes.ContainerDefinition{}, fmt.Errorf("no container uses repository %s; set container: on the task step", repo)
}

func describeTask(ctx context.Context, api ecsTaskAPI, cluster, arn string) (ecstypes.Task, error) {
	out, err := api.DescribeTasks(ctx, &ecs.DescribeTasksInput{Cluster: aws.String(cluster), Tasks: []string{arn}})
	if err != nil {
		return ecstypes.Task{}, err
	}
	if len(out.Tasks) == 0 {
		return ecstypes.Task{}, fmt.Errorf("task %s not found", arn)
	}
	return out.Tasks[0], nil
}

func taskOutcome(t ecstypes.Task, container string) error {
	for _, c := range t.Containers {
		if aws.ToString(c.Name) != container {
			continue
		}
		if c.ExitCode == nil {
			reason := aws.ToString(t.StoppedReason)
			if r := aws.ToString(c.Reason); r != "" {
				reason += ": " + r
			}
			return fmt.Errorf("task stopped: %s", reason)
		}
		if *c.ExitCode != 0 {
			return fmt.Errorf("task exited with code %d", *c.ExitCode)
		}
		return nil
	}
	return fmt.Errorf("task stopped: %s", aws.ToString(t.StoppedReason))
}

func taskID(arn string) string {
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

// logFollower streams one task container's awslogs stream. A nil follower
// (container without awslogs) does nothing.
type logFollower struct {
	api           taskLogsAPI
	group, stream string
	token         *string
}

func newLogFollower(api taskLogsAPI, c ecstypes.ContainerDefinition, taskARN string) *logFollower {
	lc := c.LogConfiguration
	if lc == nil || lc.LogDriver != ecstypes.LogDriverAwslogs {
		return nil
	}
	group, prefix := lc.Options["awslogs-group"], lc.Options["awslogs-stream-prefix"]
	if group == "" || prefix == "" {
		return nil
	}
	return &logFollower{api: api, group: group, stream: prefix + "/" + aws.ToString(c.Name) + "/" + taskID(taskARN)}
}

// drain prints every event available so far. Errors (e.g. the stream does not
// exist yet) are ignored: logs are best-effort, the exit code decides.
func (f *logFollower) drain(ctx context.Context, w io.Writer) {
	if f == nil {
		return
	}
	for {
		out, err := f.api.GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
			LogGroupName:  aws.String(f.group),
			LogStreamName: aws.String(f.stream),
			StartFromHead: aws.Bool(true),
			NextToken:     f.token,
		})
		if err != nil {
			return
		}
		for _, e := range out.Events {
			fmt.Fprintf(w, "   %s\n", aws.ToString(e.Message))
		}
		same := f.token != nil && out.NextForwardToken != nil && *out.NextForwardToken == *f.token
		f.token = out.NextForwardToken
		if same || len(out.Events) == 0 {
			return
		}
	}
}
