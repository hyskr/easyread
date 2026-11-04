package pdf

import "testing"

func TestPromptEmphasis(t *testing.T) {
	runs := parseEmphasis([]byte(`<pdf2xml><page><text><b>Important result</b> and <i>related work</i></text></page></pdf2xml>`))
	got := insertEmphasis("Important result and related work", runs)
	if got != "**Important result** and *related work*" {
		t.Fatal(got)
	}
}
