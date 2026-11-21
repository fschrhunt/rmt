package cli

import (
	"testing"

	"github.com/fschrhunt/rmt/src/shell"
)

// TestColorCode maps Pure's color names and numbers and rejects anything else.
func TestColorCode(t *testing.T) {
	for _, tc := range []struct{ spec, want string }{
		{"", "fallback"},
		{"blue", "\x1b[34m"},
		{"Magenta", "\x1b[35m"},
		{"242", "\x1b[38;5;242m"},
		{"999", "fallback"},
		{"\x1b[31m", "fallback"},
	} {
		if got := colorCode(tc.spec, "fallback"); got != tc.want {
			t.Fatalf("%q: got %q, want %q", tc.spec, got, tc.want)
		}
	}
}

// TestSafeSymbol rejects host values that could inject terminal control codes.
func TestSafeSymbol(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"", "❯"},
		{"λ", "λ"},
		{"⇣⇡", "⇣⇡"},
		{"a\nb", "❯"},
		{"\x1b[31m", "❯"},
		{"abcdef", "❯"},
	} {
		if got := safeSymbol(tc.value, "❯"); got != tc.want {
			t.Fatalf("%q: got %q, want %q", tc.value, got, tc.want)
		}
	}
}

// TestThemeFromStyle overlays inherited values while retaining stock defaults.
func TestThemeFromStyle(t *testing.T) {
	tm := themeFromStyle(shell.Style{Path: "33", Symbol: "→", PromptError: "red"})
	if tm.path != "\x1b[38;5;33m" || tm.symbol != "→" || tm.promptError != "\x1b[31m" {
		t.Fatalf("style not applied: %+v", tm)
	}
	if tm.user != colorUser || tm.gitBranch != colorGitBranch || tm.downArrow != "⇣" {
		t.Fatalf("stock defaults lost: %+v", tm)
	}
}
