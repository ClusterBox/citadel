package initcmd

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ClusterBox/citadel/pkg/config"
)

var update = flag.Bool("update", false, "rewrite golden files")

func renderCases() map[string]Values {
	ecsCustom := validECS()
	ecsCustom.Envs = []string{"staging"}
	ecsCustom.Port = 8080
	ecsCustom.CPU = 512
	ecsCustom.Memory = 1024
	ecsCustom.HealthCheckPath = "/healthz"
	ecsCustom.Secrets = []string{"DATABASE_URL", "JWT_SECRET"}

	lambdaCustom := validLambda()
	lambdaCustom.FunctionName = "smaug-api-{env}"
	lambdaCustom.Secrets = []string{"DATABASE_URL"}

	return map[string]Values{
		"ecs-defaults":    validECS(),
		"ecs-custom":      ecsCustom,
		"lambda-defaults": validLambda(),
		"lambda-custom":   lambdaCustom,
	}
}

func TestRender_Golden(t *testing.T) {
	for name, v := range renderCases() {
		t.Run(name, func(t *testing.T) {
			got, err := Render(v, "vTEST")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run: go test ./internal/initcmd/ -run TestRender_Golden -update)", err)
			}
			if string(got) != string(want) {
				t.Fatalf("render mismatch for %s.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
			}
		})
	}
}

// TestRender_RoundTrip checks that what citadel reads back is what init asked for.
func TestRender_RoundTrip(t *testing.T) {
	for name, v := range renderCases() {
		t.Run(name, func(t *testing.T) {
			out, err := Render(v, "vTEST")
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Parse(out)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Name != v.Name || cfg.Region != v.Region || cfg.ResolvedRuntime() != v.Runtime {
				t.Fatalf("identity = %s/%s/%s", cfg.Name, cfg.Region, cfg.ResolvedRuntime())
			}
			var envs []string
			for e, ec := range cfg.Environments {
				envs = append(envs, e)
				if ec.Account != v.Account {
					t.Fatalf("env %s account = %q", e, ec.Account)
				}
			}
			sort.Strings(envs)
			want := append([]string(nil), v.Envs...)
			sort.Strings(want)
			if !reflect.DeepEqual(envs, want) {
				t.Fatalf("envs = %v, want %v", envs, want)
			}
			if len(v.Secrets) > 0 && !reflect.DeepEqual(cfg.Secrets, v.Secrets) {
				t.Fatalf("secrets = %v, want %v", cfg.Secrets, v.Secrets)
			}
			if v.Runtime == config.RuntimeECS {
				c := cfg.Container
				if c.Port != v.Port || c.CPU != v.CPU || c.Memory != v.Memory || c.HealthCheckPath != v.HealthCheckPath {
					t.Fatalf("container = %+v", c)
				}
			}
			if v.Runtime == config.RuntimeLambda {
				// Unset functionName must fall back to the <name>-<env> convention.
				want := v.Name + "-dev"
				if v.FunctionName != "" {
					want = strings.ReplaceAll(v.FunctionName, "{env}", "dev")
				}
				if got := cfg.ResolveFunctionName("dev"); got != want {
					t.Fatalf("function name for dev = %q, want %q", got, want)
				}
			}
		})
	}
}

// TestRender_YAMLReservedWordsRoundTrip guards against name/env keys being
// emitted unquoted: a directory (or env) literally called "null", "true" or
// "yes" would otherwise render a YAML keyword instead of a string, and the
// rendered citadel.yml would fail Render's own config.Parse check ("this is
// a citadel bug") or silently round-trip to the wrong Go type.
func TestRender_YAMLReservedWordsRoundTrip(t *testing.T) {
	for _, word := range []string{"null", "true", "yes"} {
		t.Run(word, func(t *testing.T) {
			v := validECS()
			v.Name = word
			v.Envs = []string{word}

			out, err := Render(v, "vTEST")
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Parse(out)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Name != word {
				t.Fatalf("name = %q, want %q", cfg.Name, word)
			}
			if _, ok := cfg.Environments[word]; !ok {
				t.Fatalf("environments missing key %q: %v", word, cfg.Environments)
			}
		})
	}
}

func TestRender_RejectsInvalidValues(t *testing.T) {
	v := validECS()
	v.Account = "123"
	if _, err := Render(v, "vTEST"); err == nil {
		t.Fatal("Render accepted an invalid account")
	}
	v = validECS()
	v.Runtime = "ec2"
	if _, err := Render(v, "vTEST"); err == nil {
		t.Fatal("Render accepted an unsupported runtime")
	}
}
