package hosts

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fschrhunt/rmt/src/shell"
)

// styleScript reads only Pure's color settings and symbols from the user's zsh config.
const styleScript = `
zmodload -i zsh/zutil 2>/dev/null
_style_source() { source "$1" >/dev/null 2>&1 </dev/null }
_style_source "${ZDOTDIR:-$HOME}/.zshrc"
_style_query() {
  local value=
  if zstyle -s "$1" "$2" value; then
    print -r -- "RMT_STYLE"$'\t'"$3"$'\t'"$value"
  fi
}
_style_query ':prompt:pure:user' color user
_style_query ':prompt:pure:host' color host
_style_query ':prompt:pure:path' color path
_style_query ':prompt:pure:git:branch' color git_branch
_style_query ':prompt:pure:git:dirty' color git_dirty
_style_query ':prompt:pure:git:action' color git_action
_style_query ':prompt:pure:git:arrow' color git_arrow
_style_query ':prompt:pure:git:stash' color git_stash
_style_query ':prompt:pure:execution_time' color execution
_style_query ':prompt:pure:prompt:success' color prompt_ok
_style_query ':prompt:pure:prompt:error' color prompt_error
_style_symbol() {
  [[ -n ${(P)1-} ]] && print -r -- "RMT_STYLE"$'\t'"$2"$'\t'"${(P)1}"
}
_style_symbol PURE_PROMPT_SYMBOL symbol
_style_symbol PURE_GIT_DOWN_ARROW down_arrow
_style_symbol PURE_GIT_UP_ARROW up_arrow
_style_symbol PURE_GIT_STASH_SYMBOL stash
true
`

// styleHandler exposes the user's prompt styling so clients can inherit it.
func styleHandler(home, shellPath string) http.Handler {
	if shellPath == "" {
		shellPath = os.Getenv("SHELL")
	}
	if shellPath == "" || !filepath.IsAbs(shellPath) {
		if found, err := exec.LookPath("zsh"); err == nil {
			shellPath = found
		}
	}
	if filepath.Base(shellPath) != "zsh" {
		shellPath = ""
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if shellPath == "" {
			_ = json.NewEncoder(w).Encode(shell.Style{})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, shellPath, "-f", "-c", styleScript)
		cmd.Env = append(os.Environ(), "HOME="+home, "TERM=dumb")
		out, err := cmd.Output()
		if err != nil && len(out) == 0 {
			_ = json.NewEncoder(w).Encode(shell.Style{})
			return
		}
		_ = json.NewEncoder(w).Encode(parseStyle(out))
	})
}

// parseStyle collects tagged lines printed by styleScript.
func parseStyle(out []byte) shell.Style {
	var style shell.Style
	hostColor := ""
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || parts[0] != "RMT_STYLE" {
			continue
		}
		value := strings.TrimSpace(parts[2])
		switch parts[1] {
		case "user":
			style.User = value
		case "host":
			hostColor = value
		case "path":
			style.Path = value
		case "git_branch":
			style.GitBranch = value
		case "git_dirty":
			style.GitDirty = value
		case "git_action":
			style.GitAction = value
		case "git_arrow":
			style.GitArrow = value
		case "git_stash":
			style.GitStash = value
		case "execution":
			style.Execution = value
		case "prompt_ok":
			style.PromptOK = value
		case "prompt_error":
			style.PromptError = value
		case "symbol":
			style.Symbol = value
		case "down_arrow":
			style.DownArrow = value
		case "up_arrow":
			style.UpArrow = value
		case "stash":
			style.Stash = value
		}
	}
	if style.User == "" {
		style.User = hostColor
	}
	return style
}
