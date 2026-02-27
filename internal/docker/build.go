package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/pkg/archive"
)

// BuildResult holds the result of a Docker build
type BuildResult struct {
	ImageTag string
	ImageURI string
}

// Build builds a Docker image
func (c *Client) Build(ctx context.Context, cfg *config.DeployConfig, contextPath, tag string) (*BuildResult, error) {
	// Create build context tar
	buildContext, err := archive.TarWithOptions(contextPath, &archive.TarOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create build context: %w", err)
	}
	defer buildContext.Close()

	// Build options
	opts := image.BuildOptions{
		Tags:       []string{tag},
		Dockerfile: "Dockerfile",
		Remove:     true,
	}

	// Build image
	resp, err := c.Docker.ImageBuild(ctx, buildContext, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to build image: %w", err)
	}
	defer resp.Body.Close()

	// Stream build output
	_, err = io.Copy(os.Stdout, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read build output: %w", err)
	}

	return &BuildResult{
		ImageTag: tag,
	}, nil
}

// Push pushes an image to ECR
func (c *Client) Push(ctx context.Context, ecrClient *ecr.Client, imageTag string) error {
	// Get ECR authorization token
	authToken, err := c.getECRAuthToken(ctx, ecrClient)
	if err != nil {
		return fmt.Errorf("failed to get ECR auth token: %w", err)
	}

	// Push image
	opts := image.PushOptions{
		RegistryAuth: authToken,
	}

	resp, err := c.Docker.ImagePush(ctx, imageTag, opts)
	if err != nil {
		return fmt.Errorf("failed to push image: %w", err)
	}
	defer resp.Close()

	// Stream push output
	_, err = io.Copy(os.Stdout, resp)
	if err != nil {
		return fmt.Errorf("failed to read push output: %w", err)
	}

	return nil
}

// getECRAuthToken gets an authorization token for ECR
func (c *Client) getECRAuthToken(ctx context.Context, ecrClient *ecr.Client) (string, error) {
	output, err := ecrClient.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return "", fmt.Errorf("failed to get authorization token: %w", err)
	}

	if len(output.AuthorizationData) == 0 {
		return "", fmt.Errorf("no authorization data returned")
	}

	authData := output.AuthorizationData[0]
	if authData.AuthorizationToken == nil {
		return "", fmt.Errorf("authorization token is nil")
	}

	// Decode the base64 token
	decodedToken, err := base64.StdEncoding.DecodeString(*authData.AuthorizationToken)
	if err != nil {
		return "", fmt.Errorf("failed to decode authorization token: %w", err)
	}

	// Create registry auth config
	authConfig := registry.AuthConfig{
		Username: "AWS",
		Password: string(decodedToken),
	}

	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		return "", fmt.Errorf("failed to encode auth config: %w", err)
	}

	return base64.URLEncoding.EncodeToString(encodedJSON), nil
}

// Tag tags a Docker image
func (c *Client) Tag(ctx context.Context, sourceTag, targetTag string) error {
	return c.Docker.ImageTag(ctx, sourceTag, targetTag)
}
