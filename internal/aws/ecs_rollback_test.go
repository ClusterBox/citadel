package aws

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestCurrentTaskDefinitionARN(t *testing.T) {
	api := &fakeECSDeployAPI{services: serviceWithTaskDef()}
	got, err := currentTaskDefinitionARN(context.Background(), api, "c", "s")
	if err != nil || got != "arn:aws:ecs:us-east-1:111111111111:task-definition/legolas-dev:7" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := currentTaskDefinitionARN(context.Background(), &fakeECSDeployAPI{}, "c", "legolas-dev-service"); err == nil || !strings.Contains(err.Error(), "legolas-dev-service") {
		t.Fatalf("err = %v", err)
	}
}

func TestRollbackService_ActiveSnapshot(t *testing.T) {
	arn := "arn:aws:ecs:us-east-1:111111111111:task-definition/legolas-dev:7"
	td := legolasTaskDef(testRepo + ":old")
	td.Status = ecstypes.TaskDefinitionStatusActive
	api := &fakeECSDeployAPI{taskDef: td}
	var out bytes.Buffer
	if err := rollbackService(context.Background(), api, &out, "legolas-dev-cluster", "legolas-dev-service", arn); err != nil {
		t.Fatal(err)
	}
	if aws.ToString(api.describeTDIn.TaskDefinition) != arn {
		t.Fatalf("described %s, want the snapshot", aws.ToString(api.describeTDIn.TaskDefinition))
	}
	if api.registered != nil {
		t.Fatal("registered a copy of an ACTIVE task definition")
	}
	if aws.ToString(api.updated.TaskDefinition) != arn || aws.ToString(api.updated.Service) != "legolas-dev-service" {
		t.Fatalf("update = %+v", api.updated)
	}
	if !strings.Contains(out.String(), "set back to "+arn) {
		t.Fatalf("out = %q", out.String())
	}
}

// TestRollbackService_InactiveSnapshotIsReRegistered covers ruling R11: a
// CDK deploy may deregister the snapshot revision, and ECS refuses to start
// new tasks from an INACTIVE one, so rollback registers an identical copy.
func TestRollbackService_InactiveSnapshotIsReRegistered(t *testing.T) {
	arn := "arn:aws:ecs:us-east-1:111111111111:task-definition/legolas-dev:7"
	sidecar := "public.ecr.aws/aws-observability/aws-otel-collector:latest"
	td := legolasTaskDef(testRepo+":old", sidecar)
	td.Status = ecstypes.TaskDefinitionStatusInactive
	api := &fakeECSDeployAPI{taskDef: td, tags: []ecstypes.Tag{{Key: aws.String("team"), Value: aws.String("core")}}}
	var out bytes.Buffer
	if err := rollbackService(context.Background(), api, &out, "legolas-dev-cluster", "legolas-dev-service", arn); err != nil {
		t.Fatal(err)
	}
	r := api.registered
	if r == nil {
		t.Fatal("did not register a copy of the INACTIVE task definition")
	}
	if len(r.ContainerDefinitions) != 2 || aws.ToString(r.ContainerDefinitions[0].Image) != testRepo+":old" ||
		aws.ToString(r.ContainerDefinitions[1].Image) != sidecar {
		t.Fatalf("copy containers = %+v", r.ContainerDefinitions)
	}
	if aws.ToString(r.Family) != "legolas-dev" || aws.ToString(r.Cpu) != "256" || aws.ToString(r.Memory) != "512" ||
		aws.ToString(r.TaskRoleArn) == "" || aws.ToString(r.ExecutionRoleArn) == "" || len(r.Tags) != 1 {
		t.Fatalf("registrable fields not copied: %+v", r)
	}
	newARN := "arn:aws:ecs:us-east-1:111111111111:task-definition/legolas-dev:8"
	if aws.ToString(api.updated.TaskDefinition) != newARN {
		t.Fatalf("UpdateService task definition = %s, want the copy %s", aws.ToString(api.updated.TaskDefinition), newARN)
	}
	if !strings.Contains(out.String(), "is inactive; registered a copy as legolas-dev:8") {
		t.Fatalf("out = %q", out.String())
	}
	if !strings.Contains(out.String(), "set back to "+newARN) {
		t.Fatalf("out = %q", out.String())
	}
}
