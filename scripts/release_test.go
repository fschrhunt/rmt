package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// releaseFixture creates an isolated main branch, local origin, and fake GitHub CLI.
type releaseFixture struct {
	dir, calls string
	env        []string
}

// git executes Git only inside the fixture repository.
func (f *releaseFixture) git(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = f.dir, f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newReleaseFixture installs the release helper without touching the real checkout or GitHub.
func newReleaseFixture(t *testing.T, changelog string) *releaseFixture {
	t.Helper()
	root := t.TempDir()
	repo, remote, bin := filepath.Join(root, "repo"), filepath.Join(root, "origin.git"), filepath.Join(root, "bin")
	for _, dir := range []string{repo, bin} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f := &releaseFixture{dir: repo, calls: filepath.Join(root, "calls")}
	f.env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "TEST_GH_CALLS="+f.calls)
	gh := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TEST_GH_CALLS\"\ncase \"$1 $2\" in 'run list') printf '%s\\n' \"${TEST_CI:-completed success}\" ;; 'pr create'|'workflow run') : ;; *) exit 1 ;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0755); err != nil {
		t.Fatal(err)
	}
	f.git(t, "init", "--bare", remote)
	f.git(t, "init", "-b", "main")
	f.git(t, "config", "user.name", "Release test")
	f.git(t, "config", "user.email", "test@example.invalid")
	f.git(t, "config", "commit.gpgsign", "false")
	f.git(t, "config", "core.hooksPath", "/dev/null")
	f.git(t, "remote", "add", "origin", remote)
	if err := os.Mkdir(filepath.Join(repo, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("release.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "release.sh"), script, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "CHANGELOG.md"), []byte(changelog), 0644); err != nil {
		t.Fatal(err)
	}
	f.git(t, "add", ".")
	f.git(t, "commit", "-m", "initial")
	f.git(t, "push", "-u", "origin", "main")
	return f
}

// release runs the helper with a mocked gh command and captures its output.
func (f *releaseFixture) release(tag string, env ...string) (string, error) {
	cmd := exec.Command("sh", "scripts/release.sh", tag)
	cmd.Dir, cmd.Env = f.dir, append(f.env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestReleasePreparesPR verifies the first invocation leaves main intact and pushes only notes.
func TestReleasePreparesPR(t *testing.T) {
	f := newReleaseFixture(t, "# Changelog\n\n## Unreleased\n\n- A change.\n")
	before := f.git(t, "rev-parse", "HEAD")
	if out, err := f.release("v1.2.3"); err != nil {
		t.Fatalf("release preparation: %v\n%s", err, out)
	}
	if f.git(t, "rev-parse", "HEAD") != before || f.git(t, "status", "--porcelain") != "" {
		t.Fatal("release preparation changed the main checkout")
	}
	notes := f.git(t, "show", "origin/release/v1.2.3:CHANGELOG.md")
	if !strings.Contains(notes, "## Unreleased\n\n## v1.2.3 - ") || !strings.Contains(notes, "- A change.") {
		t.Fatalf("incorrect release notes: %s", notes)
	}
	calls, err := os.ReadFile(f.calls)
	if err != nil || !strings.Contains(string(calls), "pr create") || strings.Contains(string(calls), "workflow run") {
		t.Fatalf("expected a PR and no dispatch: %s, %v", calls, err)
	}
}

// TestReleaseDispatchRequiresCI verifies dispatch pins the exact main commit and refuses failed CI.
func TestReleaseDispatchRequiresCI(t *testing.T) {
	f := newReleaseFixture(t, "# Changelog\n\n## Unreleased\n\n## v1.2.3 - 2026-01-01\n\n- A change.\n")
	sha := f.git(t, "rev-parse", "HEAD")
	if out, err := f.release("v1.2.3", "TEST_CI=completed failure"); err == nil || !strings.Contains(out, "has not passed") {
		t.Fatalf("expected failed CI to block release: %v\n%s", err, out)
	}
	if out, err := f.release("v1.2.3"); err != nil {
		t.Fatalf("release dispatch: %v\n%s", err, out)
	}
	calls, err := os.ReadFile(f.calls)
	if err != nil || !strings.Contains(string(calls), "workflow run release.yml --ref main -f tag=v1.2.3 -f commit="+sha) {
		t.Fatalf("dispatch did not pin the checked commit: %s, %v", calls, err)
	}
	if tags := f.git(t, "tag", "--list"); tags != "" {
		t.Fatalf("helper created a local tag: %s", tags)
	}
}
