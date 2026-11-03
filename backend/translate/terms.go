package translate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

func cjk(s string) bool {
	for _, r := range s {
		if (r >= 0x3040 && r <= 0x30ff) || (r >= 0x3400 && r <= 0x9fff) || (r >= 0xac00 && r <= 0xd7af) {
			return true
		}
	}
	return false
}
func prose(s string) string {
	mask := protected(s)
	r := []rune(s)
	for i := range r {
		if mask[i] {
			r[i] = ' '
		}
	}
	return string(r)
}
func wordChar(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }
func occurrences(s, word string, fold, plural bool) int {
	if word == "" {
		return 0
	}
	if cjk(word) {
		return strings.Count(s, word)
	}
	if fold {
		s = strings.ToLower(s)
		word = strings.ToLower(word)
	}
	r, w := []rune(s), []rune(word)
	n := 0
	for i := 0; i+len(w) <= len(r); i++ {
		if i > 0 && wordChar(r[i-1]) {
			continue
		}
		if string(r[i:i+len(w)]) != word {
			continue
		}
		j := i + len(w)
		if plural {
			if j < len(r) && r[j] == 's' {
				j++
			} else if j+1 < len(r) && r[j] == 'e' && r[j+1] == 's' {
				j += 2
			}
		}
		if j < len(r) && wordChar(r[j]) {
			continue
		}
		n++
		i = j - 1
	}
	return n
}
func protectedTerms(s, mine string, keep []string) (string, map[string]string) {
	marks := map[string]string{}
	sort.Slice(keep, func(i, j int) bool { return len(keep[i]) > len(keep[j]) })
	for i, k := range keep {
		if k != mine && strings.Contains(k, mine) {
			marker := fmt.Sprintf("\x00%d\x01", i)
			s = strings.ReplaceAll(s, k, marker)
			marks[marker] = k
		}
	}
	return s, marks
}
func swapTerm(source, text, en, mine, old string, known []string) string {
	n := occurrences(prose(source), en, true, true)
	if n == 0 {
		return text
	}
	p, _ := protectedTerms(prose(text), mine, known)
	if occurrences(p, mine, false, false) != n {
		return text
	}
	r := []rune(text)
	mask := protected(text)
	var b strings.Builder
	for i := 0; i < len(r); {
		j := i + 1
		for j < len(r) && mask[j] == mask[i] {
			j++
		}
		part := string(r[i:j])
		if !mask[i] {
			var marks map[string]string
			part, marks = protectedTerms(part, mine, known)
			if cjk(mine) {
				part = strings.ReplaceAll(part, mine, old)
			} else {
				pattern := regexp.MustCompile(`(^|[^\pL\pN_])` + regexp.QuoteMeta(mine) + `([^\pL\pN_]|$)`)
				part = pattern.ReplaceAllStringFunc(part, func(match string) string { return strings.Replace(match, mine, old, 1) })
			}
			for marker, k := range marks {
				part = strings.ReplaceAll(part, marker, k)
			}
		}
		b.WriteString(part)
		i = j
	}
	return b.String()
}
func UnifyTerms(paper, data Object) { UnifyTermsWithSources(paper, data, nil) }
func UnifyTermsWithSources(paper, data, enOf Object) {
	known := map[string]string{}
	keep := []string{}
	for _, x := range append(append([]any{}, List(paper["glossary"])...), List(data["glossary"])...) {
		b := Obj(x)
		keep = append(keep, Str(b["zh"]))
	}
	for _, x := range List(paper["glossary"]) {
		b := Obj(x)
		known[strings.Join(strings.Fields(strings.ToLower(Str(b["en"]))), " ")] = Str(b["zh"])
	}
	remaining := []any{}
	for _, x := range List(data["glossary"]) {
		b := Obj(x)
		en, mine := Str(b["en"]), Str(b["zh"])
		old := known[strings.Join(strings.Fields(strings.ToLower(en)), " ")]
		minLen := 2
		if strings.Contains(old, mine) {
			minLen = 3
		}
		if old == "" || old == mine || len([]rune(mine)) < minLen || strings.ContainsAny(old+mine, "（()）") {
			remaining = append(remaining, b)
			continue
		}
		fix := func(v Object, src, dst string) {
			s, ok := v[dst].(string)
			if !ok {
				return
			}
			changed := swapTerm(Str(v[src]), s, en, mine, old, keep)
			if changed != s {
				v[dst] = changed
				delete(v, "sents")
			}
		}
		for _, x := range List(data["blocks"]) {
			v := Obj(x)
			fix(v, "en", "zh")
			fix(v, "caption_en", "caption_zh")
			fix(v, "image_en", "image_zh")
			for _, x := range List(v["items"]) {
				fix(Obj(x), "en", "zh")
			}
			if v["type"] == "table" {
				raw := Str(v["caption_en"]) + " " + Str(v["en"])
				if occurrences(raw, en, true, true) > 0 {
					for _, f := range []string{"head", "rows"} {
						for _, row := range List(v[f]) {
							cells := List(row)
							for i, cell := range cells {
								if strings.TrimSpace(Str(cell)) == mine {
									cells[i] = old
								}
							}
						}
					}
				}
			}
		}
		for key, value := range Obj(data["zh"]) {
			if s, ok := value.(string); ok {
				Obj(data["zh"])[key] = swapTerm(Str(enOf[key]), s, en, mine, old, keep)
			}
		}
	}
	if data["glossary"] != nil {
		data["glossary"] = remaining
	}
}
