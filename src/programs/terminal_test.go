package programs

import (
	"bufio"
	"context"
	"errors"
	"testing"
	"time"
)

// TestTerminalCommandAllowsCleanup checks that cancellation delivers a catchable signal.
func TestTerminalCommandAllowsCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	cmd := terminalCommand(ctx, "/bin/sh", "-c", `trap 'echo cleanup; exit 0' TERM
echo ready
while :; do /bin/sleep 0.01; done`)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(out)
	ready := lines.Scan() && lines.Text() == "ready"
	cancel()
	cleaned := lines.Scan() && lines.Text() == "cleanup"
	err = cmd.Wait()
	if !ready || !cleaned || !errors.Is(err, context.Canceled) {
		t.Fatalf("ready = %v, cleaned = %v, wait = %v", ready, cleaned, err)
	}
}
