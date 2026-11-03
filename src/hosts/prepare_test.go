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
	"github.com/fschrhunt/rmt/src/shell"
)

// TestPrepareProgramUsesWorktreeHelper selects a remote worktree without starting an agent.
func TestPrepareProgramUsesWorktreeHelper(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZDOTDIR", home)
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, args := range [][]string{{"init", "-q", home}, {"-C", home, "-c", "user.name=rmt test", "-c", "user.email=rmt@example.test", "commit", "--allow-empty", "-qm", "initial"}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git: %s: %v", output, err)
		}
	}
	rc := "opencode() { worktree opencode \"$@\"; }\ncodex() { worktree codex \"$@\"; }\nalias oc=opencode\n"
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(rc), 0600); err != nil {
		t.Fatal(err)
	}
	helper := "#!/bin/sh\nset -e\n[ \"$1\" = --prepare ] || exit 1\nshift\nprogram=$1; shift\n[ \"$1\" = -w ] || exit 1\nshift\nname=$1; shift\ndir=\"$HOME/.$program/worktrees/$name\"\n[ -d \"$dir\" ] || git -C \"$HOME\" worktree add -q -b \"$program/$name\" \"$dir\"\ncd \"$dir\"\nprintf '%s\\0' \"$(pwd -P)\"\n[ \"$#\" -eq 0 ] || printf '%s\\0' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "worktree"), []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	sh, err := newCommandShell(home, zsh)
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	if _, status, err := sh.Run("export RMT_MODEL='hello world'", func([]byte) error { return nil }); err != nil || status != 0 {
		t.Fatalf("environment: %d %v", status, err)
	}
	for _, program := range []string{"opencode", "codex"} {
		got, err := sh.prepareProgram(program+` -w feature --model "$RMT_MODEL"`, program)
		if err != nil {
			t.Fatalf("%s: %v (%s)", program, err, got.Output)
		}
		if got.Directory != filepath.Join(home, "."+program, "worktrees", "feature") || !reflect.DeepEqual(got.Args, []string{"--model", "hello world"}) {
			t.Fatalf("%s: %+v", program, got)
		}
	}
	got, err := sh.prepareProgram(`oc -w feature --model "$RMT_MODEL"`, "opencode")
	if err != nil || got.Directory != filepath.Join(home, ".opencode", "worktrees", "feature") || !reflect.DeepEqual(got.Args, []string{"--model", "hello world"}) {
		t.Fatalf("alias: %+v, %v", got, err)
	}
	got, err = sh.prepareProgram("opencode", "opencode")
	if err != nil || got.Directory != home || len(got.Args) != 0 {
		t.Fatalf("no arguments: %+v, %v", got, err)
	}
}

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
	manager := shellHandler(home, "/bin/sh")
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

// TestPrepareWithoutHelper reports the missing dependency only for worktree launches.
func TestPrepareWithoutHelper(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	sh, err := newCommandShell(t.TempDir(), "/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	if _, err := sh.prepareProgram("codex --help", "codex"); err != nil {
		t.Fatalf("ordinary launch requires a helper: %v", err)
	}
	got, err := sh.prepareProgram("codex -w fox", "codex")
	if err == nil || !strings.Contains(got.Output, "requires a worktree helper") {
		t.Fatalf("missing helper: output %q, error %v", got.Output, err)
	}
}

// TestPrepareRejectsOversizedLaunch keeps malformed helper output from becoming a launch.
func TestPrepareRejectsOversizedLaunch(t *testing.T) {
	bin := t.TempDir()
	helper := "#!/bin/sh\nprintf '/tmp\\0'\nhead -c 70000 /dev/zero\n"
	if err := os.WriteFile(filepath.Join(bin, "worktree"), []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	sh, err := newCommandShell(t.TempDir(), "/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	if _, err := sh.prepareProgram("codex -w example", "codex"); err == nil || !strings.Contains(err.Error(), "invalid program launch") {
		t.Fatalf("expected an invalid launch error, got %v", err)
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

// TestPrepareWithFunctionHelper supports a worktree helper defined in shell startup files.
func TestPrepareWithFunctionHelper(t *testing.T) {
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
			rc := "worktree() { printf '%s\\0' \"$HOME\" --model example; }\n"
			if err := os.WriteFile(filepath.Join(home, "."+name+"rc"), []byte(rc), 0600); err != nil {
				t.Fatal(err)
			}
			sh, err := newCommandShell(home, shellPath)
			if err != nil {
				t.Fatal(err)
			}
			defer sh.Close()
			got, err := sh.prepareProgram("codex -w example", "codex")
			if err != nil || got.Directory != home || !reflect.DeepEqual(got.Args, []string{"--model", "example"}) {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}
