package docker

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
)

// BuildResult holds the result of a Docker build
type BuildResult struct {
	ImageTag string
	ImageURI string
}

// Build builds a Docker image
func (c *Client) Build(ctx context.Context, cfg *config.DeployConfig, contextPath, tag string) (*BuildResult, error) {
	// Create build context tar
	buildContext, err := createTarFromDirectory(contextPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create build context: %w", err)
	}
	defer buildContext.Close()

	// Build options
	opts := types.ImageBuildOptions{
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

// loadDockerignore reads a .dockerignore file and returns the ignore patterns
func loadDockerignore(dir string) []string {
	ignorePath := filepath.Join(dir, ".dockerignore")
	f, err := os.Open(ignorePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// matchesIgnorePattern checks if a relative path matches any dockerignore pattern
func matchesIgnorePattern(relPath string, patterns []string) bool {
	for _, pattern := range patterns {
		// Check exact directory/file name match
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return true
		}
		// Check if any path component matches (e.g. "cdk.out" matches "cdk.out/nested/file")
		parts := strings.Split(relPath, string(filepath.Separator))
		for _, part := range parts {
			if matched, _ := filepath.Match(pattern, part); matched {
				return true
			}
		}
	}
	return false
}

// createTarFromDirectory creates a tar archive from a directory, respecting .dockerignore
func createTarFromDirectory(dir string) (io.ReadCloser, error) {
	r, w := io.Pipe()

	// Always skip .git, load additional patterns from .dockerignore
	ignorePatterns := loadDockerignore(dir)

	go func() {
		tw := tar.NewWriter(w)
		defer tw.Close()
		defer w.Close()

		err := filepath.Walk(dir, func(file string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Always skip .git directory
			if fi.IsDir() && fi.Name() == ".git" {
				return filepath.SkipDir
			}

			// Check .dockerignore patterns
			relPath, err := filepath.Rel(dir, file)
			if err != nil {
				return err
			}

			if relPath != "." && matchesIgnorePattern(relPath, ignorePatterns) {
				if fi.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// Create tar header
			header, err := tar.FileInfoHeader(fi, fi.Name())
			if err != nil {
				return err
			}

			header.Name = relPath

			// Write header
			if err := tw.WriteHeader(header); err != nil {
				return err
			}

			// Write file content if not a directory
			if !fi.IsDir() {
				f, err := os.Open(file)
				if err != nil {
					return err
				}
				defer f.Close()

				if _, err := io.Copy(tw, f); err != nil {
					return err
				}
			}

			return nil
		})

		if err != nil {
			w.CloseWithError(err)
		}
	}()

	return r, nil
}

// Push pushes an image to ECR
func (c *Client) Push(ctx context.Context, ecrClient *ecr.Client, imageTag string) error {
	// Get ECR authorization token
	authToken, err := c.getECRAuthToken(ctx, ecrClient)
	if err != nil {
		return fmt.Errorf("failed to get ECR auth token: %w", err)
	}

	// Create push options
	opts := image.PushOptions{
		RegistryAuth: authToken,
	}

	// Push image
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

	// ECR returns base64("AWS:password")
	// Docker expects base64(json({"username":"AWS","password":"..."}))
	decodedToken, err := base64.StdEncoding.DecodeString(*authData.AuthorizationToken)
	if err != nil {
		return "", fmt.Errorf("failed to decode authorization token: %w", err)
	}

	// Split "AWS:password" into username and password
	parts := strings.SplitN(string(decodedToken), ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid authorization token format")
	}

	authJSON := fmt.Sprintf(`{"username":"%s","password":"%s"}`, parts[0], parts[1])
	return base64.URLEncoding.EncodeToString([]byte(authJSON)), nil
}

// Tag tags a Docker image
func (c *Client) Tag(ctx context.Context, sourceTag, targetTag string) error {
	return c.Docker.ImageTag(ctx, sourceTag, targetTag)
}
