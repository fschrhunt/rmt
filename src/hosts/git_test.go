package hosts

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseGitStatus reads branch, upstream distance and dirty state from porcelain v2.
func TestParseGitStatus(t *testing.T) {
	out := []byte("# branch.oid abcdef0\x00# branch.head main\x00# branch.upstream origin/main\x00# branch.ab +2 -1\x001 .M N... 100644 100644 1000000 abc def M\tfile.txt\x00")
	info := parseGitStatus(out)
	if info.Branch != "main" || info.Ahead != 2 || info.Behind != 1 || !info.Dirty {
		t.Fatalf("got %+v", info)
	}

	clean := []byte("# branch.oid abcdef0\x00# branch.head main\x00")
	if info := parseGitStatus(clean); info.Dirty || info.Ahead != 0 {
		t.Fatalf("got %+v", info)
	}
}

// TestGitActionDir recognizes in-progress operations from git's own files.
func TestGitActionDir(t *testing.T) {
	for _, tc := range []struct {
		name, want string
	}{
		{"rebase-merge/interactive", "rebase-i"},
		{"rebase-merge/head-name", "rebase"},
		{"rebase-apply/applying", "am"},
		{"rebase-apply/next", "rebase"},
		{"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"},
		{"BISECT_LOG", "bisect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if got := gitActionDir(dir); got != "" {
				t.Fatalf("empty git dir: got %q", got)
			}
			path := filepath.Join(dir, tc.name)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if got := gitActionDir(dir); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
