package pipeline

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/pkg/config"
)

// Manifest env vars: names of declared secrets (never values) and the SSM
// prefix they live under. The app-side loader joins prefix + "/" + name.
const (
	envKeySecrets   = "CITADEL_SECRETS"
	envKeySSMPrefix = "CITADEL_SSM_PREFIX"
)

// MergedEnv builds the environment map to write to the function: the
// function's existing env (CDK-set keys survive), overlaid with the declared
// env: block (citadel keys win collisions), plus the secret manifest when any
// secrets are declared. changed reports whether merged differs from existing —
// callers skip the UpdateFunctionConfiguration call when it is false.
func MergedEnv(existing, declared map[string]string, secrets []string, ssmPrefix string) (map[string]string, bool) {
	merged := make(map[string]string, len(existing)+len(declared)+2)
	maps.Copy(merged, existing)
	maps.Copy(merged, declared)
	if len(secrets) > 0 {
		merged[envKeySecrets] = strings.Join(secrets, ",")
		merged[envKeySSMPrefix] = ssmPrefix
	}
	return merged, !maps.Equal(existing, merged)
}

// EnvDiff renders the added/changed keys between existing and merged as
// sorted, human-readable lines. Values under secret-looking keys are redacted;
// the CITADEL_* manifest vars are exempt (they hold names, not values). Keys
// only ever get added or changed — MergedEnv starts from existing, so nothing
// is removed.
func EnvDiff(existing, merged map[string]string) []string {
	keys := make([]string, 0, len(merged))
	for k := range merged {
		if existing[k] != merged[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		val := merged[k]
		if looksSecretish(k) {
			val = "[redacted]"
		}
		if _, ok := existing[k]; ok {
			lines = append(lines, fmt.Sprintf("~ %s=%s", k, val))
		} else {
			lines = append(lines, fmt.Sprintf("+ %s=%s", k, val))
		}
	}
	return lines
}

// looksSecretish reports whether an env key name suggests its value is a
// credential. env: values are non-secret by contract, but the merged map also
// carries pre-existing function env we did not vet — redact defensively.
func looksSecretish(key string) bool {
	if key == envKeySecrets || key == envKeySSMPrefix {
		return false
	}
	upper := strings.ToUpper(key)
	for _, marker := range []string{"SECRET", "TOKEN", "PASSWORD", "PASSWD", "PRIVATE", "CREDENTIAL", "API_KEY", "APIKEY"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// syncLambdaConfig applies citadel.yml's env: block and the secret manifest to
// the function via read-merge-write. Runs right after UpdateFunctionCode: it
// first waits for that update to settle (Lambda rejects concurrent updates
// with ResourceConflictException), then merges so CDK-set keys survive —
// UpdateFunctionConfiguration replaces the entire env map. When dryRun is set
// it prints the diff and writes nothing (and skips the settle-wait, since no
// code update was requested).
func syncLambdaConfig(ctx context.Context, lc *aws.LambdaClient, cfg *config.DeployConfig, env string, dryRun bool) error {
	fnName := cfg.ResolveFunctionName(env)
	fmt.Printf("🔧 Syncing function config for %s...\n", fnName)

	if !dryRun {
		if err := lc.WaitForFunctionUpdated(ctx, fnName); err != nil {
			return err
		}
	}

	existing, err := lc.GetFunctionEnv(ctx, fnName)
	if err != nil {
		return err
	}

	merged, changed := MergedEnv(existing, cfg.Env, cfg.Secrets, aws.SecretPrefix(cfg, env))
	if !changed {
		fmt.Printf("   Environment unchanged (%d vars)\n", len(merged))
		return nil
	}

	for _, line := range EnvDiff(existing, merged) {
		fmt.Printf("   %s\n", line)
	}
	if dryRun {
		fmt.Printf("   [dry-run] Would update function environment (%d vars)\n", len(merged))
		return nil
	}
	if err := lc.UpdateFunctionEnv(ctx, fnName, merged); err != nil {
		return err
	}
	fmt.Printf("   Environment updated (%d vars)\n", len(merged))
	return nil
}
