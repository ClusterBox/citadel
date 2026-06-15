package config

import (
	"strings"
	"testing"
)

// baseValidConfig returns a DeployConfig that passes Validate() so tests can
// isolate the queues-specific behaviour.
func baseValidConfig() *DeployConfig {
	return &DeployConfig{
		Name:   "demo",
		Region: "us-east-1",
		Container: ContainerConfig{
			Port:   3000,
			CPU:    256,
			Memory: 512,
		},
		Environments: map[string]EnvConfig{
			"dev": {Account: "111111111111"},
		},
		Secrets: []string{"DATABASE_URL"},
	}
}

func TestValidate_NoQueuesBlockIsValid(t *testing.T) {
	cfg := baseValidConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestValidate_EmptyQueueListsAreValid(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Queues = &QueuesConfig{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestValidate_ValidConsumeAndProduceArnsPass(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Queues = &QueuesConfig{
		Consume: []string{"arn:aws:sqs:us-east-1:111111111111:incoming"},
		Produce: []string{"arn:aws:sqs:us-east-1:111111111111:outgoing"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestValidate_MalformedConsumeArnFails(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Queues = &QueuesConfig{
		Consume: []string{"arn:aws:sqs:us-east-1:111111111111:ok", "not-an-arn"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for malformed consume ARN, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "queues.consume[1]") {
		t.Fatalf("expected error to name queues.consume[1], got %q", got)
	}
}

func TestValidate_LambdaRuntimeRequiresFunctionName(t *testing.T) {
	cfg := &DeployConfig{
		Name:    "smaug",
		Region:  "us-east-1",
		Runtime: RuntimeLambda,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing lambda.functionName, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "lambda.functionName") {
		t.Fatalf("expected error to mention lambda.functionName, got %q", got)
	}
}

func TestValidate_LambdaRuntimeSkipsContainerFields(t *testing.T) {
	cfg := &DeployConfig{
		Name:    "smaug",
		Region:  "us-east-1",
		Runtime: RuntimeLambda,
		Lambda:  &LambdaConfig{FunctionName: "SmaugFn"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error for lambda runtime, got %v", err)
	}
}

func TestValidate_UnknownRuntimeFails(t *testing.T) {
	cfg := &DeployConfig{
		Name:    "x",
		Region:  "us-east-1",
		Runtime: "wasm",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for unknown runtime, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "wasm") {
		t.Fatalf("expected error to mention bad runtime, got %q", got)
	}
}

func TestResolvedRuntime_DefaultsToECS(t *testing.T) {
	cfg := &DeployConfig{}
	if got := cfg.ResolvedRuntime(); got != RuntimeECS {
		t.Fatalf("expected ecs default, got %q", got)
	}
}

func TestValidate_MalformedProduceArnFails(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Queues = &QueuesConfig{
		Produce: []string{"arn:aws:s3:::wrong-service"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for malformed produce ARN, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "queues.produce[0]") {
		t.Fatalf("expected error to name queues.produce[0], got %q", got)
	}
}
