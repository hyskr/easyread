package translate

import (
	"context"
	"errors"
	"fmt"
	"github.com/hyskr/easyread/backend/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workspace(t *testing.T, p Object) *store.Workspace {
	t.Helper()
	w := store.NewWorkspace(t.TempDir())
	if e := w.Write("paper", p); e != nil {
		t.Fatal(e)
	}
	return w
}
func TestScope(t *testing.T) {
	w := workspace(t, Object{"meta": Object{"page_count": 10}})
	_ = os.MkdirAll(filepath.Join(w.Root, "extract"), 0700)
	_ = os.WriteFile(filepath.Join(w.Root, "extract", "page-007.txt"), []byte("Other text\nReferences\n[1] item"), 0600)
	for scope, want := range map[string]int{"body": 7, "all": 10, "first:3": 3, "range:2-4": 3, "range:4-2": 3} {
		got, e := ScopePages(w, scope)
		if e != nil || len(got) != want {
			t.Fatalf("%s: %v %v", scope, got, e)
		}
	}
	if _, e := ScopePages(w, "range:20-30"); e == nil {
		t.Fatal("out of range accepted")
	}
}
func TestJSONNormalizeAndSentences(t *testing.T) {
	d, e := ParseJSON("Here is JSON:\n```json\n{\"blocks\":[{\"type\":\"para\",\"id\":\"x?\",\"en\":\"First. ‖ Second.\",\"zh\":\"第一。‖第二。\"}]}\n```")
	if e != nil {
		t.Fatal(e)
	}
	d, e = Normalize(d, []int{2}, map[string]bool{"x-": true})
	if e != nil {
		t.Fatal(e)
	}
	b := Obj(List(d["blocks"])[0])
	if b["id"] != "x--2" || b["en"] != "First. Second." || b["zh"] != "第一。第二。" || b["sents"] == nil {
		t.Fatalf("%#v", b)
	}
	s, _ := Unmark("The norm ‖w‖ and $‖x‖$ remain.")
	if s != "The norm ‖w‖ and $‖x‖$ remain." {
		t.Fatal(s)
	}
	if _, e = Normalize(Object{"blocks": []any{Object{"type": "table", "head": []any{"bad"}}}}, []int{1}, map[string]bool{}); e == nil {
		t.Fatal("malformed table accepted")
	}
}
func TestMergePreservesConcurrentChangesAndReader(t *testing.T) {
	w := workspace(t, Object{"meta": Object{"page_count": 1, "title_en": "Original"}, "blocks": []any{Object{"id": "p1", "type": "para", "page": 1, "en": "Original", "zh": "old"}}})
	base, _ := w.Load("paper")
	_ = w.Write("reader", Object{"edits": Object{"p1": Object{"zh": "user overlay"}}})
	_ = w.Update("paper", func(p Object) error {
		Obj(List(p["blocks"])[0])["zh"] = "new user text"
		Obj(p["meta"])["title_en"] = "User title"
		return nil
	})
	e := Merge(w, Object{"blocks": []any{Object{"id": "p1", "type": "para", "page": 1, "en": "Original", "zh": "model"}}, "meta": Object{"title_en": "Model title"}}, base, []int{1}, false)
	if e != nil {
		t.Fatal(e)
	}
	p, _ := w.Load("paper")
	if Obj(List(p["blocks"])[0])["zh"] != "new user text" || Obj(p["meta"])["title_en"] != "User title" {
		t.Fatal(p)
	}
	r, _ := w.Load("reader")
	if Obj(Obj(r["edits"])["p1"])["zh"] != "user overlay" {
		t.Fatal(r)
	}
}
func TestPagesCheckpointRetryAndFill(t *testing.T) {
	w := workspace(t, Object{"meta": Object{"page_count": 3}, "blocks": []any{}, "translation": Object{}})
	old := Run
	defer func() { Run = old }()
	calls := 0
	Run = func(ctx context.Context, cfg Object, prompt, cwd string, images []string) (string, Object, error) {
		calls++
		if strings.Contains(prompt, "PAGE 2") {
			return "", nil, errors.New("temporary failure")
		}
		n := 1
		if strings.Contains(prompt, "PAGE 3") {
			n = 3
		}
		return fmt.Sprintf(`{"blocks":[{"id":"p%d","type":"para","page":%d,"en":"text","zh":"译文"}]}`, n, n), Object{"calls": 1}, nil
	}
	failed, e := Pages(context.Background(), w, Object{"target": "ja", "batch_pages": 1, "concurrency": 1}, []int{1, 2, 3}, false, func(int, int, string, Object) {})
	if e != nil || len(failed) != 1 || calls != 4 {
		t.Fatalf("failed=%v err=%v calls=%d", failed, e, calls)
	}
	p, _ := w.Load("paper")
	if Obj(p["meta"])["target"] != "ja" || len(List(Obj(p["translation"])["done_pages"])) != 2 {
		t.Fatal(p)
	}
	_ = w.Update("paper", func(p Object) error {
		p["blocks"] = []any{Object{"id": "keep-id", "type": "para", "page": 2, "en": "Source"}}
		p["translation"] = Object{"done_pages": []int{2}, "en_pages": []int{2}}
		return nil
	})
	Run = func(ctx context.Context, cfg Object, prompt, cwd string, images []string) (string, Object, error) {
		if !strings.Contains(prompt, "keep-id") {
			t.Fatal(prompt)
		}
		return `{"zh":{"keep-id":"filled"}}`, nil, nil
	}
	failed, e = Pages(context.Background(), w, Object{}, []int{2}, false, func(int, int, string, Object) {})
	if e != nil || len(failed) > 0 {
		t.Fatalf("%v %v", failed, e)
	}
	p, _ = w.Load("paper")
	if Obj(List(p["blocks"])[0])["id"] != "keep-id" || Obj(List(p["blocks"])[0])["zh"] != "filled" || len(List(Obj(p["translation"])["en_pages"])) != 0 {
		t.Fatal(p)
	}
}
func TestCancellationDoesNotPublish(t *testing.T) {
	w := workspace(t, Object{"meta": Object{"page_count": 1}, "blocks": []any{}})
	old := Run
	defer func() { Run = old }()
	ctx, cancel := context.WithCancel(context.Background())
	Run = func(context.Context, Object, string, string, []string) (string, Object, error) {
		cancel()
		return `{"blocks":[{"id":"p1","type":"para","en":"a","zh":"b"}]}`, nil, nil
	}
	_, e := Pages(ctx, w, Object{}, []int{1}, false, func(int, int, string, Object) {})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	p, _ := w.Load("paper")
	if len(List(p["blocks"])) != 0 {
		t.Fatal(p)
	}
}
func TestMissingFillRemainsRetryable(t *testing.T) {
	w := workspace(t, Object{"meta": Object{"page_count": 1}, "translation": Object{"done_pages": []int{1}, "en_pages": []int{1}}, "blocks": []any{Object{"id": "a", "type": "para", "page": 1, "en": "A"}, Object{"id": "b", "type": "para", "page": 1, "en": "B"}}})
	p, _ := w.Load("paper")
	todo := Todo(List(p["blocks"]), true)
	if e := Fill(w, Object{"zh": Object{"a": "甲"}}, p, todo, []int{1}); e == nil {
		t.Fatal("missing fill accepted")
	}
	p, _ = w.Load("paper")
	if len(List(Obj(p["translation"])["en_pages"])) != 1 || Obj(List(p["blocks"])[0])["zh"] != "甲" {
		t.Fatal(p)
	}
}
