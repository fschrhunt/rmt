package hosts

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fschrhunt/rmt/src/shell"
)

// TestParseStyle reads only tagged lines from the host's style query.
func TestParseStyle(t *testing.T) {
	out := []byte("some unrelated startup output\x00" +
		"RMT_STYLE\tuser\t242\x00" +
		"RMT_STYLE\tpath\tblue\x00" +
		"RMT_STYLE\tsymbol\tλ\x00" +
		"RMT_STYLE\tdown_arrow\t⇣\x00" +
		"RMT_STYLE\tup_arrow\t⇡\x00" +
		"RMT_STYLE\tstash\t≡\x00" +
		"RMT_STYLE\tgit_branch\tbad\nRMT_STYLE\tprompt_error\tgreen\x00")
	style := parseStyle(out)
	if style.User != "242" || style.Path != "blue" || style.Symbol != "λ" || style.DownArrow != "⇣" || style.UpArrow != "⇡" || style.Stash != "≡" {
		t.Fatalf("got %+v", style)
	}
	if style.GitBranch != "" || style.PromptError != "" {
		t.Fatalf("unexpected values: %+v", style)
	}
}

// TestParseStyleHostFallback uses the host color when no user color is set.
func TestParseStyleHostFallback(t *testing.T) {
	style := parseStyle([]byte("RMT_STYLE\thost\t245\x00"))
	if style.User != "245" {
		t.Fatalf("got %+v", style)
	}
}

// TestStyleHandler imports a partial Pure config without requiring Pure or a real home.
func TestStyleHandler(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	home := t.TempDir()
	t.Setenv("ZDOTDIR", home)
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("print startup\nzstyle ':prompt:pure:path' color 33\nPURE_PROMPT_SYMBOL='>>'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	styleHandler(home, zsh).ServeHTTP(w, httptest.NewRequest("GET", "/style", nil))
	var got shell.Style
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Path != "33" || got.Symbol != ">>" {
		t.Fatalf("style = %+v, error = %v", got, err)
	}
}
