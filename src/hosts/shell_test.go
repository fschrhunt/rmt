package hosts

import (
	"context"
	"net/http/httptest"

	"bytes"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/fschrhunt/rmt/src/shell"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCommandShellPreservesState runs normal commands in the host's persistent shell.
func TestCommandShellPreservesState(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "project"), 0700); err != nil {
		t.Fatal(err)
	}
	sh, err := newCommandShell(home, "")
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	var output bytes.Buffer
	run := func(line string) (string, int) {
		t.Helper()
		output.Reset()
		dir, status, err := sh.Run(line, func(data []byte) error { _, err := output.Write(data); return err })
		if err != nil {
			t.Fatal(err)
		}
		return dir, status
	}
	if _, status := run("cd project && export RMT_TEST_VALUE='hello world'"); status != 0 {
		t.Fatal(status)
	}
	dir, status := run(`printf '%s\n' "$RMT_TEST_VALUE" | tr a-z A-Z`)
	if status != 0 || filepath.Base(dir) != "project" || output.String() != "HELLO WORLD\n" {
		t.Fatalf("dir %q status %d output %q", dir, status, output.String())
	}
	if _, status = run("false"); status == 0 {
		t.Fatal("expected nonzero status")
	}
	if _, status = run(`printf "%s" "$?"`); status != 0 || output.String() != "1" {
		t.Fatalf("previous status: %d %q", status, output.String())
	}
	if _, status = run(`printf "%s" "it's ok"`); status != 0 || output.String() != "it's ok" {
		t.Fatalf("quote: %d %q", status, output.String())
	}
}

// TestCommandShellInterruptStopsCommand keeps the host shell usable after Ctrl-C.
func TestCommandShellInterruptStopsCommand(t *testing.T) {
	sh, err := newCommandShell(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	started := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, _, err := sh.Run("printf started; sleep 30", func(data []byte) error {
			if strings.Contains(string(data), "started") {
				select {
				case started <- struct{}{}:
				default:
				}
			}
			return nil
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("command did not start")
	}
	time.Sleep(100 * time.Millisecond)
	if err := sh.Interrupt(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("command did not stop")
	}
	var output bytes.Buffer
	_, status, err := sh.Run("printf alive", func(data []byte) error { _, err := output.Write(data); return err })
	if err != nil || status != 0 || output.String() != "alive" {
		t.Fatalf("shell unusable after interrupt: %q %d %v", output.String(), status, err)
	}
}

// TestShellHandlerKeepsRemoteDirectory verifies the command stream over WebSocket.
func TestShellHandlerKeepsRemoteDirectory(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "project"), 0700); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(shellHandler(home, ""))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	var event shell.Event
	if err := wsjson.Read(ctx, conn, &event); err != nil || event.Type != "ready" {
		t.Fatalf("ready: %+v %v", event, err)
	}
	for _, line := range []string{"cd project", "pwd"} {
		if err := wsjson.Write(ctx, conn, shell.Request{Type: "run", Command: line}); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		for {
			if err := wsjson.Read(ctx, conn, &event); err != nil {
				t.Fatal(err)
			}
			if event.Type == "output" {
				output.Write(event.Data)
			}
			if event.Type == "done" {
				break
			}
			if event.Type == "error" {
				t.Fatal(event.Error)
			}
		}
		if filepath.Base(event.Directory) != "project" {
			t.Fatalf("directory %q", event.Directory)
		}
		if line == "pwd" && !strings.Contains(output.String(), "/project") {
			t.Fatalf("output %q", output.String())
		}
	}
}
