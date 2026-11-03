package translate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hyskr/easyread/backend/pdf"
	"github.com/hyskr/easyread/backend/store"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

var mergeLock sync.Mutex
var VerifyTextOnly = pdf.TextOnlyPages

func ContinuationCovered(ctx context.Context, ws *store.Workspace, pages []int, read bool) bool {
	if len(pages) == 0 {
		return false
	}
	for i := 1; i < len(pages); i++ {
		if pages[i] != pages[i-1]+1 {
			return false
		}
	}
	p, e := ws.Load("paper")
	if e != nil {
		return false
	}
	if !contains(ints(Obj(p["translation"])["done_pages"]), pages[0]-1) {
		return false
	}
	var prev Object
	for _, x := range List(p["blocks"]) {
		b := Obj(x)
		n := Num(b["page"])
		if contains(pages, n) {
			return false
		}
		if n < pages[0] && b["role"] != "footnote" && b["type"] != "note" {
			prev = b
		}
	}
	if prev == nil || prev["type"] != "para" || Str(prev["en"]) == "" || (!read && strings.TrimSpace(Str(prev["zh"])) == "") {
		return false
	}
	parts := []string{}
	for _, n := range pages {
		b, e := os.ReadFile(filepath.Join(ws.Root, "extract", fmt.Sprintf("page-%03d.txt", n)))
		if e != nil || len(strings.TrimSpace(string(b))) == 0 {
			return false
		}
		parts = append(parts, string(b))
	}
	ok, e := VerifyTextOnly(ctx, ws, pages)
	if e != nil || !ok {
		return false
	}
	normalize := func(s string) string {
		s = strings.NewReplacer("ﬀ", "ff", "ﬁ", "fi", "ﬂ", "fl", "ﬃ", "ffi", "ﬄ", "ffl", "ﬅ", "st", "ﬆ", "st", "\u00ad", "").Replace(s)
		return strings.Join(strings.Fields(s), " ")
	}
	source := normalize(strings.Join(parts, "\n"))
	return source != "" && strings.HasSuffix(normalize(Str(prev["en"])), source)
}

type MissingPages struct{ Pages []int }

func (e *MissingPages) Error() string { return fmt.Sprintf("model omitted pages %v", e.Pages) }

type emptyBatch struct{ pages []int }

func (e *emptyBatch) Error() string {
	return "model returned no content; waiting for previous page coverage"
}
func letters(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func Missing(ws *store.Workspace, pages []int, data Object) []int {
	blocks := List(data["blocks"])
	have := []int{}
	refs := 0
	for _, x := range blocks {
		b := Obj(x)
		n := Num(b["page"])
		have = append(have, n)
		if b["type"] == "references" && (refs == 0 || n < refs) {
			refs = n
		}
	}
	if len(List(data["references"])) > 0 && refs == 0 {
		return nil
	}
	raw, _ := json.Marshal(blocks)
	all := letters(string(raw))
	missing := []int{}
	for _, n := range pages {
		if contains(have, n) || (refs > 0 && n >= refs) {
			continue
		}
		source := []rune(letters(sourceText(ws.Root, n)))
		if len(source) < 80 {
			missing = append(missing, n)
			continue
		}
		found := false
		for _, i := range []int{len(source) / 4, len(source) / 2, len(source) * 3 / 4} {
			end := i + 30
			if end > len(source) {
				end = len(source)
			}
			if strings.Contains(all, string(source[i:end])) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, n)
		}
	}
	return missing
}

// Pages partitions contiguous pages into bounded lanes. A lane preserves document
// order; merge transactions serialize cross-lane glossary and ID resolution.
func Pages(ctx context.Context, ws *store.Workspace, cfg Object, pages []int, read bool, report func(int, int, string, Object)) (map[string]any, error) {
	failed := Object{}
	if len(pages) == 0 {
		return failed, nil
	}
	if ctx.Err() != nil {
		return failed, ctx.Err()
	}
	blank := []int{}
	todoPages := []int{}
	for _, n := range pages {
		data, err := os.ReadFile(filepath.Join(ws.Root, "extract", fmt.Sprintf("page-%03d.txt", n)))
		if err == nil && strings.TrimSpace(string(data)) == "" {
			if verified, e := VerifyTextOnly(ctx, ws, []int{n}); e == nil && verified {
				blank = append(blank, n)
				continue
			}
		}
		todoPages = append(todoPages, n)
	}
	if len(blank) > 0 {
		if e := ws.Update("paper", func(p Object) error { markPages(p, blank, false); return nil }); e != nil {
			return failed, e
		}
		markJobCompleted(ws, blank)
		originalReport := report
		report = func(done, total int, msg string, u Object) { originalReport(done+len(blank), total+len(blank), msg, u) }
	}
	pages = todoPages
	if len(pages) == 0 {
		report(0, 0, "Blank pages complete", nil)
		return failed, nil
	}
	if !read {
		if e := ws.Update("paper", func(p Object) error {
			m := Obj(p["meta"])
			if Str(m["target"]) == "" {
				m["target"] = Target(m, cfg)
				for _, raw := range List(p["blocks"]) {
					b := Obj(raw)
					if Str(b["zh"]) != "" || Str(b["caption_zh"]) != "" {
						m["target"] = "zh"
						break
					}
				}
			}
			p["meta"] = m
			return nil
		}); e != nil {
			return nil, e
		}
	}
	paper, e := ws.Load("paper")
	if e != nil {
		return nil, e
	}
	en := []int{}
	if !read {
		en = ints(Obj(paper["translation"])["en_pages"])
	}
	size := Num(cfg["batch_pages"])
	if size < 1 {
		size = 2
	}
	if size > 200 {
		size = 200
	}
	segments := Plan(pages, size, Workers(cfg["concurrency"], len(Batches(pages, size, en))), ws.Root)
	lanes := [][][]int{}
	laneOf := map[int]int{}
	for i, s := range segments {
		lanes = append(lanes, Batches(s, size, en))
		for _, n := range s {
			laneOf[n] = i
		}
	}
	var mu sync.Mutex
	done := 0
	quota := ""
	ok := map[int]bool{}
	type laterItem struct {
		pages []int
		fill  bool
	}
	later := []laterItem{}
	var wg sync.WaitGroup
	for laneIndex, lane := range lanes {
		laneIndex, lane := laneIndex, lane
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					mu.Lock()
					defer mu.Unlock()
					for _, batch := range lane {
						for _, n := range batch {
							if !ok[n] {
								failed[strconv.Itoa(n)] = fmt.Sprintf("translation worker failed: %v", recovered)
							}
						}
					}
				}
			}()
			for batchIndex, batch := range lane {
				if ctx.Err() != nil {
					return
				}
				mu.Lock()
				if quota != "" {
					for _, n := range batch {
						failed[strconv.Itoa(n)] = quota
					}
					done += len(batch)
					report(done, len(pages), quota, nil)
					mu.Unlock()
					continue
				}
				previous := batch[0] - 1
				skip := contains(pages, previous) && !ok[previous] && !contains(en, previous)
				next := batch[len(batch)-1] + 1
				nextLane, nextScheduled := laneOf[next]
				owner := next <= Num(Obj(paper["meta"])["page_count"]) && (!nextScheduled || nextLane != laneIndex) && !contains(en, next)
				wait := contains(pages, previous) && !ok[previous]
				report(done, len(pages), fmt.Sprintf("Processing pages %v", batch), nil)
				mu.Unlock()
				todo := append([]int{}, batch...)
				var err error
				for attempt := 0; attempt < 2; attempt++ {
					if ctx.Err() != nil {
						return
					}
					options := Object{"skip_head": skip, "front": batchIndex == 0 && batch[0] > 1, "wait": wait, "next_owner": owner}
					if attempt > 0 {
						options["skip_head"] = false
					}
					err = oneBatch(ctx, ws, cfg, todo, read, options, func(u Object) {
						mu.Lock()
						defer mu.Unlock()
						report(done, len(pages), fmt.Sprintf("Processing pages %v", todo), u)
					})
					if err == nil {
						markJobCompleted(ws, todo)
						mu.Lock()
						for _, n := range todo {
							ok[n] = true
						}
						mu.Unlock()
						break
					}
					if ctx.Err() != nil {
						return
					}
					var missing *MissingPages
					if errors.As(err, &missing) {
						mu.Lock()
						for _, n := range todo {
							if !contains(missing.Pages, n) {
								ok[n] = true
							}
						}
						mu.Unlock()
						completed := []int{}
						for _, n := range todo {
							if !contains(missing.Pages, n) {
								completed = append(completed, n)
							}
						}
						markJobCompleted(ws, completed)
						todo = missing.Pages
						continue
					}
					var empty *emptyBatch
					if errors.As(err, &empty) && wait {
						mu.Lock()
						later = append(later, laterItem{todo, contains(en, todo[0])})
						mu.Unlock()
						err = nil
						break
					}
					Journal(ws, fmt.Sprintf("Pages %v attempt %d: %v", todo, attempt+1, err))
					s := strings.ToLower(err.Error())
					if strings.Contains(s, "insufficient_quota") || strings.Contains(s, "insufficient balance") || strings.Contains(s, "usage limit") || strings.Contains(s, "session limit") || strings.Contains(s, "401") || strings.Contains(s, "402") {
						mu.Lock()
						quota = err.Error()
						mu.Unlock()
						break
					}
				}
				mu.Lock()
				done += len(batch)
				if err != nil {
					for _, n := range todo {
						failed[strconv.Itoa(n)] = err.Error()
					}
				}
				report(done, len(pages), fmt.Sprintf("Processed %d / %d pages", done, len(pages)), nil)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return failed, ctx.Err()
	}
	sort.Slice(later, func(i, j int) bool { return later[i].pages[0] < later[j].pages[0] })
	for _, item := range later {
		var err error
		if ContinuationCovered(ctx, ws, item.pages, read) {
			err = ws.Update("paper", func(p Object) error { markPages(p, item.pages, read); return nil })
		} else if !item.fill && quota == "" {
			err = oneBatch(ctx, ws, cfg, item.pages, read, Object{}, func(u Object) { report(done, len(pages), "Checking continuation", u) })
		} else {
			err = errors.New("unverified continuation remains incomplete")
		}
		if err == nil {
			markJobCompleted(ws, item.pages)
		}
		if err != nil {
			for _, n := range item.pages {
				failed[strconv.Itoa(n)] = err.Error()
			}
		}
	}
	if !read && quota == "" && len(lanes) > 0 && len(Batches(pages, size, en)) > 1 {
		successful := []int{}
		for _, n := range pages {
			if failed[strconv.Itoa(n)] == nil {
				successful = append(successful, n)
			}
		}
		if len(successful) > 0 {
			_, err := Consistency(ctx, ws, cfg, successful, func(u Object) { report(done, len(pages), "Checking terminology consistency", u) })
			if err != nil {
				Journal(ws, "Terminology check skipped: "+err.Error())
			}
		}
	}
	return failed, nil
}
func onePage(ctx context.Context, ws *store.Workspace, cfg Object, n int, read bool, usage func(Object)) error {
	return oneBatch(ctx, ws, cfg, []int{n}, read, Object{}, usage)
}
func oneBatch(ctx context.Context, ws *store.Workspace, cfg Object, pages []int, read bool, options Object, usage func(Object)) error {
	paper, e := ws.Load("paper")
	if e != nil {
		return e
	}
	baseline := clone(paper)
	var todo Object
	empty := []int{}
	if !read && contains(ints(Obj(paper["translation"])["en_pages"]), pages[0]) {
		blocks := []any{}
		for _, x := range List(paper["blocks"]) {
			if contains(pages, Num(Obj(x)["page"])) {
				blocks = append(blocks, x)
			}
		}
		for _, n := range pages {
			present := false
			for _, x := range blocks {
				if Num(Obj(x)["page"]) == n {
					present = true
				}
			}
			if !present {
				empty = append(empty, n)
			}
		}
		todo = Todo(blocks, cfg["translate_figure_text"] != false)
		if len(todo) == 0 {
			if len(empty) > 0 && !ContinuationCovered(ctx, ws, empty, false) {
				return &emptyBatch{empty}
			}
			return Fill(ws, Object{}, baseline, todo, pages)
		}
	}
	prompt, images := Prompt(ws, cfg, paper, pages, read, todo)
	if todo == nil {
		if Bool(options["front"]) {
			prompt = FrontContext(ws.Root, pages[0]) + prompt
		}
		if Bool(options["skip_head"]) {
			prompt += "\nThe preceding page is being processed concurrently. Its final paragraph owns the continuation at the top of this batch: skip only that incomplete leading continuation, preserving all new paragraphs, footnotes, equations, tables and figures."
		}
		next := pages[len(pages)-1] + 1
		if text := sourceText(ws.Root, next); text != "" {
			r := []rune(text)
			if !Bool(options["next_owner"]) && len(r) > 1500 {
				r = r[:1500]
			}
			prompt += fmt.Sprintf("\nNEXT PAGE %d CONTEXT ONLY: use solely to finish the last paragraph started in the requested pages. Assign that paragraph to its starting page, and do not output unrelated content from this context.\n%s", next, string(r))
		}
		prev := previousParagraph(paper, pages[0])
		if prev != nil {
			raw, _ := json.Marshal(prev)
			prompt += "\nPrevious adjacent paragraph already saved (context only, never repeat it):\n" + string(raw)
		}
	}
	raw, u, e := Run(ctx, cfg, prompt, ws.Root, images)
	if u != nil {
		usage(u)
	}
	if e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	data, e := ParseJSON(raw)
	if e != nil {
		_ = os.MkdirAll(filepath.Join(ws.Root, "extract"), 0700)
		_ = os.WriteFile(filepath.Join(ws.Root, "extract", fmt.Sprintf("failed-%03d.txt", pages[0])), []byte(raw), 0600)
		return e
	}
	data = repair(ctx, ws, cfg, data, read, usage)
	if todo != nil {
		if _, ok := data["zh"].(map[string]any); !ok {
			return errors.New("model output missing zh map")
		}
		keep := []int{}
		for _, n := range pages {
			if !contains(empty, n) {
				keep = append(keep, n)
			}
		}
		mergeLock.Lock()
		current, loadErr := ws.Load("paper")
		if loadErr != nil {
			mergeLock.Unlock()
			return loadErr
		}
		UnifyTermsWithSources(current, data, todo)
		fillErr := Fill(ws, data, baseline, todo, keep)
		mergeLock.Unlock()
		if e = fillErr; e != nil {
			return e
		}
		if len(empty) > 0 {
			if !ContinuationCovered(ctx, ws, empty, false) {
				return &emptyBatch{empty}
			}
			if e = ws.Update("paper", func(p Object) error { markPages(p, empty, false); return nil }); e != nil {
				return e
			}
		}
		return SaveChecks(ws, data["checks"], pages)
	}
	mergeLock.Lock()
	defer mergeLock.Unlock()
	paper, e = ws.Load("paper")
	if e != nil {
		return e
	}
	taken := map[string]bool{}
	for _, x := range List(paper["blocks"]) {
		b := Obj(x)
		if !contains(pages, Num(b["page"])) {
			taken[Str(b["id"])] = true
		}
	}
	data, e = Normalize(data, pages, taken)
	if e != nil {
		return e
	}
	if len(List(data["blocks"])) == 0 && len(List(data["references"])) == 0 {
		if ContinuationCovered(ctx, ws, pages, read) {
			return ws.Update("paper", func(p Object) error { markPages(p, pages, read); return nil })
		}
		return &emptyBatch{pages}
	}
	missing := Missing(ws, pages, data)
	keep := []int{}
	for _, n := range pages {
		if !contains(missing, n) {
			keep = append(keep, n)
		}
	}
	for _, x := range List(data["blocks"]) {
		b := Obj(x)
		if cfg["translate_figure_text"] == false {
			delete(b, "image_en")
			delete(b, "image_zh")
		}
		if b["type"] == "figure" {
			box := []float64{}
			for _, v := range List(b["box"]) {
				f, ok := v.(float64)
				if !ok {
					box = nil
					break
				}
				box = append(box, f)
			}
			if len(box) == 4 {
				if src, err := pdf.Crop(ctx, ws, Num(b["page"]), box, ""); err == nil {
					b["src"] = src
				} else {
					Journal(ws, "Figure crop unavailable: "+err.Error())
				}
			}
		}
	}
	UnifyTerms(paper, data)
	if e = Merge(ws, data, baseline, keep, read); e != nil {
		return e
	}
	if e = SaveChecks(ws, data["checks"], pages); e != nil {
		return e
	}
	if len(missing) > 0 {
		return &MissingPages{missing}
	}
	return nil
}
func previousParagraph(p Object, page int) Object {
	var prev Object
	for _, x := range List(p["blocks"]) {
		b := Obj(x)
		if Num(b["page"]) >= page || Num(b["page"]) < page-1 || b["role"] == "footnote" || b["type"] == "note" {
			continue
		}
		prev = b
	}
	if prev != nil && prev["type"] == "para" {
		return prev
	}
	return nil
}

func markJobCompleted(ws *store.Workspace, pages []int) {
	if len(pages) == 0 {
		return
	}
	job, e := ws.Load("job")
	if e != nil || job["state"] != "running" {
		return
	}
	_ = ws.Update("job", func(j Object) error {
		if j["state"] == "running" {
			j["completed_pages"] = unique(append(ints(j["completed_pages"]), pages...))
		}
		return nil
	})
}
