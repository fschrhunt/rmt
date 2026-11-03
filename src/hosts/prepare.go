package hosts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// preparedProgram is the host directory and arguments selected for a native client.
type preparedProgram struct {
	Directory string   `json:"directory"`
	Args      []string `json:"args"`
	Output    string   `json:"output,omitempty"`
}

// prepareProgram expands arguments on the host and asks the worktree helper to prepare -w launches.
// The shell function records the launch instead of starting a host-side interface.
func (s *commandShell) prepareProgram(line, name string) (preparedProgram, error) {
	var result preparedProgram
	if name != "opencode" && name != "codex" {
		return result, errors.New("unsupported program")
	}
	dir, err := os.MkdirTemp("", "rmt-launch-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	resultPath := filepath.Join(dir, "launch")
	var output bytes.Buffer
	function := name + `() {
  if [ -e ` + quoteShell(resultPath) + ` ]; then
    printf '%s\n' 'rmt: launch the program once per command' >&2
    return 1
  fi
  __rmt_worktree=false
  for __rmt_arg do
    case "$__rmt_arg" in --) break ;; -w) __rmt_worktree=true ;; esac
  done
  if $__rmt_worktree; then
    if ! command -v worktree >/dev/null 2>&1; then
      printf '%s\n' 'rmt: -w requires a worktree helper supporting --prepare on the host' >&2
      return 127
    fi
    if \worktree --prepare ` + name + ` "$@" > ` + quoteShell(resultPath) + `; then
      return 0
    else
      __rmt_status=$?
      if [ "$__rmt_status" -eq 127 ]; then
        printf '%s\n' 'rmt: update the host worktree helper to a version supporting --prepare' >&2
      fi
      return "$__rmt_status"
    fi
  else
    { command printf '%s\0' "$(pwd -P)"; if [ "$#" -gt 0 ]; then command printf '%s\0' "$@"; fi; } > ` + quoteShell(resultPath) + `
  fi
}`
	// Parse the function only after removing a same-named alias in the subshell.
	command := "( unalias " + name + " 2>/dev/null || :\neval " + quoteShell(function) + "\neval " + quoteShell(line) + "\n)"
	_, status, err := s.Run(command, func(data []byte) error {
		if remaining := (1 << 20) - output.Len(); remaining > 0 {
			if len(data) > remaining {
				data = data[:remaining]
			}
			_, _ = output.Write(data)
		}
		return nil
	})
	result.Output = output.String()
	if err != nil {
		return result, err
	}
	if status != 0 {
		return result, fmt.Errorf("program preparation exited with status %d", status)
	}
	file, err := os.Open(resultPath)
	if err != nil {
		return result, errors.New("shell wrapper did not launch the supported program")
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	_ = file.Close()
	if err != nil {
		return result, fmt.Errorf("read program launch: %w", err)
	}
	if len(data) > 64<<10 || len(data) == 0 || data[len(data)-1] != 0 {
		return result, errors.New("invalid program launch from shell wrapper")
	}
	fields := strings.Split(string(data[:len(data)-1]), "\x00")
	if !filepath.IsAbs(fields[0]) {
		return result, errors.New("shell wrapper returned an invalid directory")
	}
	result.Directory, result.Args = fields[0], fields[1:]
	return result, nil
}
