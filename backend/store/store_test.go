package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestTextHashUTF16(t *testing.T) {
	cases := map[string]string{"": "811c9dc5", "hello": "4f9f2cab", "😀": "cb31c4b8"}
	for s, want := range cases {
		if got := TextHash(s); got != want {
			t.Errorf("hash %q = %s, want %s", s, got, want)
		}
	}
}
func TestReaderOpsAndHistory(t *testing.T) {
	w := NewWorkspace(t.TempDir())
	if e := w.Write("reader", EmptyReader()); e != nil {
		t.Fatal(e)
	}
	_, e := w.ApplyReaderOps([]Object{{"op": "note_del", "id": "n1", "at": "2026-10-01T00:02:00Z"}, {"op": "note", "note": Object{"id": "n1", "body": "old", "updated": "2026-10-01T00:01:00Z"}}, {"op": "progress", "block": "p2", "off": 0.32, "ratio": 0.43, "at": "2026-10-01T00:01:00Z"}}, "test")
	if e != nil {
		t.Fatal(e)
	}
	r, _ := w.Load("reader")
	if !truth(obj(obj(r["notes"])["n1"])["deleted"]) {
		t.Fatal("older note resurrected")
	}
	if obj(r["progress"])["off"] != 0.32 {
		t.Fatal("missing offset")
	}
	_, e = w.ApplyReaderOps([]Object{{"op": "progress", "block": "p5", "ratio": 0.6, "at": "2026-10-01T00:02:00Z"}}, "")
	if e != nil {
		t.Fatal(e)
	}
	r, _ = w.Load("reader")
	if _, ok := obj(r["progress"])["off"]; ok {
		t.Fatal("legacy progress retained offset")
	}
	snaps, _ := filepath.Glob(filepath.Join(w.Root, "history", "reader-*.json"))
	if len(snaps) != 1 {
		t.Fatal(snaps)
	}
	b, _ := os.ReadFile(filepath.Join(w.Root, "history", "reader.log.jsonl"))
	if strings.Count(string(b), "\n") != 4 {
		t.Fatal(string(b))
	}
}
func TestCorruptProtection(t *testing.T) {
	w := NewWorkspace(t.TempDir())
	path := filepath.Join(w.Root, "reader.json")
	broken := []byte{0x89, 1}
	os.WriteFile(path, broken, 0600)
	if _, e := w.ApplyReaderOps([]Object{{"op": "paper_note", "body": "x"}}, ""); e == nil {
		t.Fatal("expected corruption error")
	}
	if e := w.Write("reader", EmptyReader()); e == nil {
		t.Fatal("write replaced corrupt file")
	}
	got, _ := os.ReadFile(path)
	if !reflect.DeepEqual(got, broken) {
		t.Fatal("corrupt data changed")
	}
}
func TestConcurrentUpdatesPreserveUnknown(t *testing.T) {
	w := NewWorkspace(t.TempDir())
	w.Write("item", Object{"unknown": Object{"keep": true}, "count": 0})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := w.Update("item", func(v Object) error { v["count"] = number(v["count"]) + 1; return nil }); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	v, _ := w.Load("item")
	if number(v["count"]) != 10 || obj(v["unknown"])["keep"] != true {
		t.Fatal(v)
	}
}
func TestImportLinksTrashAndRestore(t *testing.T) {
	l, e := NewLibrary(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(t.TempDir(), "test.pdf")
	os.WriteFile(source, []byte("%PDF-1.4\ntest"), 0600)
	w, fresh, e := l.ImportPath(source, Object{"custom": "preserved", "source_pdf_path": "evil"}, true)
	if e != nil || !fresh {
		t.Fatal(e)
	}
	p, e := w.SourcePDF()
	if e != nil || !sameFilePath(p, source) {
		t.Fatal(p, e)
	}
	w2, fresh, e := l.ImportPDF([]byte("%PDF-1.4\ntest"), "other.pdf", nil)
	if e != nil || fresh || w2.ID() != w.ID() {
		t.Fatal("duplicate import changed workspace", e)
	}
	paper, _ := w.Load("paper")
	if obj(paper["meta"])["custom"] != "preserved" {
		t.Fatal(paper)
	}
	if _, e := os.Stat(filepath.Join(w.Root, "source.pdf")); !os.IsNotExist(e) {
		t.Fatal("duplicate import created source copy")
	}
	trash, e := l.Trash(w.ID())
	if e != nil {
		t.Fatal(e)
	}
	items, e := TrashItems(l.Root)
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	id, e := Restore(l.Root, filepath.Base(trash))
	if e != nil || id != w.ID() {
		t.Fatal(id, e)
	}
	if _, _, e = l.ImportPath(source, nil, true); e != nil {
		t.Fatal(e)
	}
	if _, e := Purge(l.Root, "../outside"); e == nil {
		t.Fatal("traversal permitted")
	}
	if _, e = os.Stat(source); e != nil {
		t.Fatal("original removed", e)
	}
}
func TestListIncludesCorruptEntries(t *testing.T) {
	l, _ := NewLibrary(t.TempDir())
	for _, id := range []string{"healthy00001", "damaged00001"} {
		os.Mkdir(filepath.Join(l.Root, id), 0755)
		WriteJSON(filepath.Join(l.Root, id, "paper.json"), Object{"meta": Object{"title_en": "Healthy"}})
	}
	os.WriteFile(filepath.Join(l.Root, "damaged00001", "reader.json"), []byte("{bad"), 0600)
	items, e := l.List()
	if e != nil || len(items) != 2 {
		t.Fatal(items, e)
	}
	if items[0]["load_error"] == nil {
		t.Fatal(items)
	}
}

func TestConvertPDFToLinkRequiresEqualCopyAndConsent(t *testing.T) {
	l, _ := NewLibrary(t.TempDir())
	data := []byte("%PDF-1.4\noriginal")
	source := filepath.Join(t.TempDir(), "original.pdf")
	os.WriteFile(source, data, 0600)
	w, _, _ := l.ImportPDF(data, "original.pdf", nil)
	if _, e := l.ConvertPDFToLink(w.ID(), source, false); e == nil {
		t.Fatal("copy deleted without consent")
	}
	os.WriteFile(filepath.Join(w.Root, "source.pdf"), append(data, 1), 0600)
	if _, e := l.ConvertPDFToLink(w.ID(), source, true); e == nil {
		t.Fatal("changed copy discarded")
	}
	os.WriteFile(filepath.Join(w.Root, "source.pdf"), data, 0600)
	if _, e := l.ConvertPDFToLink(w.ID(), source, true); e != nil {
		t.Fatal(e)
	}
	if p, e := w.SourcePDF(); e != nil || !sameFilePath(p, source) {
		t.Fatal(p, e)
	}
	if _, e := os.Stat(filepath.Join(w.Root, "source.pdf")); !os.IsNotExist(e) {
		t.Fatal("copy remains")
	}
}
func TestRepairDoneLeavesCrossPageContinuation(t *testing.T) {
	w := NewWorkspace(t.TempDir())
	os.Mkdir(filepath.Join(w.Root, "extract"), 0755)
	page1 := strings.Repeat("abandon", 20)
	page2 := strings.Repeat("zebra", 30)
	os.WriteFile(filepath.Join(w.Root, "extract", "page-001.txt"), []byte(page1), 0600)
	os.WriteFile(filepath.Join(w.Root, "extract", "page-002.txt"), []byte(page2), 0600)
	w.Write("paper", Object{"translation": Object{"done_pages": []any{1, 2, 3}}, "blocks": []any{Object{"page": 3, "en": page2}}})
	bad, e := w.RepairDone()
	if e != nil || !reflect.DeepEqual(bad, []int{1}) {
		t.Fatal(bad, e)
	}
	p, _ := w.Load("paper")
	if len(array(obj(p["translation"])["done_pages"])) != 2 {
		t.Fatal(p)
	}
}
func TestInvalidOpHasNoPersistedSideEffects(t *testing.T) {
	w := NewWorkspace(t.TempDir())
	w.Write("reader", EmptyReader())
	before, _ := os.ReadFile(filepath.Join(w.Root, "reader.json"))
	if _, e := w.ApplyReaderOps([]Object{{"op": "paper_note", "body": "not saved"}, {"op": "edit"}}, ""); e == nil {
		t.Fatal("invalid op accepted")
	}
	after, _ := os.ReadFile(filepath.Join(w.Root, "reader.json"))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("partial operation persisted")
	}
}

// Exact behavioral ports of both tests in tests/library/test_reader_ops.py.
func TestReaderDeletionWinsDelayedCreationBothOrders(t *testing.T) {
	note := Object{"op": "note", "note": Object{"id": "n1", "body": "旧笔记", "updated": "2026-10-01T00:01:00Z"}}
	deletion := Object{"op": "note_del", "id": "n1", "at": "2026-10-01T00:02:00Z"}
	for _, ops := range [][]Object{{note, deletion}, {deletion, note}} {
		w := NewWorkspace(t.TempDir())
		for _, op := range ops {
			if _, e := w.ApplyReaderOps([]Object{op}, ""); e != nil {
				t.Fatal(e)
			}
		}
		r, e := w.Load("reader")
		if e != nil {
			t.Fatal(e)
		}
		if !truth(obj(obj(r["notes"])["n1"])["deleted"]) {
			t.Fatal("older creation resurrected deleted note")
		}
		restored := Object{"op": "note", "note": Object{"id": "n1", "body": "旧笔记", "updated": "2026-10-01T00:03:00Z"}}
		if _, e = w.ApplyReaderOps([]Object{restored}, ""); e != nil {
			t.Fatal(e)
		}
		r, e = w.Load("reader")
		if e != nil {
			t.Fatal(e)
		}
		if truth(obj(obj(r["notes"])["n1"])["deleted"]) {
			t.Fatal("newer note could not restore deletion")
		}
	}
}
func TestReaderReadingPositionOffsetRatioAndDelayedProgress(t *testing.T) {
	w := NewWorkspace(t.TempDir())
	first := Object{"op": "progress", "block": "p2", "off": 0.32, "ratio": 0.43, "at": "2026-10-01T00:01:00Z"}
	if _, e := w.ApplyReaderOps([]Object{first}, ""); e != nil {
		t.Fatal(e)
	}
	r, e := w.Load("reader")
	if e != nil {
		t.Fatal(e)
	}
	want := Object{"block": "p2", "off": 0.32, "ratio": 0.43, "at": "2026-10-01T00:01:00Z"}
	if !reflect.DeepEqual(r["progress"], want) {
		t.Fatal(r["progress"])
	}
	older := Object{"op": "progress", "block": "p1", "off": 0.9, "ratio": 0.1, "at": "2026-10-01T00:00:00Z"}
	if _, e = w.ApplyReaderOps([]Object{older}, ""); e != nil {
		t.Fatal(e)
	}
	r, e = w.Load("reader")
	if e != nil {
		t.Fatal(e)
	}
	if obj(r["progress"])["block"] != "p2" {
		t.Fatal("older progress overwrote position")
	}
	legacy := Object{"op": "progress", "block": "p5", "ratio": 0.6, "at": "2026-10-01T00:02:00Z"}
	if _, e = w.ApplyReaderOps([]Object{legacy}, ""); e != nil {
		t.Fatal(e)
	}
	r, e = w.Load("reader")
	if e != nil {
		t.Fatal(e)
	}
	p := obj(r["progress"])
	if p["ratio"] != 0.6 {
		t.Fatal(p)
	}
	if _, exists := p["off"]; exists {
		t.Fatal("legacy progress retained old offset")
	}
}

func sameFilePath(a, b string) bool {
	a, _ = filepath.EvalSymlinks(a)
	b, _ = filepath.EvalSymlinks(b)
	return a == b
}

func TestLibraryAcceptsDirectoryAlias(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "library-link")
	if err := os.Symlink(root, alias); err != nil {
		t.Skip(err)
	}
	lib, err := NewLibrary(alias)
	if err != nil {
		t.Fatal(err)
	}
	workspace, _, err := lib.ImportPDF([]byte("%PDF-1.4\nalias"), "alias.pdf", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lib.Workspace(workspace.ID()); err != nil {
		t.Fatal(err)
	}
}
