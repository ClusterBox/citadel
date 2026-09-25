package aws

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

const taskARN = "arn:aws:ecs:us-east-1:111111111111:task/legolas-dev-cluster/abc123"

type fakeTaskAPI struct {
	fakeECSDeployAPI
	service   ecstypes.Service
	runIn     *ecs.RunTaskInput
	runOut    *ecs.RunTaskOutput
	statuses  []ecstypes.Task // returned by successive DescribeTasks calls; last repeats
	describes int
	stopped   *ecs.StopTaskInput
}

func (f *fakeTaskAPI) DescribeServices(_ context.Context, _ *ecs.DescribeServicesInput, _ ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error) {
	return &ecs.DescribeServicesOutput{Services: []ecstypes.Service{f.service}}, nil
}

func (f *fakeTaskAPI) RunTask(_ context.Context, in *ecs.RunTaskInput, _ ...func(*ecs.Options)) (*ecs.RunTaskOutput, error) {
	f.runIn = in
	if f.runOut != nil {
		return f.runOut, nil
	}
	return &ecs.RunTaskOutput{Tasks: []ecstypes.Task{{TaskArn: aws.String(taskARN)}}}, nil
}

func (f *fakeTaskAPI) DescribeTasks(_ context.Context, _ *ecs.DescribeTasksInput, _ ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error) {
	i := f.describes
	if i >= len(f.statuses) {
		i = len(f.statuses) - 1
	}
	f.describes++
	return &ecs.DescribeTasksOutput{Tasks: []ecstypes.Task{f.statuses[i]}}, nil
}

func (f *fakeTaskAPI) StopTask(_ context.Context, in *ecs.StopTaskInput, _ ...func(*ecs.Options)) (*ecs.StopTaskOutput, error) {
	f.stopped = in
	return &ecs.StopTaskOutput{}, nil
}

type fakeLogs struct {
	lines  []string
	served bool
}

func (f *fakeLogs) GetLogEvents(_ context.Context, in *cloudwatchlogs.GetLogEventsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
	if aws.ToString(in.LogStreamName) != "ecs/app/abc123" || aws.ToString(in.LogGroupName) != "/ecs/legolas-dev" {
		return nil, errors.New("unexpected stream " + aws.ToString(in.LogStreamName))
	}
	if f.served {
		return &cloudwatchlogs.GetLogEventsOutput{NextForwardToken: aws.String("t1")}, nil
	}
	f.served = true
	var ev []cwtypes.OutputLogEvent
	for _, l := range f.lines {
		ev = append(ev, cwtypes.OutputLogEvent{Message: aws.String(l)})
	}
	return &cloudwatchlogs.GetLogEventsOutput{Events: ev, NextForwardToken: aws.String("t1")}, nil
}

func taskTD(image string) *ecstypes.TaskDefinition {
	td := legolasTaskDef(image)
	td.TaskDefinitionArn = aws.String("arn:aws:ecs:us-east-1:111111111111:task-definition/legolas-dev:7")
	td.ContainerDefinitions[0].Name = aws.String("app")
	td.ContainerDefinitions[0].LogConfiguration = &ecstypes.LogConfiguration{
		LogDriver: ecstypes.LogDriverAwslogs,
		Options:   map[string]string{"awslogs-group": "/ecs/legolas-dev", "awslogs-stream-prefix": "ecs"},
	}
	return td
}

func newTaskAPI(image string, statuses ...ecstypes.Task) *fakeTaskAPI {
	return &fakeTaskAPI{
		fakeECSDeployAPI: fakeECSDeployAPI{taskDef: taskTD(image)},
		service: ecstypes.Service{
			TaskDefinition: aws.String("arn:aws:ecs:us-east-1:111111111111:task-definition/legolas-dev:7"),
			NetworkConfiguration: &ecstypes.NetworkConfiguration{AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
				Subnets: []string{"subnet-a"}, SecurityGroups: []string{"sg-1"}, AssignPublicIp: ecstypes.AssignPublicIpDisabled,
			}},
			CapacityProviderStrategy: []ecstypes.CapacityProviderStrategyItem{{CapacityProvider: aws.String("FARGATE_SPOT"), Weight: 1}},
			PlatformVersion:          aws.String("LATEST"),
		},
		statuses: statuses,
	}
}

func stoppedWith(code *int32, reason string) ecstypes.Task {
	return ecstypes.Task{LastStatus: aws.String("STOPPED"), StoppedReason: aws.String(reason),
		Containers: []ecstypes.Container{{Name: aws.String("app"), ExitCode: code}}}
}

func running() ecstypes.Task { return ecstypes.Task{LastStatus: aws.String("RUNNING")} }

var migrate = TaskSpec{Command: []string{"sh", "-c", "npx prisma migrate deploy"}, Timeout: time.Minute}

func TestRunOneOffTask_RegistersRevisionWhenServiceIsOnOldImage(t *testing.T) {
	api := newTaskAPI(testRepo+":old", running(), stoppedWith(aws.Int32(0), "Essential container exited"))
	logs := &fakeLogs{lines: []string{"applied 2 migrations"}}
	var out bytes.Buffer
	if err := runOneOffTask(context.Background(), api, logs, &out, "legolas-dev-cluster", "legolas-dev-service", testRepo+":new", migrate, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if api.registered == nil || aws.ToString(api.registered.ContainerDefinitions[0].Image) != testRepo+":new" {
		t.Fatal("expected a revision with the new image")
	}
	in := api.runIn
	if aws.ToString(in.TaskDefinition) != "arn:aws:ecs:us-east-1:111111111111:task-definition/legolas-dev:8" {
		t.Fatalf("RunTask used %s", aws.ToString(in.TaskDefinition))
	}
	if aws.ToInt32(in.Count) != 1 || aws.ToString(in.StartedBy) != "citadel" || aws.ToString(in.PlatformVersion) != "LATEST" {
		t.Fatalf("run input = %+v", in)
	}
	if !reflect.DeepEqual(in.NetworkConfiguration, api.service.NetworkConfiguration) || len(in.CapacityProviderStrategy) != 1 || in.LaunchType != "" {
		t.Fatal("network settings / capacity strategy not copied from the service")
	}
	ov := in.Overrides.ContainerOverrides[0]
	if aws.ToString(ov.Name) != "app" || !reflect.DeepEqual(ov.Command, migrate.Command) {
		t.Fatalf("override = %+v", ov)
	}
	if !strings.Contains(out.String(), "applied 2 migrations") {
		t.Fatalf("task logs not streamed:\n%s", out.String())
	}
}

func TestRunOneOffTask_ReusesTaskDefinitionAlreadyOnImage(t *testing.T) {
	api := newTaskAPI(testRepo+":new", stoppedWith(aws.Int32(0), ""))
	if err := runOneOffTask(context.Background(), api, &fakeLogs{}, &bytes.Buffer{}, "c", "s", testRepo+":new", migrate, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if api.registered != nil {
		t.Fatal("registered a revision although the service already runs the image")
	}
	if aws.ToString(api.runIn.TaskDefinition) != "arn:aws:ecs:us-east-1:111111111111:task-definition/legolas-dev:7" {
		t.Fatalf("RunTask used %s", aws.ToString(api.runIn.TaskDefinition))
	}
}

func TestRunOneOffTask_ExplicitContainerAndLaunchType(t *testing.T) {
	api := newTaskAPI(testRepo+":new", stoppedWith(aws.Int32(0), ""))
	api.service.CapacityProviderStrategy = nil
	api.service.LaunchType = ecstypes.LaunchTypeFargate
	spec := migrate
	spec.Container = "app"
	if err := runOneOffTask(context.Background(), api, &fakeLogs{}, &bytes.Buffer{}, "c", "s", testRepo+":new", spec, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if api.runIn.LaunchType != ecstypes.LaunchTypeFargate {
		t.Fatal("launch type not copied")
	}
	spec.Container = "nope"
	if err := runOneOffTask(context.Background(), newTaskAPI(testRepo+":new", stoppedWith(aws.Int32(0), "")), &fakeLogs{}, &bytes.Buffer{}, "c", "s", testRepo+":new", spec, time.Millisecond); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunOneOffTask_NonZeroExit(t *testing.T) {
	api := newTaskAPI(testRepo+":new", stoppedWith(aws.Int32(3), "Essential container exited"))
	err := runOneOffTask(context.Background(), api, &fakeLogs{}, &bytes.Buffer{}, "c", "s", testRepo+":new", migrate, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "task exited with code 3") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunOneOffTask_NeverStarted(t *testing.T) {
	api := newTaskAPI(testRepo+":new", stoppedWith(nil, "CannotPullContainerError: pull access denied"))
	err := runOneOffTask(context.Background(), api, &fakeLogs{}, &bytes.Buffer{}, "c", "s", testRepo+":new", migrate, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "CannotPullContainerError") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunOneOffTask_RunTaskFailure(t *testing.T) {
	api := newTaskAPI(testRepo+":new", running())
	api.runOut = &ecs.RunTaskOutput{Failures: []ecstypes.Failure{{Reason: aws.String("RESOURCE:MEMORY"), Arn: aws.String("x")}}}
	err := runOneOffTask(context.Background(), api, &fakeLogs{}, &bytes.Buffer{}, "c", "s", testRepo+":new", migrate, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "RESOURCE:MEMORY") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunOneOffTask_TimeoutStopsTask(t *testing.T) {
	api := newTaskAPI(testRepo+":new", running())
	spec := migrate
	spec.Timeout = 20 * time.Millisecond
	err := runOneOffTask(context.Background(), api, &fakeLogs{}, &bytes.Buffer{}, "c", "s", testRepo+":new", spec, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out after 20ms") {
		t.Fatalf("err = %v", err)
	}
	if api.stopped == nil || aws.ToString(api.stopped.Task) != taskARN {
		t.Fatal("StopTask was not called for the running task")
	}
}

func TestRunOneOffTask_CancelStopsTask(t *testing.T) {
	api := newTaskAPI(testRepo+":new", running())
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	err := runOneOffTask(ctx, api, &fakeLogs{}, &bytes.Buffer{}, "c", "s", testRepo+":new", migrate, time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if api.stopped == nil {
		t.Fatal("StopTask was not called on cancel")
	}
}
