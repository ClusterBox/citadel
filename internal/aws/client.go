package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// Client holds AWS service clients
type Client struct {
	Config aws.Config
	SSM    *ssm.Client
	ECR    *ecr.Client
	ECS    *ecs.Client
}

// NewClient creates a new AWS client with the specified region
func NewClient(ctx context.Context, region string) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &Client{
		Config: cfg,
		SSM:    ssm.NewFromConfig(cfg),
		ECR:    ecr.NewFromConfig(cfg),
		ECS:    ecs.NewFromConfig(cfg),
	}, nil
}
