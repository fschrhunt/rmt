package hosts

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// privateSocket resolves a socket link and verifies owner-only access.
func privateSocket(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("Codex's native control socket is unavailable")
	}
	info, err := os.Stat(resolved)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return "", errors.New("Codex's native control socket is unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("Codex's native control socket must be private and owned by this user")
	}
	return resolved, nil
}
