package shell

import (
	"reflect"
	"testing"
)

// TestParseRoutesProgramsWithoutChangingOrdinaryHostCommands protects the local/native boundary.
func TestParseRoutesProgramsWithoutChangingOrdinaryHostCommands(t *testing.T) {
	cases := []struct {
		line, program, unsupported string
		args                       []string
		bad, exit                  bool
	}{
		{line: `opencode --model 'hello world'`, program: "opencode", args: []string{"--model", "hello world"}},
		{line: `codex resume`, program: "codex", args: []string{"resume"}},
		{line: `vim file`, unsupported: "vim"}, {line: `sudo /usr/bin/nvim file`, unsupported: "nvim"},
		{line: `ls -la | head`}, {line: `cd /tmp && pwd`}, {line: `printf '%s' "$HOME"`},
		{line: `opencode | cat`, bad: true}, {line: `opencode "$HOME"`, bad: true}, {line: `FOO=bar opencode`, bad: true},
		{line: `exit`, exit: true},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			got, err := Parse(tc.line)
			if (err != nil) != tc.bad || got.Program != tc.program || got.Unsupported != tc.unsupported || got.Exit != tc.exit || !reflect.DeepEqual(got.Args, tc.args) {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}
