package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

const installTag = "v1.2.3"
const installedFixture = "#!/bin/sh\nprintf executed > \"$RMT_INSTALL_EXEC_MARKER\"\nexit 97\n"
const previousBinary = "previous rmt binary\n"

// installMember describes a tar entry, including deliberately unsafe fixtures.
type installMember struct {
	name, body, link string
	kind             byte
}

// releaseArchive builds fixture bytes without invoking a real program or account.
func releaseArchive(t *testing.T, members []installMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, member := range members {
		h := &tar.Header{Name: member.name, Typeflag: member.kind, Linkname: member.link, Mode: 0755}
		if member.kind == tar.TypeReg {
			h.Size = int64(len(member.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(member.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseMembers matches the two regular files produced by the release workflow.
func releaseMembers() []installMember {
	return []installMember{
		{name: "rmt", body: installedFixture, kind: tar.TypeReg},
		{name: "LICENSE", body: "fixture license\n", kind: tar.TypeReg},
	}
}

// installFixture serves only local release files and tracks effects of an install.
type installFixture struct {
	dir, temp, marker, releases string
	requests                    atomic.Int32
}

// newInstallFixture creates an existing binary and an HTTP release mirror. A nil
// manifest uses the archive's correct checksum, alongside a misleading suffix match.
func newInstallFixture(t *testing.T, archive []byte, manifest func(asset, hash string) string) *installFixture {
	t.Helper()
	if (runtime.GOOS != "darwin" && runtime.GOOS != "linux") || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("installer targets macOS/Linux amd64/arm64")
	}
	root := t.TempDir()
	f := &installFixture{dir: filepath.Join(root, "bin with spaces"), temp: filepath.Join(root, "tmp"), marker: filepath.Join(root, "executed")}
	for _, dir := range []string{f.dir, f.temp} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.dir, "rmt"), []byte(previousBinary), 0755); err != nil {
		t.Fatal(err)
	}
	asset := fmt.Sprintf("rmt_%s_%s_%s.tar.gz", installTag, runtime.GOOS, runtime.GOARCH)
	hash := fmt.Sprintf("%x", sha256.Sum256(archive))
	checksums := strings.Repeat("0", 64) + "  prefix_" + asset + "\n" + hash + "  " + asset + "\n"
	if manifest != nil {
		checksums = manifest(asset, hash)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "/releases/tag/"+installTag, http.StatusFound)
		case "/releases/tag/" + installTag:
			w.WriteHeader(http.StatusOK)
		case "/releases/download/" + installTag + "/" + asset:
			_, _ = w.Write(archive)
		case "/releases/download/" + installTag + "/checksums.txt":
			_, _ = w.Write([]byte(checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	f.releases = server.URL + "/releases"
	return f
}

// run invokes the POSIX installer with isolated temporary files and no network
// dependency beyond the fixture server. Every path checks for leaks or execution.
func (f *installFixture) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{"install.sh", "--dir", f.dir}, args...)...)
	cmd.Env = append(os.Environ(), "RMT_RELEASES="+f.releases, "TMPDIR="+f.temp,
		"RMT_INSTALL_EXEC_MARKER="+f.marker, "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1")
	output, err := cmd.CombinedOutput()
	if _, statErr := os.Lstat(f.marker); !os.IsNotExist(statErr) {
		t.Fatalf("installer executed the fixture binary: %v", statErr)
	}
	for _, dir := range []string{f.temp, f.dir} {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, entry := range entries {
			if dir == f.temp || strings.HasPrefix(entry.Name(), ".rmt-install.") {
				t.Errorf("installer left temporary file %s", filepath.Join(dir, entry.Name()))
			}
		}
	}
	return string(output), err
}

// assertBinary checks bytes and mode so failures cannot silently damage an install.
func assertBinary(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("binary = %q, %v; want %q", got, err, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0755 {
		t.Fatalf("binary mode = %v; want regular 0755", info.Mode())
	}
}

// TestInstallReplacesVerifiedBinary covers explicit tags and the latest redirect,
// exact checksum selection, executable permissions, and never running the binary.
func TestInstallReplacesVerifiedBinary(t *testing.T) {
	for _, latest := range []bool{false, true} {
		t.Run(fmt.Sprintf("latest=%t", latest), func(t *testing.T) {
			f := newInstallFixture(t, releaseArchive(t, releaseMembers()), nil)
			args := []string{"--version", installTag}
			wantRequests := int32(2)
			if latest {
				args = nil
				wantRequests = 4
			}
			if output, err := f.run(t, args...); err != nil {
				t.Fatalf("install failed: %v\n%s", err, output)
			}
			assertBinary(t, filepath.Join(f.dir, "rmt"), installedFixture)
			if got := f.requests.Load(); got != wantRequests {
				t.Fatalf("release requests = %d; want %d", got, wantRequests)
			}
		})
	}
}

// TestInstallRejectsChecksums preserves the old binary when the exact asset has
// a wrong, missing, duplicate, or malformed checksum record.
func TestInstallRejectsChecksums(t *testing.T) {
	cases := map[string]func(string, string) string{
		"mismatch":       func(a, h string) string { return strings.Repeat("0", 64) + "  " + a + "\n" },
		"suffix only":    func(a, h string) string { return h + "  prefix_" + a + "\n" },
		"duplicate":      func(a, h string) string { return strings.Repeat(h+"  "+a+"\n", 2) },
		"malformed hash": func(a, h string) string { return h[:63] + "z  " + a + "\n" },
	}
	for name, manifest := range cases {
		t.Run(name, func(t *testing.T) {
			f := newInstallFixture(t, releaseArchive(t, releaseMembers()), manifest)
			output, err := f.run(t, "--version", installTag)
			if err == nil || !strings.Contains(output, "checksum") {
				t.Fatalf("expected checksum failure: %v\n%s", err, output)
			}
			assertBinary(t, filepath.Join(f.dir, "rmt"), previousBinary)
		})
	}
}

// TestInstallRejectsUnsafeArchives rejects paths and nonregular or ambiguous
// members before any extraction can affect the existing binary or outside files.
func TestInstallRejectsUnsafeArchives(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte(previousBinary), 0755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]installMember{
		"traversal":        {name: "../outside", body: "unsafe", kind: tar.TypeReg},
		"absolute path":    {name: outside, body: "unsafe", kind: tar.TypeReg},
		"extra file":       {name: "unexpected", body: "unsafe", kind: tar.TypeReg},
		"duplicate rmt":    {name: "rmt", body: "unsafe", kind: tar.TypeReg},
		"symlink binary":   {name: "rmt", link: outside, kind: tar.TypeSymlink},
		"hardlink binary":  {name: "rmt", link: "LICENSE", kind: tar.TypeLink},
		"directory binary": {name: "rmt", kind: tar.TypeDir},
		"symlink license":  {name: "LICENSE", link: outside, kind: tar.TypeSymlink},
	}
	for name, member := range cases {
		t.Run(name, func(t *testing.T) {
			members := releaseMembers()
			switch name {
			case "symlink binary", "hardlink binary", "directory binary":
				members[0] = member
			case "symlink license":
				members[1] = member
			default:
				members = append(members, member)
			}
			f := newInstallFixture(t, releaseArchive(t, members), nil)
			output, err := f.run(t, "--version", installTag)
			if err == nil || !strings.Contains(output, "unsafe archive") {
				t.Fatalf("expected unsafe archive failure: %v\n%s", err, output)
			}
			assertBinary(t, filepath.Join(f.dir, "rmt"), previousBinary)
			assertBinary(t, outside, previousBinary)
		})
	}
}

// TestInstallRefusesDestinationSymlinks protects package manager links, including
// dangling links, without changing the link or its target or making a download.
func TestInstallRefusesDestinationSymlinks(t *testing.T) {
	for _, dangling := range []bool{false, true} {
		t.Run(fmt.Sprintf("dangling=%t", dangling), func(t *testing.T) {
			f := newInstallFixture(t, releaseArchive(t, releaseMembers()), nil)
			target := filepath.Join(f.dir, "rmt")
			other := filepath.Join(t.TempDir(), "managed-rmt")
			if !dangling {
				if err := os.WriteFile(other, []byte(previousBinary), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, target); err != nil {
				t.Fatal(err)
			}
			output, err := f.run(t, "--version", installTag)
			if err == nil || !strings.Contains(output, "is a symlink") {
				t.Fatalf("expected symlink refusal: %v\n%s", err, output)
			}
			if link, err := os.Readlink(target); err != nil || link != other {
				t.Fatalf("destination symlink changed: %q, %v", link, err)
			}
			if !dangling {
				assertBinary(t, other, previousBinary)
			} else if _, err := os.Lstat(other); !os.IsNotExist(err) {
				t.Fatalf("dangling target changed: %v", err)
			}
			if f.requests.Load() != 0 {
				t.Fatal("downloaded a release before refusing the symlink")
			}
		})
	}
}

// TestInstallRejectsMalformedTags ensures tag input cannot become a URL path or
// a partial regex match, and matches the release workflow's stable tag contract.
func TestInstallRejectsMalformedTags(t *testing.T) {
	for _, tag := range []string{"", "1.2.3", "v01.2.3", "v1.02.3", "v1.2.03", "v1.2.3-rc1", "v1.2.3/../other", "v1.2.3\nother", "v1.2.3.4"} {
		t.Run(tag, func(t *testing.T) {
			f := newInstallFixture(t, releaseArchive(t, releaseMembers()), nil)
			if output, err := f.run(t, "--version", tag); err == nil {
				t.Fatalf("accepted malformed tag: %s", output)
			}
			assertBinary(t, filepath.Join(f.dir, "rmt"), previousBinary)
			if f.requests.Load() != 0 {
				t.Fatal("malformed tag triggered a release request")
			}
		})
	}
}
