package pipeline

import (
	"maps"
	"strings"
	"testing"
)

func TestMergedEnv_UnionWithCitadelPrecedence(t *testing.T) {
	existing := map[string]string{"STAGE": "dev", "AWS_COGNITO_REGION": "eu-west-1"}
	declared := map[string]string{"AWS_COGNITO_REGION": "us-east-1", "AWS_COGNITO_CLIENT_ID": "abc"}

	merged, changed := MergedEnv(existing, declared, []string{"DATABASE_URL", "PAYSTACK_SECRET_KEY"}, "/smaug-dev")

	if !changed {
		t.Fatal("expected changed=true")
	}
	want := map[string]string{
		"STAGE":                 "dev",       // CDK-set key survives
		"AWS_COGNITO_REGION":    "us-east-1", // citadel key wins the collision
		"AWS_COGNITO_CLIENT_ID": "abc",
		"CITADEL_SECRETS":       "DATABASE_URL,PAYSTACK_SECRET_KEY",
		"CITADEL_SSM_PREFIX":    "/smaug-dev",
	}
	if !maps.Equal(merged, want) {
		t.Fatalf("merged = %v, want %v", merged, want)
	}
	if existing["AWS_COGNITO_REGION"] != "eu-west-1" {
		t.Fatal("MergedEnv must not mutate its input map")
	}
}

func TestMergedEnv_IdenticalMapIsNoOp(t *testing.T) {
	existing := map[string]string{
		"STAGE":              "dev",
		"CITADEL_SECRETS":    "DATABASE_URL",
		"CITADEL_SSM_PREFIX": "/smaug-dev",
	}
	_, changed := MergedEnv(existing, nil, []string{"DATABASE_URL"}, "/smaug-dev")
	if changed {
		t.Fatal("expected changed=false when merged equals existing")
	}
}

func TestMergedEnv_NoSecretsEmitsNoManifest(t *testing.T) {
	merged, _ := MergedEnv(map[string]string{}, map[string]string{"A": "1"}, nil, "/smaug-dev")
	if _, ok := merged["CITADEL_SECRETS"]; ok {
		t.Fatal("no secrets declared: CITADEL_SECRETS must not be set")
	}
	if _, ok := merged["CITADEL_SSM_PREFIX"]; ok {
		t.Fatal("no secrets declared: CITADEL_SSM_PREFIX must not be set")
	}
}

func TestEnvDiff_ShowsAddedAndChangedRedactingSecretish(t *testing.T) {
	existing := map[string]string{"STAGE": "dev", "API_TOKEN": "old"}
	merged := map[string]string{
		"STAGE":              "dev",
		"API_TOKEN":          "new",
		"AWS_COGNITO_REGION": "us-east-1",
		"CITADEL_SECRETS":    "DATABASE_URL",
	}
	out := strings.Join(EnvDiff(existing, merged), "\n")

	if !strings.Contains(out, "AWS_COGNITO_REGION") || !strings.Contains(out, "us-east-1") {
		t.Fatalf("added plain key should show value, got:\n%s", out)
	}
	if strings.Contains(out, "old") || strings.Contains(out, "new") {
		t.Fatalf("secretish API_TOKEN values must be redacted, got:\n%s", out)
	}
	if !strings.Contains(out, "API_TOKEN") {
		t.Fatalf("redacted key should still be listed, got:\n%s", out)
	}
	// The manifest is names-only — never redacted despite containing "SECRETS".
	if !strings.Contains(out, "DATABASE_URL") {
		t.Fatalf("CITADEL_SECRETS value should be visible, got:\n%s", out)
	}
	if strings.Contains(out, "STAGE") {
		t.Fatalf("unchanged keys must not appear in the diff, got:\n%s", out)
	}
}
