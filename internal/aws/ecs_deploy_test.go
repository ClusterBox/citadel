package aws

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

const testRepo = "111111111111.dkr.ecr.us-east-1.amazonaws.com/legolas-dev-repo"

type fakeECSDeployAPI struct {
	services     []ecstypes.Service
	taskDef      *ecstypes.TaskDefinition
	tags         []ecstypes.Tag
	describeTDIn *ecs.DescribeTaskDefinitionInput
	registered   *ecs.RegisterTaskDefinitionInput
	updated      *ecs.UpdateServiceInput
}

func (f *fakeECSDeployAPI) DescribeServices(_ context.Context, _ *ecs.DescribeServicesInput, _ ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error) {
	return &ecs.DescribeServicesOutput{Services: f.services}, nil
}

func (f *fakeECSDeployAPI) DescribeTaskDefinition(_ context.Context, in *ecs.DescribeTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error) {
	f.describeTDIn = in
	return &ecs.DescribeTaskDefinitionOutput{TaskDefinition: f.taskDef, Tags: f.tags}, nil
}

func (f *fakeECSDeployAPI) RegisterTaskDefinition(_ context.Context, in *ecs.RegisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error) {
	f.registered = in
	return &ecs.RegisterTaskDefinitionOutput{TaskDefinition: &ecstypes.TaskDefinition{
		Family:            in.Family,
		Revision:          8,
		TaskDefinitionArn: aws.String("arn:aws:ecs:us-east-1:111111111111:task-definition/legolas-dev:8"),
	}}, nil
}

func (f *fakeECSDeployAPI) UpdateService(_ context.Context, in *ecs.UpdateServiceInput, _ ...func(*ecs.Options)) (*ecs.UpdateServiceOutput, error) {
	f.updated = in
	return &ecs.UpdateServiceOutput{Service: &ecstypes.Service{ServiceName: in.Service, DesiredCount: 2, RunningCount: 2}}, nil
}

func legolasTaskDef(images ...string) *ecstypes.TaskDefinition {
	var containers []ecstypes.ContainerDefinition
	for i, img := range images {
		containers = append(containers, ecstypes.ContainerDefinition{Name: aws.String("c" + string(rune('0'+i))), Image: aws.String(img)})
	}
	return &ecstypes.TaskDefinition{
		Family:                  aws.String("legolas-dev"),
		ContainerDefinitions:    containers,
		TaskRoleArn:             aws.String("arn:aws:iam::111111111111:role/task"),
		ExecutionRoleArn:        aws.String("arn:aws:iam::111111111111:role/exec"),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		Cpu:                     aws.String("256"),
		Memory:                  aws.String("512"),
		RuntimePlatform:         &ecstypes.RuntimePlatform{CpuArchitecture: ecstypes.CPUArchitectureX8664},
	}
}

func serviceWithTaskDef() []ecstypes.Service {
	return []ecstypes.Service{{TaskDefinition: aws.String("arn:aws:ecs:us-east-1:111111111111:task-definition/legolas-dev:7")}}
}

func TestDeployImage_OnlyServiceRepoContainersChange(t *testing.T) {
	sidecar := "public.ecr.aws/aws-observability/aws-otel-collector:latest"
	pinned := testRepo + "@sha256:0123456789abcdef"
	td := legolasTaskDef(testRepo+":oldsha", sidecar, pinned)
	api := &fakeECSDeployAPI{services: serviceWithTaskDef(), taskDef: td, tags: []ecstypes.Tag{{Key: aws.String("team"), Value: aws.String("core")}}}
	var out bytes.Buffer

	if err := deployImage(context.Background(), api, &out, "legolas-dev-cluster", "legolas-dev-service", testRepo+":newsha"); err != nil {
		t.Fatal(err)
	}

	got := api.registered.ContainerDefinitions
	if aws.ToString(got[0].Image) != testRepo+":newsha" {
		t.Fatalf("service container image = %s", aws.ToString(got[0].Image))
	}
	if aws.ToString(got[1].Image) != sidecar {
		t.Fatalf("sidecar was changed to %s", aws.ToString(got[1].Image))
	}
	if aws.ToString(got[2].Image) != testRepo+":newsha" {
		t.Fatalf("digest-pinned container of the service repo = %s, want the new image", aws.ToString(got[2].Image))
	}
	if aws.ToString(td.ContainerDefinitions[0].Image) != testRepo+":oldsha" {
		t.Fatal("the described task definition was mutated")
	}

	r := api.registered
	if aws.ToString(r.Family) != "legolas-dev" || aws.ToString(r.TaskRoleArn) == "" || aws.ToString(r.ExecutionRoleArn) == "" ||
		r.NetworkMode != ecstypes.NetworkModeAwsvpc || aws.ToString(r.Cpu) != "256" || aws.ToString(r.Memory) != "512" ||
		len(r.RequiresCompatibilities) != 1 || r.RuntimePlatform == nil || len(r.Tags) != 1 {
		t.Fatalf("registerable fields not copied: %+v", r)
	}
	if len(api.describeTDIn.Include) != 1 || api.describeTDIn.Include[0] != ecstypes.TaskDefinitionFieldTags {
		t.Fatal("DescribeTaskDefinition must include tags so they carry over")
	}
	if aws.ToString(api.updated.TaskDefinition) != "arn:aws:ecs:us-east-1:111111111111:task-definition/legolas-dev:8" {
		t.Fatalf("UpdateService task definition = %s", aws.ToString(api.updated.TaskDefinition))
	}
	if aws.ToString(api.updated.Cluster) != "legolas-dev-cluster" || aws.ToString(api.updated.Service) != "legolas-dev-service" {
		t.Fatalf("UpdateService target = %s/%s", aws.ToString(api.updated.Cluster), aws.ToString(api.updated.Service))
	}
	for _, want := range []string{"✅ Registered task definition: legolas-dev:8", "✅ Deployment triggered for service: legolas-dev-service"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestDeployImage_LatestTagMovesToSHA(t *testing.T) {
	api := &fakeECSDeployAPI{services: serviceWithTaskDef(), taskDef: legolasTaskDef(testRepo + ":latest")}
	if err := deployImage(context.Background(), api, &bytes.Buffer{}, "c", "s", testRepo+":abc1234"); err != nil {
		t.Fatal(err)
	}
	if got := aws.ToString(api.registered.ContainerDefinitions[0].Image); got != testRepo+":abc1234" {
		t.Fatalf("image = %s", got)
	}
}

func TestDeployImage_NoMatchingContainerFailsBeforeRegistering(t *testing.T) {
	api := &fakeECSDeployAPI{services: serviceWithTaskDef(), taskDef: legolasTaskDef("nginx:1.27")}
	err := deployImage(context.Background(), api, &bytes.Buffer{}, "c", "s", testRepo+":abc1234")
	if err == nil || !strings.Contains(err.Error(), testRepo) || !strings.Contains(err.Error(), "nginx:1.27") {
		t.Fatalf("error = %v, want one naming the repository and the images found", err)
	}
	if api.registered != nil || api.updated != nil {
		t.Fatal("nothing may be registered or updated when no container matches")
	}
}

func TestDeployImage_ServiceNotFound(t *testing.T) {
	api := &fakeECSDeployAPI{}
	err := deployImage(context.Background(), api, &bytes.Buffer{}, "legolas-dev-cluster", "legolas-dev-service", testRepo+":x")
	if err == nil || !strings.Contains(err.Error(), "legolas-dev-service") {
		t.Fatalf("error = %v", err)
	}
	if api.registered != nil {
		t.Fatal("registered a task definition for a missing service")
	}
}

func TestImageRepository(t *testing.T) {
	cases := map[string]string{
		testRepo + ":abc1234":       testRepo,
		testRepo + "@sha256:0123":   testRepo,
		testRepo:                    testRepo,
		"localhost:5000/app:1.2":    "localhost:5000/app",
		"localhost:5000/app":        "localhost:5000/app",
		"nginx:1.27":                "nginx",
		"public.ecr.aws/x/y:latest": "public.ecr.aws/x/y",
	}
	for in, want := range cases {
		if got := imageRepository(in); got != want {
			t.Errorf("imageRepository(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServiceRunsImage_ExactMatch(t *testing.T) {
	api := &fakeECSDeployAPI{services: serviceWithTaskDef(), taskDef: legolasTaskDef("public.ecr.aws/sidecar:latest", testRepo+":abc1234")}
	ok, err := serviceRunsImage(context.Background(), api, "c", "s", testRepo+":abc1234")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("service runs the image; want true")
	}
}

func TestServiceRunsImage_OlderTagIsFalse(t *testing.T) {
	api := &fakeECSDeployAPI{services: serviceWithTaskDef(), taskDef: legolasTaskDef(testRepo + ":oldsha")}
	ok, err := serviceRunsImage(context.Background(), api, "c", "s", testRepo+":abc1234")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("service is on an older tag; want false")
	}
}

func TestServiceRunsImage_ServiceMissingIsError(t *testing.T) {
	api := &fakeECSDeployAPI{taskDef: legolasTaskDef(testRepo + ":abc1234")}
	_, err := serviceRunsImage(context.Background(), api, "legolas-dev-cluster", "legolas-dev-service", testRepo+":abc1234")
	if err == nil || !strings.Contains(err.Error(), "legolas-dev-service") {
		t.Fatalf("err = %v, want a service-not-found error", err)
	}
}
