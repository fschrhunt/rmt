package shell

import "testing"

// TestParseRoutesProgramsWithoutChangingOrdinaryHostCommands protects the local/native boundary.
func TestParseRoutesProgramsWithoutChangingOrdinaryHostCommands(t *testing.T) {
	cases := []struct {
		line, program, unsupported string
		bad, exit                  bool
	}{
		{line: `opencode --model 'hello world'`, program: "opencode"},
		{line: `codex resume`, program: "codex"},
		{line: `vim file`, unsupported: "vim"}, {line: `sudo /usr/bin/nvim file`, unsupported: "nvim"},
		{line: `claude -w`, unsupported: "claude"}, {line: `pi -w`, unsupported: "pi"},
		{line: `ls -la | head`}, {line: `cd /tmp && pwd`}, {line: `printf '%s' "$HOME"`},
		{line: `command -v codex`}, {line: `command -V opencode`},
		{line: `print -l -- *(.N)`},
		{line: `opencode | cat`, bad: true}, {line: `opencode "$HOME"`, program: "opencode"}, {line: `FOO=bar opencode`, bad: true}, {line: `/usr/bin/opencode`, bad: true},
		{line: `exit`, exit: true},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			got, err := Parse(tc.line)
			if (err != nil) != tc.bad || got.Program != tc.program || got.Unsupported != tc.unsupported || got.Exit != tc.exit {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

// TestParseRoutesExtensionProgram includes explicitly registered programs in native routing.
func TestParseRoutesExtensionProgram(t *testing.T) {
	for _, name := range []string{"demo", "pi"} {
		got, err := Parse(name+" --flag", name)
		if err != nil || got.Program != name || got.Unsupported != "" {
			t.Fatalf("%s: %+v %v", name, got, err)
		}
	}
	got := AliasCommand("custom=demo\n", "custom", "demo")
	if got.Program != "demo" {
		t.Fatalf("extension alias: %+v", got)
	}
}

// TestAliasCommand detects interactive programs through a host shell alias.
func TestAliasCommand(t *testing.T) {
	for _, output := range []string{"oc=opencode\n", "alias oc='opencode --model example'\n"} {
		if got := AliasCommand(output, "oc"); got.Program != "opencode" {
			t.Fatalf("%q: got %+v", output, got)
		}
	}
	if got := AliasCommand("oc='echo opencode'\n", "oc"); got.Program != "" {
		t.Fatalf("ordinary alias detected as a program: %+v", got)
	}
	if got := AliasCommand("cl=claude\n", "cl"); got.Unsupported != "claude" {
		t.Fatalf("unsupported program alias: %+v", got)
	}
}
