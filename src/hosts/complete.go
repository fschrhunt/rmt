package hosts

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// completionHandler lists matching paths without changing the persistent command shell.
func completionHandler(home string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			Directory string `json:"directory"`
			Prefix    string `json:"prefix"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&request) != nil || !filepath.IsAbs(request.Directory) || len(request.Prefix) > 4096 {
			http.Error(w, "invalid completion", http.StatusBadRequest)
			return
		}
		prefix := request.Prefix
		if prefix == "~" {
			_ = json.NewEncoder(w).Encode([]string{"~/"})
			return
		}
		full := prefix
		if prefix == "~" {
			full = home
		} else if strings.HasPrefix(prefix, "~/") {
			full = filepath.Join(home, prefix[2:])
		} else if !filepath.IsAbs(prefix) {
			full = filepath.Join(request.Directory, prefix)
		}
		base := filepath.Base(full)
		dir := filepath.Dir(full)
		if strings.HasSuffix(prefix, "/") {
			dir = full
			base = ""
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			_ = json.NewEncoder(w).Encode([]string{})
			return
		}
		before := ""
		if i := strings.LastIndex(prefix, "/"); i >= 0 {
			before = prefix[:i+1]
		}
		var matches []string
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), base) || (strings.HasPrefix(entry.Name(), ".") && !strings.HasPrefix(base, ".")) {
				continue
			}
			name := before + entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			matches = append(matches, name)
			if len(matches) >= 200 {
				break
			}
		}
		_ = json.NewEncoder(w).Encode(matches)
	})
}
