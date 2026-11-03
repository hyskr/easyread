package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hyskr/easyread/backend/store"
	"regexp"
	"strings"
	"unicode/utf16"
)

var parens = regexp.MustCompile(`[（(][^）)]*[）)]`)

func cleanTerm(v any) string { return strings.TrimSpace(parens.ReplaceAllString(Str(v), "")) }
func textFields(b Object) []Object {
	id := Str(b["id"])
	out := []Object{}
	add := func(key, en, zh string) { out = append(out, Object{"key": key, "en": en, "zh": zh, "page": b["page"]}) }
	switch b["type"] {
	case "para", "heading":
		add(id, Str(b["en"]), Str(b["zh"]))
	case "list":
		for i, x := range List(b["items"]) {
			it := Obj(x)
			add(fmt.Sprintf("%s#%d", id, i), Str(it["en"]), Str(it["zh"]))
		}
	case "figure", "table":
		add(id+"#caption", Str(b["caption_en"]), Str(b["caption_zh"]))
	}
	return out
}
func skipEdited(reader Object, key string) bool {
	edits := Obj(reader["edits"])
	id := strings.SplitN(key, "#", 2)[0]
	for _, k := range []string{key, id} {
		if v, ok := Obj(edits[k])["zh"]; ok && v != nil {
			return true
		}
	}
	return false
}
func Suspects(paper, reader Object, pages []int) ([]Object, Object) {
	rules := Object{}
	for _, x := range List(paper["glossary"]) {
		g := Obj(x)
		en, want := strings.TrimSpace(Str(g["en"])), cleanTerm(g["zh"])
		if len([]rune(en)) >= 3 && len([]rune(want)) >= 2 && !strings.EqualFold(en, want) {
			rules[en] = want
		}
	}
	items := []Object{}
	agree := Object{}
	for _, x := range List(paper["blocks"]) {
		b := Obj(x)
		for _, f := range textFields(b) {
			if Str(f["zh"]) == "" || skipEdited(reader, Str(f["key"])) {
				continue
			}
			terms := []any{}
			for en, v := range rules {
				want := Str(v)
				if occurrences(prose(Str(f["en"])), en, true, true) == 0 {
					continue
				}
				if strings.Contains(strings.ToLower(prose(Str(f["zh"]))), strings.ToLower(want)) {
					agree[en] = Num(agree[en]) + 1
				} else {
					terms = append(terms, Object{"en": en, "want": want})
				}
			}
			if len(terms) > 0 && (pages == nil || contains(pages, Num(b["page"]))) {
				f["terms"] = terms
				items = append(items, f)
			}
		}
	}
	if len(items) > 60 {
		items = items[:60]
	}
	return items, agree
}

// Consistency only lets the model identify terminology, never rewrite passages.
// Actual edits are deterministic, count-checked and optimistic against current
// paper text and reader overlays at publication time.
func Consistency(ctx context.Context, ws *store.Workspace, cfg Object, pages []int, usage func(Object)) (int, error) {
	paper, e := ws.Load("paper")
	if e != nil {
		return 0, e
	}
	reader, e := ws.Load("reader")
	if e != nil {
		return 0, e
	}
	items, agree := Suspects(paper, reader, pages)
	if len(items) == 0 {
		return 0, nil
	}
	asked := Object{}
	byKey := map[string]Object{}
	for _, it := range items {
		byKey[Str(it["key"])] = it
		for _, x := range List(it["terms"]) {
			t := Obj(x)
			asked[Str(t["en"])] = t["want"]
		}
	}
	payload, _ := json.Marshal(Object{"passages": items, "agree": agree, "terms": asked})
	prompt := "Identify inconsistent terminology in these academic translations. Choose standard majority/domain usage. Do NOT rewrite passages. Return only {\"use\":{\"source term\":\"preferred translation\"},\"fixes\":[{\"key\":\"passage key\",\"term\":\"source term\",\"from\":\"exact alternative phrase copied from zh\"}]}. Do not propose fixes for pronouns, legitimate omissions, abbreviations, or unrelated meanings.\n" + string(payload)
	raw, u, e := Run(ctx, cfg, prompt, ws.Root, nil)
	if u != nil {
		usage(u)
	}
	if e != nil {
		return 0, e
	}
	data, e := ParseJSON(raw)
	if e != nil {
		return 0, e
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	uses := Object{}
	for en, v := range Obj(data["use"]) {
		use := cleanTerm(v)
		want, ok := asked[en]
		if !ok || len([]rune(use)) < 2 {
			continue
		}
		if !cjk(use) && !strings.EqualFold(use, en) && cjk(Str(want)) {
			continue
		}
		uses[en] = use
	}
	whole := true
	for _, x := range List(paper["blocks"]) {
		n := Num(Obj(x)["page"])
		if n > 0 && !contains(pages, n) {
			whole = false
		}
	}
	baselineFields := map[string]Object{}
	for _, raw := range List(paper["blocks"]) {
		for _, f := range textFields(Obj(raw)) {
			baselineFields[Str(f["key"])] = f
		}
	}
	changed := 0
	e = ws.Update("paper", func(current Object) error {
		latest, e := ws.Load("reader")
		if e != nil {
			return e
		}
		keep := []string{}
		for _, x := range List(current["glossary"]) {
			keep = append(keep, cleanTerm(Obj(x)["zh"]))
		}
		for _, v := range uses {
			keep = append(keep, Str(v))
		}
		for _, x := range List(current["blocks"]) {
			b := Obj(x)
			if !whole && !contains(pages, Num(b["page"])) {
				continue
			}
			for _, f := range textFields(b) {
				key := Str(f["key"])
				if skipEdited(latest, key) {
					continue
				}
				old := baselineFields[key]
				if old == nil || old["en"] != f["en"] || old["zh"] != f["zh"] {
					continue
				}
				original, source := Str(f["zh"]), Str(f["en"])
				newText := original
				for en, v := range uses {
					newText = swapTerm(source, newText, en, Str(asked[en]), Str(v), keep)
				}
				for _, raw := range List(data["fixes"]) {
					fix := Obj(raw)
					if fix["key"] != key {
						continue
					}
					it := byKey[key]
					if it == nil || it["zh"] != original || it["en"] != source {
						continue
					}
					term := Str(fix["term"])
					allowed := false
					for _, raw := range List(it["terms"]) {
						if Obj(raw)["en"] == term {
							allowed = true
						}
					}
					if !allowed {
						continue
					}
					use := Str(uses[term])
					if use == "" {
						use = Str(asked[term])
					}
					old := strings.TrimSpace(Str(fix["from"]))
					if old == "" || len([]rune(old)) > 3*max(2, len([]rune(use))) {
						continue
					}
					newText = swapTerm(source, newText, term, old, use, keep)
				}
				if newText != original {
					obj, k, e := field(b, key)
					if e != nil {
						continue
					}
					shiftAlignment(obj, original, newText)
					obj[k] = newText
					changed++
				}
			}
		}
		if whole {
			for _, x := range List(current["glossary"]) {
				g := Obj(x)
				if use := uses[Str(g["en"])]; use != nil && g["zh"] == asked[Str(g["en"])] {
					g["zh"] = use
				}
			}
		}
		return nil
	})
	return changed, e
}
func shiftAlignment(obj Object, old, new string) {
	pairs := List(obj["sents"])
	if len(pairs) < 2 {
		delete(obj, "sents")
		return
	}
	a, b := utf16.Encode([]rune(old)), utf16.Encode([]rune(new))
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	delta := len(b) - len(a)
	out := [][]int{}
	for i, raw := range pairs {
		p := ints(raw)
		if len(p) != 2 {
			delete(obj, "sents")
			return
		}
		z := p[1]
		if i == len(pairs)-1 {
			z = len(b)
		} else if z > prefix {
			if z < len(a)-suffix {
				delete(obj, "sents")
				return
			}
			z += delta
		}
		out = append(out, []int{p[0], z})
	}
	obj["sents"] = out
}
