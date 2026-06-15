package aws

import (
	"testing"

	"github.com/ClusterBox/citadel/pkg/config"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func awslogsContainer(group string) ecstypes.ContainerDefinition {
	return ecstypes.ContainerDefinition{
		LogConfiguration: &ecstypes.LogConfiguration{
			LogDriver: ecstypes.LogDriverAwslogs,
			Options:   map[string]string{"awslogs-group": group},
		},
	}
}

func TestExtractLogGroup_ReturnsAwslogsGroup(t *testing.T) {
	got, err := extractLogGroup([]ecstypes.ContainerDefinition{
		awslogsContainer("/ecs/my-service"),
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "/ecs/my-service" {
		t.Fatalf("expected /ecs/my-service, got %q", got)
	}
}

func TestExtractLogGroup_SkipsContainersWithoutAwslogs(t *testing.T) {
	got, err := extractLogGroup([]ecstypes.ContainerDefinition{
		{LogConfiguration: &ecstypes.LogConfiguration{LogDriver: ecstypes.LogDriverJsonFile}},
		{LogConfiguration: nil},
		awslogsContainer("AhadiInfraStack-AhadiServiceTaskDefwebLogGroup"),
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "AhadiInfraStack-AhadiServiceTaskDefwebLogGroup" {
		t.Fatalf("unexpected group %q", got)
	}
}

func TestExtractLogGroup_NoAwslogsContainerFails(t *testing.T) {
	_, err := extractLogGroup([]ecstypes.ContainerDefinition{
		{LogConfiguration: &ecstypes.LogConfiguration{LogDriver: ecstypes.LogDriverJsonFile}},
	})
	if err == nil {
		t.Fatal("expected error when no container uses awslogs, got nil")
	}
}

func TestExtractLogGroup_AwslogsWithoutGroupOptionFails(t *testing.T) {
	_, err := extractLogGroup([]ecstypes.ContainerDefinition{
		{LogConfiguration: &ecstypes.LogConfiguration{
			LogDriver: ecstypes.LogDriverAwslogs,
			Options:   map[string]string{"awslogs-region": "us-east-1"},
		}},
	})
	if err == nil {
		t.Fatal("expected error when awslogs-group is absent, got nil")
	}
}

func TestExtractLogGroup_EmptyContainersFails(t *testing.T) {
	if _, err := extractLogGroup(nil); err == nil {
		t.Fatal("expected error for empty container list, got nil")
	}
}

func TestResolveCluster_UsesExplicitECSBlock(t *testing.T) {
	cfg := &config.DeployConfig{Name: "ahadi-backend", ECS: &config.ECSConfig{Cluster: "ahadi-cluster"}}
	if got := resolveCluster(cfg); got != "ahadi-cluster" {
		t.Fatalf("expected ahadi-cluster, got %q", got)
	}
}

func TestResolveCluster_FallsBackToConvention(t *testing.T) {
	cfg := &config.DeployConfig{Name: "legolas"}
	if got := resolveCluster(cfg); got != "legolas-cluster" {
		t.Fatalf("expected legolas-cluster, got %q", got)
	}
}

func TestResolveService_UsesExplicitECSBlock(t *testing.T) {
	cfg := &config.DeployConfig{Name: "ahadi-backend", ECS: &config.ECSConfig{Service: "ahadi-backend"}}
	if got := resolveService(cfg); got != "ahadi-backend" {
		t.Fatalf("expected ahadi-backend, got %q", got)
	}
}

func TestResolveService_FallsBackToConvention(t *testing.T) {
	cfg := &config.DeployConfig{Name: "legolas"}
	if got := resolveService(cfg); got != "legolas-service" {
		t.Fatalf("expected legolas-service, got %q", got)
	}
}
