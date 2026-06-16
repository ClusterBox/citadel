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
	// An explicit ecs.cluster override wins regardless of env (adopting
	// non-Citadel resources): no env suffix is applied.
	cfg := &config.DeployConfig{Name: "ahadi-backend", ECS: &config.ECSConfig{Cluster: "ahadi-cluster"}}
	if got := resolveCluster(cfg, "dev"); got != "ahadi-cluster" {
		t.Fatalf("expected ahadi-cluster, got %q", got)
	}
}

func TestResolveCluster_FallsBackToEnvNamespacedConvention(t *testing.T) {
	cfg := &config.DeployConfig{Name: "legolas"}
	if got := resolveCluster(cfg, "dev"); got != "legolas-dev-cluster" {
		t.Fatalf("expected legolas-dev-cluster, got %q", got)
	}
	if got := resolveCluster(cfg, "prod"); got != "legolas-prod-cluster" {
		t.Fatalf("expected legolas-prod-cluster, got %q", got)
	}
}

func TestResolveService_UsesExplicitECSBlock(t *testing.T) {
	cfg := &config.DeployConfig{Name: "ahadi-backend", ECS: &config.ECSConfig{Service: "ahadi-backend"}}
	if got := resolveService(cfg, "dev"); got != "ahadi-backend" {
		t.Fatalf("expected ahadi-backend, got %q", got)
	}
}

func TestResolveService_FallsBackToEnvNamespacedConvention(t *testing.T) {
	cfg := &config.DeployConfig{Name: "legolas"}
	if got := resolveService(cfg, "dev"); got != "legolas-dev-service" {
		t.Fatalf("expected legolas-dev-service, got %q", got)
	}
	if got := resolveService(cfg, "prod"); got != "legolas-prod-service" {
		t.Fatalf("expected legolas-prod-service, got %q", got)
	}
}
