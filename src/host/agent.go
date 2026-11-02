package host

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// superviseAgent keeps one backend running without interrupting other agents.
func superviseAgent(ctx context.Context, name string, command func() *exec.Cmd) {
	delay := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		cmd := command()
		cmd.Stdout = io.Discard
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "rmt: %s stopped (%v); restarting in %s\n", name, err, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if time.Since(started) > 30*time.Second {
			delay = time.Second
		} else if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}
