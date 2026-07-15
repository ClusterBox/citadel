package pipeline

import (
	"fmt"
	"maps"
	"sort"
	"strings"
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
