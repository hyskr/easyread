package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hyskr/easyread/backend/store"
	"reflect"
	"regexp"
	"strings"
	"time"
)

var bareTex = regexp.MustCompile(`\\(?:[dt]?frac|sqrt|sum|prod|int|lim|alpha|beta|gamma|delta|theta|lambda|sigma|omega|mathbb|mathcal|mathbf|mathrm|operatorname|left|right|begin|end|hat|bar|vec|infty|partial|nabla|times|cdot|leq|geq)\b|[A-Za-z][_\^]\{[^{}\n]+\}`)

func textProblems(s string) []string {
	out := []string{}
	mask := protected(s)
	r := []rune(s)
	var prose strings.Builder
	dollars := 0
	for i, c := range r {
		if mask[i] {
			prose.WriteRune(' ')
			continue
		}
		prose.WriteRune(c)
		if c == '$' && (i == 0 || r[i-1] != '\\') {
			dollars++
		}
		if (c >= 0 && c <= 8) || c == 11 || c == 12 || (c >= 14 && c <= 31) {
			out = append(out, "control character in text (JSON TeX escape likely corrupted)")
		}
	}
	if dollars%2 != 0 {
		out = append(out, "unpaired $ math delimiter")
	}
	if bareTex.MatchString(prose.String()) {
		out = append(out, "TeX formula outside math delimiters; wrap complete formula in $TeX$ or a math block")
	}
	return out
}

// Problems validates renderer-facing shapes and common corrupted TeX. It never
// guesses formula boundaries or silently drops malformed content.
func Problems(data Object) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range List(data["blocks"]) {
		b := Obj(raw)
		id := Str(b["id"])
		if id == "" {
			out = append(out, "block missing id")
		}
		if seen[id] {
			out = append(out, id+": duplicate id")
		}
		seen[id] = true
		t := Str(b["type"])
		if !types[t] && t != "code" && t != "listing" {
			out = append(out, id+": unknown block type")
		}
		texts := []string{}
		for _, f := range []string{"en", "zh", "caption_en", "caption_zh", "image_en", "image_zh"} {
			texts = append(texts, Str(b[f]))
		}
		for _, raw := range List(b["items"]) {
			it, ok := raw.(map[string]any)
			if !ok {
				out = append(out, id+": list item must be an object")
				continue
			}
			texts = append(texts, Str(it["en"]), Str(it["zh"]))
		}
		if t == "table" {
			for _, f := range []string{"head", "rows"} {
				if b[f] == nil {
					continue
				}
				rows, ok := b[f].([]any)
				if !ok {
					out = append(out, id+": table."+f+" must be a matrix")
					continue
				}
				for _, row := range rows {
					cells, ok := row.([]any)
					if !ok {
						out = append(out, id+": table."+f+" must be a matrix")
						continue
					}
					for _, cell := range cells {
						texts = append(texts, fmt.Sprint(cell))
					}
				}
			}
		}
		if t == "code" || t == "listing" {
			continue
		}
		for _, text := range texts {
			for _, problem := range textProblems(text) {
				out = append(out, id+": "+problem)
			}
		}
	}
	for key, value := range Obj(data["zh"]) {
		if strings.HasSuffix(key, "#head") {
			rows, ok := value.([]any)
			if !ok {
				out = append(out, key+": table head must be a matrix")
				continue
			}
			for _, row := range rows {
				cells, ok := row.([]any)
				if !ok {
					out = append(out, key+": table head must be a matrix")
					continue
				}
				for _, v := range cells {
					for _, p := range textProblems(fmt.Sprint(v)) {
						out = append(out, key+": "+p)
					}
				}
			}
		} else if text, ok := value.(string); ok {
			for _, p := range textProblems(text) {
				out = append(out, key+": "+p)
			}
		}
	}
	return out
}
func retains(original, repaired any) bool {
	switch a := original.(type) {
	case map[string]any:
		b, ok := repaired.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range a {
			if v == nil || v == "" {
				continue
			}
			if _, ok := b[k]; !ok || !retains(v, b[k]) {
				return false
			}
		}
		return true
	case []any:
		b, ok := repaired.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !retains(a[i], b[i]) {
				return false
			}
		}
		return true
	case string:
		b, ok := repaired.(string)
		return ok && (strings.TrimSpace(a) == "" || strings.TrimSpace(b) != "")
	default:
		return reflect.TypeOf(original) == reflect.TypeOf(repaired)
	}
}
func RetainsContent(original, repaired Object) bool {
	if !retains(original, repaired) {
		return false
	}
	a, b := List(original["blocks"]), List(repaired["blocks"])
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		for _, k := range []string{"id", "type", "page"} {
			if !reflect.DeepEqual(Obj(a[i])[k], Obj(b[i])[k]) {
				return false
			}
		}
	}
	return true
}
func repair(ctx context.Context, ws *store.Workspace, cfg, data Object, read bool, usage func(Object)) Object {
	problems := Problems(data)
	if len(problems) == 0 {
		return data
	}
	raw, _ := json.Marshal(data)
	list, _ := json.Marshal(problems)
	prompt := fmt.Sprintf("Repair only the listed formatting/math/JSON shape problems in this translation. Preserve every block ID, type, page, original and translated content, list entry, table cell, reference and required field. Never summarize, remove content, or change meaning. Return only the corrected JSON object.\nProblems: %s\nOriginal JSON: %s", list, raw)
	fixed, u, e := Run(ctx, cfg, prompt, ws.Root, nil)
	if u != nil {
		usage(u)
	}
	if e != nil {
		Journal(ws, "Format repair failed; preserving original: "+e.Error())
		return data
	}
	v, e := ParseJSON(fixed)
	if e == nil && RetainsContent(data, v) && len(Problems(v)) < len(problems) {
		return v
	}
	Journal(ws, "Rejected lossy or ineffective formatting repair; preserving original")
	return data
}
func SaveChecks(ws *store.Workspace, checks any, pages []int) error {
	p, e := ws.Load("paper")
	if e != nil {
		return e
	}
	by := map[string]int{}
	for _, x := range List(p["blocks"]) {
		b := Obj(x)
		by[Str(b["id"])] = Num(b["page"])
	}
	return ws.Update("discussion", func(d Object) error {
		entries := []any{}
		for _, x := range List(d["entries"]) {
			it := Obj(x)
			if it["kind"] == "check" && it["by"] == "translator" && contains(pages, by[Str(it["anchor"])]) {
				continue
			}
			entries = append(entries, it)
		}
		for i, x := range List(checks) {
			c := Obj(x)
			anchor := Str(c["anchor"])
			if by[anchor] == 0 || strings.TrimSpace(Str(c["body"])) == "" {
				continue
			}
			quote, _ := Unmark(Str(c["quote"]))
			q := []rune(quote)
			if len(q) > 200 {
				q = q[:200]
			}
			title := []rune(Str(c["title"]))
			if len(title) > 80 {
				title = title[:80]
			}
			entries = append(entries, Object{"id": fmt.Sprintf("check-%d-%d", time.Now().UnixNano(), i), "kind": "check", "by": "translator", "anchor": anchor, "quote": string(q), "title": string(title), "body": c["body"], "at": time.Now().UTC().Format(time.RFC3339)})
		}
		d["entries"] = entries
		return nil
	})
}
