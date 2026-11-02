package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestAgentSupervisionIsIndependent keeps a healthy backend alive while another restarts.
func TestAgentSupervisionIsIndependent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	crashes := filepath.Join(dir, "crashes")
	healthy := filepath.Join(dir, "healthy")
	done := make(chan struct{}, 2)
	go func() {
		superviseAgent(ctx, "crashing-test", func() *exec.Cmd {
			c := exec.CommandContext(ctx, "/bin/sh", "-c", `printf x >> "$TEST_PATH"; exit 1`)
			c.Env = append(os.Environ(), "TEST_PATH="+crashes)
			return c
		})
		done <- struct{}{}
	}()
	go func() {
		superviseAgent(ctx, "healthy-test", func() *exec.Cmd {
			c := exec.CommandContext(ctx, "/bin/sh", "-c", `printf x >> "$TEST_PATH"; exec /bin/sleep 10`)
			c.Env = append(os.Environ(), "TEST_PATH="+healthy)
			return c
		})
		done <- struct{}{}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		crashed, _ := os.ReadFile(crashes)
		started, _ := os.ReadFile(healthy)
		if len(crashed) >= 2 && len(started) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	crashed, _ := os.ReadFile(crashes)
	started, _ := os.ReadFile(healthy)
	cancel()
	<-done
	<-done
	if len(crashed) < 2 || len(started) != 1 {
		t.Fatalf("crashing starts %d, healthy starts %d", len(crashed), len(started))
	}
}
