package pdf

import "testing"

func TestWordsWithPDFControlCharacter(t *testing.T) {
	page, err := parseWords([]byte("<doc><page width=\"100\" height=\"100\"><word xMin=\"1\" yMin=\"2\" xMax=\"9\" yMax=\"8\">A\x10B</word><word>中文\tC</word></page></doc>"))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Words) != 2 || page.Words[0].Text != "A�B" || page.Words[1].Text != "中文\tC" {
		t.Fatalf("%+v", page.Words)
	}
}
func TestPDFXMLKeepsWhitespace(t *testing.T) {
	if got := string(cleanPDFXML([]byte("甲\n乙\t丙\r\x00\x10"))); got != "甲\n乙\t丙\r��" {
		t.Fatal(got)
	}
}
