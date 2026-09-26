package main

import (
	"context"
	"syscall"
	"testing"
	"time"
)

// TestDeployContext_CancelledBySIGTERM covers the first signal: it cancels
// the deploy context (so run: children and task: steps are stopped) instead
// of killing citadel outright.
func TestDeployContext_CancelledBySIGTERM(t *testing.T) {
	ctx, stop := deployContext()
	defer stop()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM did not cancel the deploy context")
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("ctx.Err() = %v", ctx.Err())
	}
}
