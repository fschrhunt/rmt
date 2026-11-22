package programs

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// terminalCommand gives a native UI time to restore the terminal before forcing it to exit.
func terminalCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = time.Second
	return cmd
}
