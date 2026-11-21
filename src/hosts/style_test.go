package hosts

import "testing"

// TestParseStyle reads only tagged lines from the host's style query.
func TestParseStyle(t *testing.T) {
	out := []byte("some unrelated startup output\n" +
		"RMT_STYLE\tuser\t242\n" +
		"RMT_STYLE\tpath\tblue\n" +
		"RMT_STYLE\tsymbol\tλ\n" +
		"RMT_STYLE\tdown_arrow\t⇣\n" +
		"RMT_STYLE\tup_arrow\t⇡\n" +
		"RMT_STYLE\tstash\t≡\n")
	style := parseStyle(out)
	if style.User != "242" || style.Path != "blue" || style.Symbol != "λ" || style.DownArrow != "⇣" || style.UpArrow != "⇡" || style.Stash != "≡" {
		t.Fatalf("got %+v", style)
	}
	if style.GitBranch != "" || style.PromptError != "" {
		t.Fatalf("unexpected values: %+v", style)
	}
}

// TestParseStyleHostFallback uses the host color when no user color is set.
func TestParseStyleHostFallback(t *testing.T) {
	style := parseStyle([]byte("RMT_STYLE\thost\t245\n"))
	if style.User != "245" {
		t.Fatalf("got %+v", style)
	}
}
