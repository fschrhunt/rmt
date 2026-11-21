package hosts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fschrhunt/rmt/src/shell"
)

// gitHandler reports the repository state shown by the local prompt.
func gitHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		dir := r.URL.Query().Get("directory")
		if !filepath.IsAbs(dir) || strings.IndexByte(dir, 0) >= 0 || len(dir) > 4096 {
			http.Error(w, "invalid directory", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gitStatus(ctx, filepath.Clean(dir)))
	})
}

// gitStatus runs bounded git queries so prompt rendering never blocks on a repository.
func gitStatus(ctx context.Context, dir string) shell.Git {
	out, err := runGit(ctx, dir, "-c", "core.fsmonitor=false", "status", "--porcelain=v2", "--branch", "--untracked-files=normal", "-z")
	if err != nil {
		return shell.Git{}
	}
	info := parseGitStatus(out)
	if info.Branch == "(detached)" {
		if oid, err := runGit(ctx, dir, "rev-parse", "--short", "HEAD"); err == nil {
			info.Branch = strings.TrimSpace(string(oid))
		}
	}
	if info.Branch == "" {
		return shell.Git{}
	}
	info.Action = gitAction(ctx, dir)
	if out, err := runGit(ctx, dir, "rev-list", "--walk-reflogs", "--count", "refs/stash"); err == nil {
		info.Stash, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
	return info
}

// parseGitStatus extracts the branch, upstream distance and dirty marker from porcelain v2 output.
func parseGitStatus(out []byte) shell.Git {
	var info shell.Git
	for record := range bytes.SplitSeq(out, []byte{0}) {
		switch {
		case bytes.HasPrefix(record, []byte("# branch.head ")):
			info.Branch = string(record[len("# branch.head "):])
		case bytes.HasPrefix(record, []byte("# branch.ab ")):
			_, _ = fmt.Sscanf(string(record[len("# branch.ab "):]), "+%d -%d", &info.Ahead, &info.Behind)
		case len(record) > 0 && record[0] != '#':
			info.Dirty = true
			// Headers precede entries; rename entries can contain a second path.
			return info
		}
	}
	return info
}

// gitAction names an in-progress merge, rebase or similar operation.
func gitAction(ctx context.Context, dir string) string {
	out, err := runGit(ctx, dir, "rev-parse", "--git-dir")
	if err != nil {
		return ""
	}
	gitDir := strings.TrimSuffix(string(out), "\n")
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	return gitActionDir(gitDir)
}

// gitActionDir identifies an operation from the files git keeps in its directory.
func gitActionDir(gitDir string) string {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil
	}
	switch {
	case exists("rebase-merge/interactive"):
		return "rebase-i"
	case exists("rebase-merge"):
		return "rebase"
	case exists("rebase-apply/applying"):
		return "am"
	case exists("rebase-apply"):
		return "rebase"
	case exists("MERGE_HEAD"):
		return "merge"
	case exists("CHERRY_PICK_HEAD"):
		return "cherry-pick"
	case exists("REVERT_HEAD"):
		return "revert"
	case exists("BISECT_LOG"):
		return "bisect"
	}
	return ""
}

// runGit runs one read-only git command with prompts and optional locks disabled.
func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "LC_ALL=C")
	var out limitedOutput
	cmd.Stdout = &out
	cmd.WaitDelay = 100 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if out.overflow {
		return nil, errors.New("git metadata output is too large")
	}
	return out.data.Bytes(), nil
}
