package tests

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// formulaChecksums supplies distinct digests so tests can detect architecture mixups.
func formulaChecksums(tag string) string {
	var sums strings.Builder
	for i, target := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		fmt.Fprintf(&sums, "%s  rmt_%s_%s.tar.gz\n", strings.Repeat(string("aB3f"[i]), 64), tag, target)
	}
	return sums.String()
}

// runFormula invokes only the generator, keeping release fixtures in a temporary directory.
func runFormula(t *testing.T, tag, sums string) (string, string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(path, []byte(sums), 0600); err != nil {
		t.Fatal(err)
	}
	return formulaCommand(tag, path)
}

// formulaCommand captures stdout separately to verify failures never emit partial Ruby.
func formulaCommand(args ...string) (string, string, error) {
	// Go's test cache cannot observe file reads performed by the shell subprocess.
	if _, err := os.ReadFile("../formula.sh"); err != nil {
		return "", "", err
	}
	cmd := exec.Command("sh", append([]string{"../formula.sh"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// TestFormula checks platform selection, official URLs, digests, and a non-executing brew test.
func TestFormula(t *testing.T) {
	for _, tag := range []string{"v0.0.0", "v1.2.3", "v123.45.678"} {
		t.Run(tag, func(t *testing.T) {
			out, stderr, err := runFormula(t, tag, formulaChecksums(tag))
			if err != nil || stderr != "" {
				t.Fatalf("generator: %v, stderr: %s", err, stderr)
			}
			for _, want := range []string{
				"class Rmt < Formula\n",
				`homepage "https://github.com/fschrhunt/rmt"`,
				`version "` + strings.TrimPrefix(tag, "v") + `"`,
				`license "MIT"`,
				"  def install\n    bin.install \"rmt\"\n  end\n",
				"  test do\n    # Running rmt starts user services; inspect the installed binary without executing it.\n    assert_predicate bin/\"rmt\", :executable?\n  end\nend\n",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("formula missing %q", want)
				}
			}
			platforms := regexp.MustCompile(`(?s)  on_(macos|linux) do\n(.*?)\n  end`).FindAllStringSubmatch(out, -1)
			if len(platforms) != 2 {
				t.Fatalf("expected two platform blocks, got %d", len(platforms))
			}
			for i, platform := range platforms {
				osName := []string{"darwin", "linux"}[i]
				if platform[1] != []string{"macos", "linux"}[i] {
					t.Fatalf("unexpected platform %q", platform[1])
				}
				for j, arch := range []string{"arm64", "amd64"} {
					want := fmt.Sprintf("    on_%s do\n      url 'https://github.com/fschrhunt/rmt/releases/download/%s/rmt_%s_%s_%s.tar.gz'\n      sha256 '%s'\n    end",
						[]string{"arm", "intel"}[j], tag, tag, osName, arch, strings.Repeat(string("aB3f"[i*2+j]), 64))
					if !strings.Contains(platform[2], want) {
						t.Errorf("incorrect %s/%s stanza:\n%s", osName, arch, platform[2])
					}
				}
			}
		})
	}
}

// TestFormulaRejectsTags pins stable SemVer validation, including Ruby and multiline injection.
func TestFormulaRejectsTags(t *testing.T) {
	for _, tag := range []string{"", "1.2.3", "v1.2", "v1.2.3.4", "v01.2.3", "v1.02.3", "v1.2.03", "v1.2.3-rc.1", "v1.2.3+build", "v1.2.3\nv4.5.6", "v1.2.3'", "v1.2.3$(false)"} {
		t.Run(tag, func(t *testing.T) {
			out, stderr, err := runFormula(t, tag, formulaChecksums("v1.2.3"))
			if err == nil || out != "" || !strings.Contains(stderr, "formula.sh:") {
				t.Fatalf("expected tag rejection without output: stdout=%q stderr=%q err=%v", out, stderr, err)
			}
		})
	}
}

// TestFormulaRejectsChecksums requires one well-formed entry for each exact release archive.
func TestFormulaRejectsChecksums(t *testing.T) {
	const tag = "v1.2.3"
	valid := formulaChecksums(tag)
	lines := strings.SplitAfter(valid, "\n")
	cases := map[string]string{
		"duplicate":         valid + lines[0],
		"conflicting":       valid + strings.Replace(lines[0], strings.Repeat("a", 64), strings.Repeat("b", 64), 1),
		"short digest":      strings.Replace(valid, strings.Repeat("a", 64), strings.Repeat("a", 63), 1),
		"long digest":       strings.Replace(valid, strings.Repeat("a", 64), strings.Repeat("a", 65), 1),
		"nonhex digest":     strings.Replace(valid, strings.Repeat("a", 64), strings.Repeat("g", 64), 1),
		"extra field":       strings.Replace(valid, lines[0], strings.TrimSpace(lines[0])+" extra\n", 1),
		"filename suffix":   strings.Replace(valid, "darwin_arm64.tar.gz", "darwin_arm64.tar.gz.bak", 1),
		"filename prefix":   strings.Replace(valid, "rmt_v1.2.3_darwin_arm64", "other_rmt_v1.2.3_darwin_arm64", 1),
		"different release": strings.ReplaceAll(valid, tag, "v1.2.4"),
		"empty file":        "",
	}
	for i, target := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		cases["missing "+target] = strings.Replace(valid, lines[i], "", 1)
	}
	for name, sums := range cases {
		t.Run(name, func(t *testing.T) {
			out, stderr, err := runFormula(t, tag, sums)
			if err == nil || out != "" || !strings.Contains(stderr, "invalid or duplicate checksum") {
				t.Fatalf("expected checksum rejection without output: stdout=%q stderr=%q err=%v", out, stderr, err)
			}
		})
	}
}

// TestFormulaRejectsArguments verifies usage errors and unreadable checksum paths fail clearly.
func TestFormulaRejectsArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"v1.2.3"}, {"v1.2.3", "unused", "extra"}, {"v1.2.3", filepath.Join(t.TempDir(), "missing")}, {"v1.2.3", t.TempDir()}} {
		out, stderr, err := formulaCommand(args...)
		if err == nil || out != "" || stderr == "" {
			t.Errorf("expected argument rejection for %q: stdout=%q stderr=%q err=%v", args, out, stderr, err)
		}
	}
}
