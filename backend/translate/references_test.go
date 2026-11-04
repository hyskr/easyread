package translate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferenceCollisionKeepsBothEntries(t *testing.T) {
	r := mergeReferences([]any{Object{"id": "1", "text": "First reference", "page": 2}}, []any{Object{"id": "1", "text": "Second reference", "page": 3}})
	if len(r) != 2 || Obj(r[0])["id"] == Obj(r[1])["id"] || Obj(r[0])["cite_key"] != "1" {
		t.Fatal(r)
	}
}
func TestReferenceRetryAddsPrintedKey(t *testing.T) {
	r := mergeReferences([]any{Object{"id": referenceContentID("Same reference"), "text": "Same reference"}}, []any{Object{"id": "Author-2024", "text": "Same reference"}})
	if len(r) != 1 || Obj(r[0])["id"] != "Author-2024" {
		t.Fatal(r)
	}
}

func TestBibliographyHeadingRequiresEntries(t *testing.T) {
	w := workspace(t, Object{"meta": Object{}, "blocks": []any{}})
	os.MkdirAll(filepath.Join(w.Root, "extract"), 0700)
	os.WriteFile(filepath.Join(w.Root, "extract", "page-002.txt"), []byte("References\n"+strings.Repeat("Author research reference publication ", 8)), 0600)
	missing := Missing(w, []int{2}, Object{"blocks": []any{Object{"type": "references", "page": 2}}})
	if len(missing) != 1 || missing[0] != 2 {
		t.Fatal(missing)
	}
}
