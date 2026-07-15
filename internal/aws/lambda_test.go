package aws

import (
	"context"
	"fmt"
	"maps"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

type fakeFunctionConfigAPI struct {
	getOut     *lambda.GetFunctionConfigurationOutput
	getErr     error
	updateSeen *lambda.UpdateFunctionConfigurationInput
	updateErr  error
}

func (f *fakeFunctionConfigAPI) GetFunctionConfiguration(_ context.Context, _ *lambda.GetFunctionConfigurationInput, _ ...func(*lambda.Options)) (*lambda.GetFunctionConfigurationOutput, error) {
	return f.getOut, f.getErr
}

func (f *fakeFunctionConfigAPI) UpdateFunctionConfiguration(_ context.Context, in *lambda.UpdateFunctionConfigurationInput, _ ...func(*lambda.Options)) (*lambda.UpdateFunctionConfigurationOutput, error) {
	f.updateSeen = in
	return &lambda.UpdateFunctionConfigurationOutput{}, f.updateErr
}

func TestGetFunctionEnv_NilEnvironmentReturnsEmptyMap(t *testing.T) {
	api := &fakeFunctionConfigAPI{getOut: &lambda.GetFunctionConfigurationOutput{}}
	got, err := getFunctionEnv(context.Background(), api, "smaug-dev")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected empty non-nil map, got %v", got)
	}
}

func TestGetFunctionEnv_ReturnsVariables(t *testing.T) {
	api := &fakeFunctionConfigAPI{getOut: &lambda.GetFunctionConfigurationOutput{
		Environment: &lambdatypes.EnvironmentResponse{
			Variables: map[string]string{"STAGE": "dev"},
		},
	}}
	got, err := getFunctionEnv(context.Background(), api, "smaug-dev")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !maps.Equal(got, map[string]string{"STAGE": "dev"}) {
		t.Fatalf("got %v", got)
	}
}

func TestGetFunctionEnv_PropagatesError(t *testing.T) {
	api := &fakeFunctionConfigAPI{getErr: fmt.Errorf("boom")}
	if _, err := getFunctionEnv(context.Background(), api, "smaug-dev"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpdateFunctionEnv_SendsMergedMap(t *testing.T) {
	api := &fakeFunctionConfigAPI{}
	merged := map[string]string{"STAGE": "dev", "CITADEL_SSM_PREFIX": "/smaug-dev"}
	if err := updateFunctionEnv(context.Background(), api, "smaug-dev", merged); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if api.updateSeen == nil || api.updateSeen.Environment == nil {
		t.Fatal("expected UpdateFunctionConfiguration to receive an Environment")
	}
	if !maps.Equal(api.updateSeen.Environment.Variables, merged) {
		t.Fatalf("sent %v, want %v", api.updateSeen.Environment.Variables, merged)
	}
	if api.updateSeen.FunctionName == nil || *api.updateSeen.FunctionName != "smaug-dev" {
		t.Fatal("expected function name smaug-dev")
	}
}
