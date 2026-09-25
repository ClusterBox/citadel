package initcmd

import "testing"

func TestEnvDetectorRegion(t *testing.T) {
	d := NewEnvDetector()

	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	if got := d.Region(); got != "" {
		t.Fatalf("no env: %q, want empty", got)
	}
	t.Setenv("AWS_DEFAULT_REGION", "eu-west-2")
	if got := d.Region(); got != "eu-west-2" {
		t.Fatalf("AWS_DEFAULT_REGION: %q", got)
	}
	t.Setenv("AWS_REGION", "ap-south-1")
	if got := d.Region(); got != "ap-south-1" {
		t.Fatalf("AWS_REGION must win: %q", got)
	}
}
