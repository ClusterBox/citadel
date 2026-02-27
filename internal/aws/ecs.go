package aws

import (
	"context"
	"fmt"

	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ECSClient wraps ECS operations
type ECSClient struct {
	client *ecs.Client
}

// NewECSClient creates a new ECS client
func (c *Client) NewECSClient() *ECSClient {
	if c.Config.Region == "" {
		c.Config.Region = "us-east-1"
	}
	
	return &ECSClient{
		client: ecs.NewFromConfig(c.Config),
	}
}

// UpdateService triggers a new deployment for an ECS service
func (ec *ECSClient) UpdateService(ctx context.Context, cfg *config.DeployConfig) error {
	clusterName := fmt.Sprintf("%s-cluster", cfg.Name)
	serviceName := fmt.Sprintf("%s-service", cfg.Name)

	input := &ecs.UpdateServiceInput{
		Cluster:            aws.String(clusterName),
		Service:            aws.String(serviceName),
		ForceNewDeployment: aws.Bool(true),
	}

	output, err := ec.client.UpdateService(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to update service: %w", err)
	}

	if output.Service == nil {
		return fmt.Errorf("service update returned nil service")
	}

	fmt.Printf("✅ Deployment triggered for service: %s\n", *output.Service.ServiceName)
	fmt.Printf("   Desired tasks: %d\n", output.Service.DesiredCount)
	fmt.Printf("   Running tasks: %d\n", output.Service.RunningCount)

	return nil
}

// GetServiceStatus returns the current status of an ECS service
func (ec *ECSClient) GetServiceStatus(ctx context.Context, cfg *config.DeployConfig) (*types.Service, error) {
	clusterName := fmt.Sprintf("%s-cluster", cfg.Name)
	serviceName := fmt.Sprintf("%s-service", cfg.Name)

	input := &ecs.DescribeServicesInput{
		Cluster:  aws.String(clusterName),
		Services: []string{serviceName},
	}

	output, err := ec.client.DescribeServices(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe service: %w", err)
	}

	if len(output.Services) == 0 {
		return nil, fmt.Errorf("service not found")
	}

	return &output.Services[0], nil
}

// WaitForStableService waits for a service to reach a stable state
func (ec *ECSClient) WaitForStableService(ctx context.Context, cfg *config.DeployConfig) error {
	clusterName := fmt.Sprintf("%s-cluster", cfg.Name)
	serviceName := fmt.Sprintf("%s-service", cfg.Name)

	fmt.Printf("⏳ Waiting for service to stabilize...\n")

	waiter := ecs.NewServicesStableWaiter(ec.client)
	
	input := &ecs.DescribeServicesInput{
		Cluster:  aws.String(clusterName),
		Services: []string{serviceName},
	}

	err := waiter.Wait(ctx, input, ecs.DefaultServicesStableWaiterOptions())
	if err != nil {
		return fmt.Errorf("failed waiting for service to stabilize: %w", err)
	}

	fmt.Printf("✅ Service is stable\n")
	return nil
}
