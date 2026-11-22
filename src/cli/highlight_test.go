package cli

import (
	"regexp"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// TestInputPainter checks styling and that the painter preserves the typed text.
func TestInputPainter(t *testing.T) {
	painter := inputPainter{syntax.NewParser(syntax.Variant(syntax.LangZsh), syntax.KeepComments(true))}
	line := `écho -n "hello" | tee >out # note`
	got := string(painter.Paint([]rune(line), 0))
	for _, want := range []string{inputCommand + "écho", inputOption + "-n", inputString + `"hello"`, inputOperator + "|", inputOperator + ">", inputComment + "# note"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	sgr := regexp.MustCompile(`\x1b\[[0-9;]+m`)
	for _, line := range []string{line, `echo "unfinished`, `echo $( `, `cat >`, `echo x |`, `if foo; then`, `echo '☃'`, `cat <<EOF`} {
		if got := sgr.ReplaceAllString(string(painter.Paint([]rune(line), 0)), ""); got != line {
			t.Errorf("paint changed %q to %q", line, got)
		}
	}
}
