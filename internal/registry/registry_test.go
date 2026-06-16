package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ClusterBox/citadel/pkg/config"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolve_ECSAndLambdaTogether(t *testing.T) {
	dir := t.TempDir()
	aragorn := filepath.Join(dir, "aragorn")
	smaug := filepath.Join(dir, "smaug")
	writeFile(t, filepath.Join(aragorn, "citadel.yml"), `
name: aragorn
region: us-east-1
container:
  port: 3000
  cpu: 256
  memory: 512
environments:
  dev:
    account: "111111111111"
secrets:
  - DATABASE_URL
`)
	writeFile(t, filepath.Join(smaug, "citadel.yml"), `
name: smaug
region: us-east-1
runtime: lambda
lambda:
  functionName: SmaugFn
`)
	reg := filepath.Join(dir, "registry.yml")
	writeFile(t, reg, "services:\n"+
		"  - repo: "+aragorn+"\n    env: dev\n"+
		"  - repo: "+smaug+"\n    env: dev\n")

	services, errs := Resolve(reg)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
	if services[0].ID != "aragorn-dev" || services[0].Runtime != config.RuntimeECS {
		t.Fatalf("aragorn entry wrong: %+v", services[0])
	}
	if services[1].ID != "smaug-dev" || services[1].Runtime != config.RuntimeLambda || services[1].LambdaFunction != "SmaugFn" {
		t.Fatalf("smaug entry wrong: %+v", services[1])
	}
}

func TestResolve_MissingCitadelYmlIsCollectedError(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "registry.yml")
	writeFile(t, reg, "services:\n  - repo: "+dir+"/ghost\n    env: dev\n")

	services, errs := Resolve(reg)
	if len(services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(services))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d (%v)", len(errs), errs)
	}
}

func TestResolve_LambdaWithoutFunctionNameUsesConvention(t *testing.T) {
	// A lambda service with no functionName must NOT be dropped: the function
	// name is derived via the "<name>-<env>" convention (matching the deploy
	// CLI), so convention-based services like smaug still get log ingestion.
	dir := t.TempDir()
	repo := filepath.Join(dir, "smaug")
	writeFile(t, filepath.Join(repo, "citadel.yml"), `
name: smaug
region: us-east-1
runtime: lambda
`)
	reg := filepath.Join(dir, "registry.yml")
	writeFile(t, reg, "services:\n  - repo: "+repo+"\n    env: dev\n")

	services, errs := Resolve(reg)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].LambdaFunction != "smaug-dev" {
		t.Fatalf("expected LambdaFunction smaug-dev (convention), got %q", services[0].LambdaFunction)
	}
}

func TestLoadFile_EmptyFileIsValid(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "registry.yml")
	writeFile(t, reg, "")
	f, err := LoadFile(reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(f.Services))
	}
}
