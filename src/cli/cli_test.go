package cli

import "testing"

// TestPromptLabel displays the remote location relative to its home directory.
func TestPromptLabel(t *testing.T) {
	for _, tc := range []struct{ dir, want string }{{"/home/me", "mini ~: "}, {"/home/me/dev", "mini ~/dev: "}, {"/tmp", "mini /tmp: "}} {
		if got := promptLabel("mini", "/home/me", tc.dir); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
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
