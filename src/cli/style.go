package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// style is the local Pure settings; empty fields keep stock styling.
type style struct {
	User, Path, GitBranch, GitDirty, GitAction, GitArrow, GitStash string
	Execution, PromptOK, PromptError                               string
	Symbol, DownArrow, UpArrow, Stash                              string
}

// styleScript reads only Pure's color settings and symbols from the user's zsh config.
const styleScript = `
zmodload -i zsh/zutil 2>/dev/null
_style_source() { source "$1" >/dev/null 2>&1 </dev/null }
_style_source "${ZDOTDIR:-$HOME}/.zshrc"
_style_query() {
  local value=
  if zstyle -s "$1" "$2" value; then
    builtin printf '%s\t%s\t%s\0' RMT_STYLE "$3" "$value"
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
  [[ -n ${(P)1-} ]] && builtin printf '%s\t%s\t%s\0' RMT_STYLE "$2" "${(P)1}"
}
_style_symbol PURE_PROMPT_SYMBOL symbol
_style_symbol PURE_GIT_DOWN_ARROW down_arrow
_style_symbol PURE_GIT_UP_ARROW up_arrow
_style_symbol PURE_GIT_STASH_SYMBOL stash
true
`

// localStyle reads the client's zsh settings, falling back when zsh or Pure settings are unavailable.
func localStyle() style {
	zsh := os.Getenv("SHELL")
	if !filepath.IsAbs(zsh) || filepath.Base(zsh) != "zsh" {
		var err error
		zsh, err = exec.LookPath("zsh")
		if err != nil {
			return style{}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return style{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-f", "-c", styleScript)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "TERM=dumb")
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return style{}
	}
	return parseStyle(out)
}

// parseStyle collects short tagged records printed by styleScript.
func parseStyle(out []byte) style {
	var style style
	hostColor := ""
	for record := range bytes.SplitSeq(out, []byte{0}) {
		parts := strings.SplitN(string(record), "\t", 3)
		if len(parts) != 3 || parts[0] != "RMT_STYLE" || len(parts[2]) > 64 || strings.ContainsAny(parts[2], "\t\r\n") {
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
