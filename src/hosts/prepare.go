package hosts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var programNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// preparedProgram is the host directory and arguments selected for a native client.
type preparedProgram struct {
	Directory string   `json:"directory"`
	Args      []string `json:"args"`
	Output    string   `json:"output,omitempty"`
}

// prepareProgram expands arguments on the host before starting a native client.
// The shell function records the launch instead of starting a host-side interface.
func (s *commandShell) prepareProgram(line, name string) (preparedProgram, error) {
	var result preparedProgram
	if !programNamePattern.MatchString(name) {
		return result, errors.New("unsupported program")
	}
	dir, err := os.MkdirTemp("", "rmt-launch-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	resultPath := filepath.Join(dir, "launch")
	var output bytes.Buffer
	function := `__rmt_program() {
  if [ -e ` + quoteShell(resultPath) + ` ]; then
    printf '%s\n' 'rmt: launch the program once per command' >&2
    return 1
  fi
  { command printf '%s\0' "$(pwd -P)"; if [ "$#" -gt 0 ]; then command printf '%s\0' "$@"; fi; } > ` + quoteShell(resultPath) + `
}`
	// Use a portable function name and alias the requested program to it in the subshell.
	command := "( unalias " + name + " 2>/dev/null || :\neval " + quoteShell(function) + "\nalias " + quoteShell(name+"=__rmt_program") + "\neval " + quoteShell(line) + "\n)"
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

// runExtension executes a host preparation command with the prompt's shell environment.
func (s *commandShell) runExtension(directory string, args []string) (string, string, int, error) {
	if len(args) == 0 || args[0] == "" {
		return "", "", 0, errors.New("extension command is empty")
	}
	if !filepath.IsAbs(directory) {
		return "", "", 0, errors.New("extension directory must be absolute")
	}
	dir, err := os.MkdirTemp("", "rmt-extension-")
	if err != nil {
		return "", "", 0, err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "stdout")
	words := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return "", "", 0, errors.New("extension command contains a NUL byte")
		}
		words[i] = quoteShell(arg)
	}
	var stderr bytes.Buffer
	tooLarge := false
	command := "( cd " + quoteShell(directory) + " && " + strings.Join(words, " ") + " ) > " + quoteShell(file)
	_, status, err := s.Run(command, func(data []byte) error {
		remaining := (1 << 20) - stderr.Len()
		if len(data) > remaining {
			data = data[:remaining]
			tooLarge = true
		}
		_, _ = stderr.Write(data)
		return nil
	})
	if err != nil {
		return "", stderr.String(), status, err
	}
	if tooLarge {
		return "", stderr.String(), status, errors.New("extension command output is too large")
	}
	f, err := os.Open(file)
	if err != nil {
		return "", stderr.String(), status, err
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	_ = f.Close()
	if err != nil {
		return "", stderr.String(), status, err
	}
	if len(data) > 1<<20 {
		return "", stderr.String(), status, errors.New("extension command output is too large")
	}
	return string(data), stderr.String(), status, nil
}
