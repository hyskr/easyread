package store

import "testing"

func TestCitationGraphAndManualLink(t *testing.T) {
	lib, _ := NewLibrary(t.TempDir())
	a, _, _ := lib.ImportPDF([]byte("%PDF-A"), "a.pdf", nil)
	b, _, _ := lib.ImportPDF([]byte("%PDF-B"), "b.pdf", Object{"doi": "10.1234/example", "title_en": "An example research paper"})
	reference := "Author. An example research paper. doi:10.1234/example"
	a.Update("paper", func(p Object) error { p["references"] = []any{Object{"id": "1", "text": reference}}; return nil })
	graph, err := lib.CitationGraph(a.ID())
	if err != nil {
		t.Fatal(err)
	}
	row := obj(array(graph["references"])[0])
	if row["target"] != b.ID() {
		t.Fatal(row)
	}
	reverse, _ := lib.CitationGraph(b.ID())
	if len(array(reverse["cited_by"])) != 1 {
		t.Fatal(reverse)
	}
	if err = lib.LinkReference(a, "1", CitationFingerprint(reference), b.ID()); err != nil {
		t.Fatal(err)
	}
	if err = lib.LinkReference(a, "1", "stale", b.ID()); err == nil {
		t.Fatal("stale reference accepted")
	}
}
func TestCitationAmbiguity(t *testing.T) {
	lib, _ := NewLibrary(t.TempDir())
	a, _, _ := lib.ImportPDF([]byte("%PDF-A"), "a.pdf", nil)
	for _, name := range []string{"B", "C"} {
		lib.ImportPDF([]byte("%PDF-"+name), name+".pdf", Object{"doi": "10.1234/same"})
	}
	a.Update("paper", func(p Object) error {
		p["references"] = []any{Object{"id": "1", "text": "doi:10.1234/same"}}
		return nil
	})
	graph, _ := lib.CitationGraph(a.ID())
	if obj(array(graph["references"])[0])["target"] != nil {
		t.Fatal(graph)
	}
}
