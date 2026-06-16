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

func TestValidate_LambdaRuntimeRequiresEnvironments(t *testing.T) {
	// functionName is now optional for lambda (convention-based); missing
	// environments should still be caught.
	cfg := &DeployConfig{
		Name:    "smaug",
		Region:  "us-east-1",
		Runtime: RuntimeLambda,
		// No Environments — should fail.
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing environments on lambda runtime, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "environment") {
		t.Fatalf("expected error to mention environment, got %q", got)
	}
}

func TestValidate_LambdaRuntimeSkipsContainerFields(t *testing.T) {
	cfg := &DeployConfig{
		Name:    "smaug",
		Region:  "us-east-1",
		Runtime: RuntimeLambda,
		Lambda:  &LambdaConfig{FunctionName: "SmaugFn"},
		Environments: map[string]EnvConfig{
			"dev": {Account: "123456789012"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error for lambda runtime, got %v", err)
	}
}

func TestResolveFunctionName(t *testing.T) {
	// convention default: <name>-<env>
	cfg := &DeployConfig{Name: "smaug"}
	if got := cfg.ResolveFunctionName("dev"); got != "smaug-dev" {
		t.Errorf("convention: got %q, want smaug-dev", got)
	}
	// explicit name wins
	cfg.Lambda = &LambdaConfig{FunctionName: "custom-fn"}
	if got := cfg.ResolveFunctionName("dev"); got != "custom-fn" {
		t.Errorf("explicit: got %q, want custom-fn", got)
	}
	// {env} placeholder substitution
	cfg.Lambda = &LambdaConfig{FunctionName: "smaug-{env}-fn"}
	if got := cfg.ResolveFunctionName("prod"); got != "smaug-prod-fn" {
		t.Errorf("placeholder: got %q, want smaug-prod-fn", got)
	}
}

func TestResolvedName(t *testing.T) {
	cfg := &DeployConfig{Name: "legolas"}
	if got := cfg.ResolvedName("dev"); got != "legolas-dev" {
		t.Errorf("dev: got %q, want legolas-dev", got)
	}
	if got := cfg.ResolvedName("prod"); got != "legolas-prod" {
		t.Errorf("prod: got %q, want legolas-prod", got)
	}
}

func TestValidateLambdaWithoutFunctionName(t *testing.T) {
	cfg := &DeployConfig{
		Name:    "smaug",
		Region:  "us-east-1",
		Runtime: RuntimeLambda,
		Environments: map[string]EnvConfig{
			"dev": {Account: "123456789012"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("lambda without functionName should be valid (convention), got %v", err)
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
