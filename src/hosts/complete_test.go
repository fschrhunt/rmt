package hosts

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestCompletionUsesRemoteDirectory lists matching paths without changing shell state.
func TestCompletionUsesRemoteDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "project"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"directory": dir, "prefix": "pro"})
	w := httptest.NewRecorder()
	completionHandler(dir).ServeHTTP(w, httptest.NewRequest("POST", "/complete", bytes.NewReader(data)))
	var got []string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0] != "project/" {
		t.Fatalf("completion %v: %v", got, err)
	}
}
