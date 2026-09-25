package aws

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
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

func TestRollbackService(t *testing.T) {
	api := &fakeECSDeployAPI{}
	var out bytes.Buffer
	arn := "arn:aws:ecs:us-east-1:111111111111:task-definition/legolas-dev:7"
	if err := rollbackService(context.Background(), api, &out, "legolas-dev-cluster", "legolas-dev-service", arn); err != nil {
		t.Fatal(err)
	}
	if aws.ToString(api.updated.TaskDefinition) != arn || aws.ToString(api.updated.Service) != "legolas-dev-service" {
		t.Fatalf("update = %+v", api.updated)
	}
	if !strings.Contains(out.String(), "set back to "+arn) {
		t.Fatalf("out = %q", out.String())
	}
}
