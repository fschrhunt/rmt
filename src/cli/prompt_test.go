package cli

import (
	"testing"
	"time"

	"github.com/fschrhunt/rmt/src/shell"
)

// TestPromptLabel displays the remote location relative to its home directory.
func TestPromptLabel(t *testing.T) {
	for _, tc := range []struct{ dir, want string }{{"/home/me", "mini ~: "}, {"/home/me/dev", "mini ~/dev: "}, {"/tmp", "mini /tmp: "}} {
		if got := promptLabel("mini", "/home/me", tc.dir); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

// TestHumanDuration formats slow commands the way Pure does.
func TestHumanDuration(t *testing.T) {
	for _, tc := range []struct {
		duration time.Duration
		want     string
	}{
		{6 * time.Second, "6s"},
		{65 * time.Second, "1m 5s"},
		{3661 * time.Second, "1h 1m 1s"},
		{90061 * time.Second, "1d 1h 1m 1s"},
	} {
		if got := humanDuration(tc.duration); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

// TestRenderPreprompt reproduces Pure's first line and symbols.
func TestRenderPreprompt(t *testing.T) {
	info := shell.Git{Branch: "main", Dirty: true, Ahead: 2, Behind: 1, Stash: 1}
	got := renderPreprompt("mini", "fschr", "mini", "/home/fschr", "/home/fschr/dev/rmt", info, true, 65*time.Second, stockTheme(), false)
	want := "fschr@mini ~/dev/rmt main* ⇣⇡ ≡ 1m 5s"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	got = renderPreprompt("mini", "fschr", "mini", "/home/fschr", "/home/fschr", shell.Git{}, false, time.Second, stockTheme(), false)
	if got != "fschr@mini ~" {
		t.Fatalf("got %q", got)
	}
}

// TestRenderPrompt keeps the symbol red only after a failed command.
func TestRenderPrompt(t *testing.T) {
	if got, want := renderPrompt(0, stockTheme(), false), "❯ "; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := renderPrompt(0, stockTheme(), true); got != colorPromptOK+"❯"+colorReset+" " {
		t.Fatalf("got %q", got)
	}
	if got := renderPrompt(1, stockTheme(), true); got != colorPromptError+"❯"+colorReset+" " {
		t.Fatalf("got %q", got)
	}
}

// TestRemoteCompleterSkipsShellSyntax avoids remote calls for unfinished syntax.
func TestRemoteCompleterSkipsShellSyntax(t *testing.T) {
	caller := remoteCompleter(func(prefix string) ([]string, error) {
		t.Fatalf("unexpected query %q", prefix)
		return nil, nil
	})
	if candidates, length := caller.Do([]rune("echo $HO"), 8); candidates != nil || length != 0 {
		t.Fatalf("got %v, %d", candidates, length)
	}
}

// TestRemoteCompleterAtCursor inserts a suffix without consuming the rest of the line.
func TestRemoteCompleterAtCursor(t *testing.T) {
	caller := remoteCompleter(func(prefix string) ([]string, error) {
		if prefix != "é" {
			t.Fatalf("prefix = %q", prefix)
		}
		return []string{"é file"}, nil
	})
	got, offset := caller.Do([]rune("ls é | wc"), 4)
	if len(got) != 1 || string(got[0]) != `\ file` || offset != 1 {
		t.Fatalf("candidates = %q, offset = %d", got, offset)
	}
}

// TestRemoteCompleterContinuesEscapedPath checks a second completion inside a directory with spaces.
func TestRemoteCompleterContinuesEscapedPath(t *testing.T) {
	caller := remoteCompleter(func(prefix string) ([]string, error) {
		if prefix != "~/my dir/fi" {
			t.Fatalf("prefix = %q", prefix)
		}
		return []string{"~/my dir/file name"}, nil
	})
	line := []rune(`cat ~/my\ dir/fi`)
	candidates, offset := caller.Do(line, len(line))
	if len(candidates) != 1 || string(candidates[0]) != `le\ name` || offset != len(`~/my\ dir/fi`) {
		t.Fatalf("candidates = %q, offset = %d", candidates, offset)
	}
}

// TestPromptText keeps host paths from changing terminal state or adding lines.
func TestPromptText(t *testing.T) {
	if got := promptLabel("mini", "/home/me", "/home/me/a\nb\x1b"); got != "mini ~/a�b�: " {
		t.Fatalf("got %q", got)
	}
}

// TestEscapeCompletion prevents filenames from becoming shell syntax.
func TestEscapeCompletion(t *testing.T) {
	if got := escapeCompletion("~/a b;touch"); got != `~/a\ b\;touch` {
		t.Fatal(got)
	}
	if escapeCompletion("a\nb") != "" {
		t.Fatal("control character accepted")
	}
}
