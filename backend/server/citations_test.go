package server

import (
	"bytes"
	"encoding/json"
	"github.com/hyskr/easyread/backend/store"
	"net/http"
	"testing"
)

func TestCitationGraphHTTP(t *testing.T) {
	a, s := testApp(t)
	source := fixture(t, a)
	target, _, err := a.Library.ImportPDF([]byte("%PDFtarget"), "target.pdf", Object{"doi": "10.1234/target", "title_en": "Target paper title"})
	if err != nil {
		t.Fatal(err)
	}
	source.Update("paper", func(p store.Object) error {
		p["references"] = []any{Object{"id": "1", "text": "doi:10.1234/target"}}
		return nil
	})
	status, data := call(t, s, a, http.MethodGet, "/api/p/"+source.ID()+"/citations", nil, false)
	if status != 200 || object(array(data["references"])[0])["target"] != target.ID() {
		t.Fatal(status, data)
	}
}

func TestCitationPDFImportLinksExistingPaper(t *testing.T) {
	a, s := testApp(t)
	source := fixture(t, a)
	data := []byte("%PDFtarget")
	target, _, err := a.Library.ImportPDF(data, "target.pdf", nil)
	if err != nil {
		t.Fatal(err)
	}
	target.Update("paper", func(p store.Object) error { object(p["meta"])["pages"] = []any{Object{"n": 1}}; return nil })
	text := "Unmatched bibliography entry"
	source.Update("paper", func(p store.Object) error { p["references"] = []any{Object{"id": "r1", "text": text}}; return nil })
	request, _ := http.NewRequest("POST", s.URL+"/api/p/"+source.ID()+"/citation-import-pdf?name=target.pdf&ref_id=r1&fingerprint="+store.CitationFingerprint(text), bytes.NewReader(data))
	request.Header.Set("X-Token", a.Token)
	response, err := s.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result Object
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || result["citation_error"] != nil || result["id"] != target.ID() {
		t.Fatal(response.StatusCode, result)
	}
	graph, _ := a.Library.CitationGraph(source.ID())
	if object(array(graph["references"])[0])["target"] != target.ID() {
		t.Fatal(graph)
	}
}
