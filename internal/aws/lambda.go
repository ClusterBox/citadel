package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

// LambdaClient wraps the AWS Lambda SDK operations the daemon needs.
type LambdaClient struct {
	client *lambda.Client
}

// NewLambdaClient returns a Lambda client bound to the citadel AWS config.
func (c *Client) NewLambdaClient() *LambdaClient {
	return &LambdaClient{client: lambda.NewFromConfig(c.Config)}
}

// ResolveLogGroup returns "/aws/lambda/<functionName>" after verifying the
// function exists in the configured account/region. We resolve eagerly so a
// typo in citadel.yml fails fast at daemon startup rather than producing
// empty polls forever.
func (lc *LambdaClient) ResolveLogGroup(ctx context.Context, functionName string) (string, error) {
	if functionName == "" {
		return "", fmt.Errorf("lambda function name is empty")
	}
	_, err := lc.client.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String(functionName),
	})
	if err != nil {
		return "", fmt.Errorf("verify lambda %s: %w", functionName, err)
	}
	return "/aws/lambda/" + functionName, nil
}

// UpdateFunctionCode points the Lambda function at a new container image and
// returns once AWS accepts the update request (status is then polled via
// WaitForFunctionUpdated).
func (lc *LambdaClient) UpdateFunctionCode(ctx context.Context, functionName, imageURI string) error {
	if functionName == "" {
		return fmt.Errorf("lambda function name is empty")
	}
	_, err := lc.client.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
		FunctionName: aws.String(functionName),
		ImageUri:     aws.String(imageURI),
	})
	if err != nil {
		return fmt.Errorf("update function code %s: %w", functionName, err)
	}
	return nil
}

// WaitForFunctionUpdated blocks until the function's LastUpdateStatus is
// Successful (or returns an error if it becomes Failed or the timeout elapses).
func (lc *LambdaClient) WaitForFunctionUpdated(ctx context.Context, functionName string) error {
	waiter := lambda.NewFunctionUpdatedV2Waiter(lc.client)
	if err := waiter.Wait(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String(functionName),
	}, 5*time.Minute); err != nil {
		return fmt.Errorf("wait for function %s update: %w", functionName, err)
	}
	return nil
}
