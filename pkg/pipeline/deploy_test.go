package pipeline

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ClusterBox/citadel/pkg/config"
)

func TestDeployOptionsOut_DefaultsToStdout(t *testing.T) {
	if (&DeployOptions{}).out() != os.Stdout {
		t.Fatal("nil Out must default to os.Stdout")
	}
	var buf bytes.Buffer
	if (&DeployOptions{Out: &buf}).out() != &buf {
		t.Fatal("Out not used")
	}
}

func TestDeployCDK_DryRunWritesToGivenWriter(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "cdk"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	opts := &DeployOptions{ConfigPath: filepath.Join(dir, "citadel.yml"), Environment: "dev", DryRun: true}
	if err := deployCDK(context.Background(), &buf, &config.DeployConfig{Name: "demo"}, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "[dry-run] Would run: cdk deploy --context env=dev") {
		t.Fatalf("output = %q", buf.String())
	}
}
