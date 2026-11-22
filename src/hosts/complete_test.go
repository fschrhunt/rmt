package hosts

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestCompletionUsesRemoteDirectory lists matching paths without changing shell state.
func TestCompletionUsesRemoteDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "project"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "pro"} {
		data, _ := json.Marshal(map[string]string{"directory": dir, "prefix": prefix})
		w := httptest.NewRecorder()
		completionHandler(dir).ServeHTTP(w, httptest.NewRequest("POST", "/complete", bytes.NewReader(data)))
		var got []string
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0] != "project/" {
			t.Fatalf("prefix %q: completion %v: %v", prefix, got, err)
		}
	}
}

// TestCompletionMarksSymlinkDirectories checks that completing a directory link allows descending into it.
func TestCompletionMarksSymlinkDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"directory": dir, "prefix": "lin"})
	w := httptest.NewRecorder()
	completionHandler(dir).ServeHTTP(w, httptest.NewRequest("POST", "/complete", bytes.NewReader(data)))
	var got []string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !slices.Equal(got, []string{"linked/"}) {
		t.Fatalf("completion = %v, err = %v", got, err)
	}
}
