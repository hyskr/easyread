package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hyskr/easyread/backend/store"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSegmentsAndBatches(t *testing.T) {
	if Workers(nil, 9) != 4 || Workers(20, 3) != 8 || Workers(0, 3) != 2 {
		t.Fatal("worker cap")
	}
	b := Batches([]int{1, 2, 4, 5, 6}, 3, []int{4, 5})
	if fmt.Sprint(b) != "[[1 2] [4 5] [6]]" {
		t.Fatal(b)
	}
	p := Plan([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 2, 4, t.TempDir())
	if len(p) != 3 {
		t.Fatal(p)
	}
	for _, lane := range p {
		if len(lane) > 4 {
			t.Fatal(p)
		}
	}
}
func TestConcurrentLanesAndMissingPageRetry(t *testing.T) {
	w := workspace(t, Object{"meta": Object{"page_count": 4}, "blocks": []any{}, "translation": Object{}})
	old := Run
	defer func() { Run = old }()
	var active, maxActive atomic.Int32
	var calls atomic.Int32
	Run = func(ctx context.Context, cfg Object, prompt, cwd string, images []string) (string, Object, error) {
		a := active.Add(1)
		defer active.Add(-1)
		for {
			m := maxActive.Load()
			if a <= m || maxActive.CompareAndSwap(m, a) {
				break
			}
		}
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		matches := regexp.MustCompile(`===== PAGE (\d+) =====`).FindAllStringSubmatch(prompt, -1)
		blocks := []any{}
		for _, match := range matches {
			n := Num(match[1])
			if n == 2 && len(matches) > 1 {
				continue
			}
			blocks = append(blocks, Object{"id": fmt.Sprintf("p%d", n), "type": "para", "page": n, "en": "Source", "zh": "翻译"})
		}
		raw, _ := json.Marshal(Object{"blocks": blocks})
		return string(raw), nil, nil
	}
	failed, e := Pages(context.Background(), w, Object{"batch_pages": 2, "concurrency": 2}, []int{1, 2, 3, 4}, false, func(int, int, string, Object) {})
	if e != nil || len(failed) > 0 || maxActive.Load() < 2 || calls.Load() != 3 {
		t.Fatalf("%v %v concurrency=%d calls=%d", failed, e, maxActive.Load(), calls.Load())
	}
	p, _ := w.Load("paper")
	if len(List(p["blocks"])) != 4 || len(List(Obj(p["translation"])["done_pages"])) != 4 {
		t.Fatal(p)
	}
}
func TestRepairRetainsAllCellsAndFields(t *testing.T) {
	original := Object{"blocks": []any{Object{"id": "t", "type": "table", "page": float64(1), "head": []any{[]any{"A", "B"}}, "rows": []any{[]any{"one", "two"}}}}}
	missing := clone(original)
	Obj(List(missing["blocks"])[0])["rows"] = []any{[]any{"one"}}
	if RetainsContent(original, missing) {
		t.Fatal("lost table cell accepted")
	}
	missing = clone(original)
	delete(Obj(List(missing["blocks"])[0]), "head")
	if RetainsContent(original, missing) {
		t.Fatal("lost field accepted")
	}
	w := workspace(t, Object{"meta": Object{"page_count": 1}, "blocks": []any{}})
	old := Run
	defer func() { Run = old }()
	var calls int
	Run = func(context.Context, Object, string, string, []string) (string, Object, error) {
		calls++
		if calls == 1 {
			return `{"blocks":[{"id":"a","type":"para","page":1,"en":"Use $x$.","zh":"用 \\frac{x}{2}。"}]}`, nil, nil
		}
		return `{"blocks":[{"id":"a","type":"para","page":1,"en":"Use $x$.","zh":"用 $\\frac{x}{2}$。"}]}`, nil, nil
	}
	if e := onePage(context.Background(), w, Object{}, 1, false, func(Object) {}); e != nil {
		t.Fatal(e)
	}
	p, _ := w.Load("paper")
	if calls != 2 || !strings.Contains(Str(Obj(List(p["blocks"])[0])["zh"]), "$\\frac") {
		t.Fatal(calls, p)
	}
}
func TestCodeAliasesAndMathMarkers(t *testing.T) {
	code := "print(\"```\", \"A. ‖ B.\", \"$HOME\")"
	d := Object{"blocks": []any{Object{"id": "code1", "type": "listing", "lines": []any{code}, "lang": "python", "caption_en": "Listing", "caption_zh": "代码"}}}
	d, e := Normalize(d, []int{1}, map[string]bool{})
	if e != nil {
		t.Fatal(e)
	}
	b := Obj(List(d["blocks"])[0])
	if !strings.Contains(Str(b["en"]), code) || !strings.Contains(Str(b["zh"]), code) || !strings.Contains(Str(b["en"]), "````python") {
		t.Fatal(b)
	}
	s, _ := Unmark("Keep $$a. ‖ b$$. ‖ Next.")
	if s != "Keep $$a. ‖ b$$. Next." {
		t.Fatal(s)
	}
	s, cuts := Unmark("A. ‖‖ B.")
	if s != "A. B." || cuts != nil {
		t.Fatal(s, cuts)
	}
}
func TestFillSentenceAlignment(t *testing.T) {
	w := workspace(t, Object{"meta": Object{"page_count": 1}, "translation": Object{"done_pages": []int{1}, "en_pages": []int{1}}, "blocks": []any{Object{"id": "a", "type": "para", "page": 1, "en": "First one. Second one."}}})
	old := Run
	defer func() { Run = old }()
	Run = func(ctx context.Context, cfg Object, prompt, cwd string, images []string) (string, Object, error) {
		if !strings.Contains(prompt, "First one. ‖ Second one.") {
			t.Fatal(prompt)
		}
		return `{"zh":{"a":"第一。‖第二。"}}`, nil, nil
	}
	if e := onePage(context.Background(), w, Object{}, 1, false, func(Object) {}); e != nil {
		t.Fatal(e)
	}
	p, _ := w.Load("paper")
	b := Obj(List(p["blocks"])[0])
	if b["zh"] != "第一。第二。" || b["sents"] == nil {
		t.Fatal(b)
	}
}
func TestContinuationRequiresVerifiedSourceAndExactSuffix(t *testing.T) {
	w := workspace(t, Object{"meta": Object{"page_count": 2}, "translation": Object{"done_pages": []int{1}}, "blocks": []any{Object{"id": "a", "type": "para", "page": 1, "en": "Beginning. The value is 2 + 3.", "zh": "开始，值为 2 + 3。"}}})
	_ = os.MkdirAll(filepath.Join(w.Root, "extract"), 0700)
	path := filepath.Join(w.Root, "extract", "page-002.txt")
	_ = os.WriteFile(path, []byte("The value is 2 + 3."), 0600)
	old := VerifyTextOnly
	defer func() { VerifyTextOnly = old }()
	VerifyTextOnly = func(context.Context, *store.Workspace, []int) (bool, error) { return true, nil }
	if !ContinuationCovered(context.Background(), w, []int{2}, false) {
		t.Fatal("covered suffix rejected")
	}
	_ = os.WriteFile(path, []byte("The value is 2 - 3."), 0600)
	if ContinuationCovered(context.Background(), w, []int{2}, false) {
		t.Fatal("different math accepted")
	}
	_ = os.WriteFile(path, []byte("The value is 2 + 3."), 0600)
	VerifyTextOnly = func(context.Context, *store.Workspace, []int) (bool, error) { return false, nil }
	if ContinuationCovered(context.Background(), w, []int{2}, false) {
		t.Fatal("graphics page accepted")
	}
}
func TestTermsRespectOtherMeaningsAndCode(t *testing.T) {
	paper := Object{"glossary": []any{Object{"en": "bias", "zh": "偏倚"}, Object{"en": "standard deviation", "zh": "标准偏差"}}}
	data := Object{"glossary": []any{Object{"en": "bias", "zh": "偏差"}}, "blocks": []any{Object{"id": "a", "type": "para", "en": "bias and standard deviation", "zh": "偏差和标准偏差"}, Object{"id": "b", "type": "para", "en": "```\nbias\n```", "zh": "```\n偏差\n```"}}}
	UnifyTerms(paper, data)
	if Obj(List(data["blocks"])[0])["zh"] != "偏倚和标准偏差" || Obj(List(data["blocks"])[1])["zh"] != "```\n偏差\n```" {
		t.Fatal(data)
	}
}
func TestMergeDoneIndependentOfReplace(t *testing.T) {
	w := workspace(t, Object{"meta": Object{"page_count": 2}, "blocks": []any{Object{"id": "a", "type": "para", "page": 1, "en": "keep"}}})
	if e := MergeBlocks(w, Object{"blocks": []any{Object{"id": "b", "type": "para", "page": 2, "en": "new"}}}, []int{2}, nil, true); e != nil {
		t.Fatal(e)
	}
	p, _ := w.Load("paper")
	if len(List(p["blocks"])) != 2 || fmt.Sprint(Obj(p["translation"])["done_pages"]) != "[2]" || fmt.Sprint(Obj(p["translation"])["en_pages"]) != "[2]" {
		t.Fatal(p)
	}
	if e := MergeBlocks(w, Object{"blocks": []any{Object{"id": "a", "type": "para", "page": 1, "en": "update"}}}, nil, nil, false); e != nil {
		t.Fatal(e)
	}
	p, _ = w.Load("paper")
	if len(List(p["blocks"])) != 2 || Obj(List(p["blocks"])[0])["en"] != "update" {
		t.Fatal(p)
	}
}
func TestChecksReplaceTranslatorButRetainUserNotes(t *testing.T) {
	w := workspace(t, Object{"meta": Object{}, "blocks": []any{Object{"id": "a", "type": "para", "page": 1}}})
	_ = w.Write("discussion", Object{"entries": []any{Object{"id": "user", "kind": "check", "by": "user", "anchor": "a", "body": "keep"}}})
	for i := 0; i < 2; i++ {
		if e := SaveChecks(w, []any{Object{"anchor": "a", "body": "Model concern", "quote": "A. ‖ B."}}, []int{1}); e != nil {
			t.Fatal(e)
		}
	}
	d, _ := w.Load("discussion")
	if len(List(d["entries"])) != 2 || Obj(List(d["entries"])[1])["quote"] != "A. B." {
		t.Fatal(d)
	}
}
func TestConsistencyCannotOverwriteReaderEdits(t *testing.T) {
	w := workspace(t, Object{"meta": Object{"page_count": 1}, "glossary": []any{Object{"en": "bias", "zh": "偏倚"}}, "blocks": []any{Object{"id": "a", "type": "para", "page": 1, "en": "There is bias.", "zh": "存在偏差。"}}})
	old := Run
	defer func() { Run = old }()
	Run = func(context.Context, Object, string, string, []string) (string, Object, error) {
		_ = w.Write("reader", Object{"edits": Object{"a": Object{"zh": "user corrected"}}})
		return `{"use":{"bias":"偏倚"},"fixes":[{"key":"a","term":"bias","from":"偏差"}]}`, nil, nil
	}
	n, e := Consistency(context.Background(), w, Object{}, []int{1}, func(Object) {})
	if e != nil || n != 0 {
		t.Fatal(n, e)
	}
	p, _ := w.Load("paper")
	if Obj(List(p["blocks"])[0])["zh"] != "存在偏差。" {
		t.Fatal(p)
	}
}
func TestFloatOrderRespectsColumns(t *testing.T) {
	blocks := []any{Object{"id": "left", "type": "para", "page": 1}, Object{"id": "right", "type": "para", "page": 1}, Object{"id": "fig", "type": "figure", "page": 1}}
	layout := Object{"left": Object{"page": 1, "box": []any{.1, .1, .45, .2}, "src": "text"}, "right": Object{"page": 1, "box": []any{.55, .1, .9, .2}, "src": "text"}, "fig": Object{"page": 1, "box": []any{.1, .3, .45, .5}, "src": "graphic"}}
	if ids := FloatOrder(blocks, layout); fmt.Sprint(ids) != "[left fig right]" {
		t.Fatal(ids)
	}
	Obj(layout["fig"])["box"] = []any{.55, .05, .9, .09}
	if ids := FloatOrder(blocks, layout); fmt.Sprint(ids) != "[left fig right]" {
		t.Fatal(ids)
	}
}
func TestTranslationAlignmentSurvivesNormalizeThenMerge(t *testing.T) {
	w := workspace(t, Object{"meta": Object{"page_count": 1}, "blocks": []any{}})
	old := Run
	defer func() { Run = old }()
	Run = func(context.Context, Object, string, string, []string) (string, Object, error) {
		return `{"blocks":[{"id":"a","type":"para","page":1,"en":"First. ‖ Second.","zh":"第一。‖第二。"}]}`, nil, nil
	}
	if e := onePage(context.Background(), w, Object{}, 1, false, func(Object) {}); e != nil {
		t.Fatal(e)
	}
	p, _ := w.Load("paper")
	b := Obj(List(p["blocks"])[0])
	if !ValidAlignment(Str(b["en"]), Str(b["zh"]), b["sents"]) {
		t.Fatal(b)
	}
}
