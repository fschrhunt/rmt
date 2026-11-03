package programs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckReportsUnsupportedFlags(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "opencode")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'old client'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if err := Check("opencode", false); err == nil || !strings.Contains(err.Error(), "--server") {
		t.Fatalf("unexpected result: %v", err)
	}
}
