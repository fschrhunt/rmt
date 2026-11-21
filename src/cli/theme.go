package cli

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/fschrhunt/rmt/src/shell"
)

// theme holds the SGR codes and symbols used to draw the local prompt.
type theme struct {
	user, path, gitBranch, gitDirty, gitAction, gitArrow, gitStash string
	execution, promptOK, promptError                               string
	symbol, downArrow, upArrow, stash                              string
}

// stockTheme returns Pure's default appearance.
func stockTheme() theme {
	return theme{
		user: colorUser, path: colorPath, gitBranch: colorGitBranch,
		gitDirty: colorGitDirty, gitAction: colorGitAction,
		gitArrow: colorGitArrow, gitStash: colorGitArrow,
		execution: colorExecution, promptOK: colorPromptOK,
		promptError: colorPromptError,
		symbol:      "❯", downArrow: "⇣", upArrow: "⇡", stash: "≡",
	}
}

// colorCode turns a zsh color name or 256-color number into an SGR sequence.
func colorCode(spec, fallback string) string {
	spec = strings.ToLower(strings.TrimSpace(spec))
	switch spec {
	case "":
		return fallback
	case "default":
		return "\x1b[39m"
	case "black":
		return "\x1b[30m"
	case "red":
		return "\x1b[31m"
	case "green":
		return "\x1b[32m"
	case "yellow":
		return "\x1b[33m"
	case "blue":
		return "\x1b[34m"
	case "magenta":
		return "\x1b[35m"
	case "cyan":
		return "\x1b[36m"
	case "white":
		return "\x1b[37m"
	}
	if n, err := strconv.Atoi(spec); err == nil && n >= 0 && n <= 255 {
		return "\x1b[38;5;" + strconv.Itoa(n) + "m"
	}
	return fallback
}

// safeSymbol accepts short printable symbols and rejects host-provided escape sequences.
func safeSymbol(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 3 {
		return fallback
	}
	for _, r := range value {
		if !unicode.IsPrint(r) {
			return fallback
		}
	}
	return value
}

// themeFromStyle overlays inherited styling on stock defaults.
func themeFromStyle(style shell.Style) theme {
	t := stockTheme()
	t.user = colorCode(style.User, t.user)
	t.path = colorCode(style.Path, t.path)
	t.gitBranch = colorCode(style.GitBranch, t.gitBranch)
	t.gitDirty = colorCode(style.GitDirty, t.gitDirty)
	t.gitAction = colorCode(style.GitAction, t.gitAction)
	t.gitArrow = colorCode(style.GitArrow, t.gitArrow)
	t.gitStash = colorCode(style.GitStash, t.gitStash)
	t.execution = colorCode(style.Execution, t.execution)
	t.promptOK = colorCode(style.PromptOK, t.promptOK)
	t.promptError = colorCode(style.PromptError, t.promptError)
	t.symbol = safeSymbol(style.Symbol, t.symbol)
	t.downArrow = safeSymbol(style.DownArrow, t.downArrow)
	t.upArrow = safeSymbol(style.UpArrow, t.upArrow)
	t.stash = safeSymbol(style.Stash, t.stash)
	return t
}
