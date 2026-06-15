package aws

import (
	"context"
	"fmt"

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
