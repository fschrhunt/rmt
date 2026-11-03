package hosts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/fschrhunt/rmt/src/extensions"
	"github.com/fschrhunt/rmt/src/shell"
)

// TestInterruptPreparation stops a worktree helper and leaves the prompt usable.
func TestInterruptPreparation(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	marker := filepath.Join(home, "started")
	helper := "#!/bin/sh\nprintf started > " + quoteShell(marker) + "\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "worktree"), []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	extensionDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(extensionDir, "work.lua"), []byte(`rmt.on("prepare", function(c) local _, _, status = c.run({"worktree"}); if status ~= 0 then error("interrupted") end end)`), 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := extensions.Load(extensionDir)
	if err != nil {
		t.Fatal(err)
	}
	manager := shellHandler(home, "/bin/sh")
	manager.extensions = registry
	defer manager.Close()
	mux := http.NewServeMux()
	mux.Handle("/shell", manager)
	mux.HandleFunc("/program/", manager.serveProgram)
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/shell", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	var ready shell.Event
	if err := wsjson.Read(ctx, conn, &ready); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/program/"+ready.Session+"/codex/prepare", strings.NewReader(`{"command":"codex -w example"}`))
		if err == nil {
			var resp *http.Response
			resp, err = server.Client().Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode != http.StatusBadRequest {
					err = fmt.Errorf("unexpected preparation status %s", resp.Status)
				}
			}
		}
		result <- err
	}()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("worktree helper did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := wsjson.Write(ctx, conn, shell.Request{Type: "interrupt"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("interrupted preparation did not finish")
	}
	if err := wsjson.Write(ctx, conn, shell.Request{Type: "run", Command: "printf alive"}); err != nil {
		t.Fatal(err)
	}
	for {
		var event shell.Event
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "done" {
			if event.Status != 0 {
				t.Fatalf("prompt failed after interrupt: %d", event.Status)
			}
			break
		}
	}
}

// TestPrepareEndpoint returns the shell-selected launch without starting a native service.
func TestPrepareEndpoint(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := shellHandler(home, "/bin/sh")
	defer manager.Close()
	session, _, err := manager.acquire("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/program/"+session.token+"/codex/prepare", strings.NewReader(`{"command":"codex --model example"}`))
	response := httptest.NewRecorder()
	manager.serveProgram(response, request)
	var result preparedProgram
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK || result.Directory != home || !reflect.DeepEqual(result.Args, []string{"--model", "example"}) {
		t.Fatalf("status %d, result %+v, error %v", response.Code, result, err)
	}
}

// TestPrepareWithProgramAlias handles a shell alias without launching it on the host.
func TestPrepareWithProgramAlias(t *testing.T) {
	for _, name := range []string{"bash", "zsh"} {
		t.Run(name, func(t *testing.T) {
			shellPath, err := exec.LookPath(name)
			if err != nil {
				t.Skipf("%s is not installed", name)
			}
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("ZDOTDIR", home)
			if err := os.WriteFile(filepath.Join(home, "."+name+"rc"), []byte("alias opencode='printf wrong'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			sh, err := newCommandShell(home, shellPath)
			if err != nil {
				t.Fatal(err)
			}
			defer sh.Close()
			got, err := sh.prepareProgram("opencode --help", "opencode")
			if err != nil || got.Directory != home || !reflect.DeepEqual(got.Args, []string{"--help"}) {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

// TestPreparePassesWorktreeArguments leaves application flags unchanged without a hook.
func TestPreparePassesWorktreeArguments(t *testing.T) {
	sh, err := newCommandShell(t.TempDir(), "/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	got, err := sh.prepareProgram("codex -w feature", "codex")
	if err != nil || !reflect.DeepEqual(got.Args, []string{"-w", "feature"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// TestRunExtensionUsesShellFunctions checks that host hooks run in the prompt environment.
func TestRunExtensionUsesShellFunctions(t *testing.T) {
	for _, name := range []string{"bash", "zsh"} {
		t.Run(name, func(t *testing.T) {
			shellPath, err := exec.LookPath(name)
			if err != nil {
				t.Skip(err)
			}
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("ZDOTDIR", home)
			if err := os.WriteFile(filepath.Join(home, "."+name+"rc"), []byte("helper() { printf '%s' \"$1\"; }\n"), 0600); err != nil {
				t.Fatal(err)
			}
			sh, err := newCommandShell(home, shellPath)
			if err != nil {
				t.Fatal(err)
			}
			defer sh.Close()
			out, _, status, err := sh.runExtension(home, []string{"helper", "hello world"})
			if err != nil || status != 0 || out != "hello world" {
				t.Fatalf("%q %d %v", out, status, err)
			}
		})
	}
}

// TestPrepareHyphenatedProgram supports extension names on every host shell.
func TestPrepareHyphenatedProgram(t *testing.T) {
	for _, shellName := range []string{"sh", "bash", "zsh"} {
		t.Run(shellName, func(t *testing.T) {
			path, err := exec.LookPath(shellName)
			if err != nil {
				t.Skip(err)
			}
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("ZDOTDIR", home)
			sh, err := newCommandShell(home, path)
			if err != nil {
				t.Fatal(err)
			}
			defer sh.Close()
			got, err := sh.prepareProgram("demo-app --flag value", "demo-app")
			if err != nil || got.Directory != home || !reflect.DeepEqual(got.Args, []string{"--flag", "value"}) {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}
