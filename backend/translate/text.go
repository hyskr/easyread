package translate

import (
	"strings"
	"unicode"
)

// protected marks code and TeX spans so sentence/terminology operations never
// rewrite program literals, mathematical norms, or punctuation in formulas.
func protected(text string) []bool {
	r := []rune(text)
	mask := make([]bool, len(r))
	for i := 0; i < len(r); i++ {
		if r[i] != '`' && r[i] != '$' {
			continue
		}
		if i > 0 && r[i-1] == '\\' {
			continue
		}
		c := r[i]
		width := 1
		for i+width < len(r) && r[i+width] == c {
			width++
		}
		if c == '$' && width > 2 {
			width = 2
		}
		end := -1
		for j := i + width; j < len(r); j++ {
			if r[j] != c || (j > 0 && r[j-1] == '\\') {
				continue
			}
			n := 1
			for j+n < len(r) && r[j+n] == c {
				n++
			}
			if n == width {
				end = j + width
				break
			}
			j += n - 1
		}
		if end < 0 {
			continue
		}
		for k := i; k < end; k++ {
			mask[k] = true
		}
		i = end - 1
	}
	return mask
}

var abbreviations = map[string]bool{"e.g": true, "i.e": true, "al": true, "fig": true, "figs": true, "eq": true, "eqs": true, "sec": true, "secs": true, "tab": true, "ref": true, "refs": true, "vs": true, "cf": true, "etc": true, "approx": true, "no": true, "dr": true, "mr": true, "ms": true, "prof": true, "st": true, "app": true, "ch": true, "thm": true, "lem": true, "def": true, "prop": true, "cor": true}

func MarkEnglish(text string) (string, []int) {
	r := []rune(text)
	mask := protected(text)
	cuts := []int{}
	for i, c := range r {
		if mask[i] || !strings.ContainsRune(".!?", c) {
			continue
		}
		end := i + 1
		for end < len(r) && strings.ContainsRune("\"'”’)]", r[end]) {
			end++
		}
		if end >= len(r) || !unicode.IsSpace(r[end]) {
			continue
		}
		next := end
		for next < len(r) && unicode.IsSpace(r[next]) {
			next++
		}
		if next == len(r) || !(unicode.IsUpper(r[next]) || unicode.IsDigit(r[next]) || strings.ContainsRune("\"'“([$", r[next])) {
			continue
		}
		start := i - 1
		for start >= 0 && (unicode.IsLetter(r[start]) || r[start] == '.') {
			start--
		}
		word := string(r[start+1 : i])
		if abbreviations[strings.ToLower(strings.Trim(word, "."))] || (len([]rune(word)) == 1 && unicode.IsUpper([]rune(word)[0])) {
			continue
		}
		if i > 0 && unicode.IsDigit(r[i-1]) && unicode.IsDigit(r[next]) {
			continue
		}
		cuts = append(cuts, end)
	}
	if len(cuts) == 0 {
		return text, nil
	}
	var b strings.Builder
	ends := []int{}
	last := 0
	for _, c := range cuts {
		b.WriteString(strings.TrimRightFunc(string(r[last:c]), unicode.IsSpace))
		b.WriteString(" ‖ ")
		ends = append(ends, u16(strings.TrimRightFunc(string(r[:c]), unicode.IsSpace)))
		last = c
		for last < len(r) && unicode.IsSpace(r[last]) {
			last++
		}
	}
	b.WriteString(string(r[last:]))
	return b.String(), ends
}
func alignedFill(obj Object, field, marked string) {
	clean, ze := Unmark(marked)
	obj[field] = clean
	delete(obj, "sents")
	if field != "zh" {
		return
	}
	en := Str(obj["en"])
	_, ee := MarkEnglish(en)
	if len(ee) == 0 || len(ee) != len(ze) || strings.Contains(en, "```") {
		return
	}
	pairs := [][]int{}
	for i := range ee {
		pairs = append(pairs, []int{ee[i], ze[i]})
	}
	obj["sents"] = append(pairs, []int{u16(en), u16(clean)})
}
func replaceProse(text, old, new string) string {
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
			part = strings.ReplaceAll(part, old, new)
		}
		b.WriteString(part)
		i = j
	}
	return b.String()
}
func normalizeCode(b Object) bool {
	source := Str(b["en"])
	if strings.TrimSpace(source) == "" {
		source = Str(b["code"])
	}
	if source == "" {
		lines := List(b["lines"])
		a := []string{}
		for _, x := range lines {
			s, ok := x.(string)
			if !ok {
				return false
			}
			a = append(a, s)
		}
		source = strings.Join(a, "\n")
	}
	if strings.TrimSpace(source) == "" {
		return false
	}
	language := Str(b["lang"])
	if language == "" {
		language = Str(b["language"])
	}
	for _, r := range language {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '+' && r != '-' {
			language = ""
			break
		}
	}
	wrap := func(s string) string {
		if strings.HasPrefix(strings.TrimSpace(s), "```") {
			return s
		}
		maxRun, run := 2, 0
		for _, r := range s {
			if r == '`' {
				run++
				if run > maxRun {
					maxRun = run
				}
			} else {
				run = 0
			}
		}
		fence := strings.Repeat("`", maxRun+1)
		return fence + language + "\n" + s + "\n" + fence
	}
	b["en"] = wrap(source)
	if caption := Str(b["caption_en"]); caption != "" {
		b["en"] = Str(b["en"]) + "\n\n" + caption
	}
	zh := Str(b["zh"])
	if zh == "" && Str(b["caption_zh"]) != "" {
		zh = source
	}
	if zh != "" {
		b["zh"] = wrap(zh)
		if caption := Str(b["caption_zh"]); caption != "" {
			b["zh"] = Str(b["zh"]) + "\n\n" + caption
		}
	}
	b["type"] = "para"
	delete(b, "sents")
	return true
}
func ValidAlignment(en, zh string, value any) bool {
	pairs := [][]int{}
	switch v := value.(type) {
	case [][]int:
		pairs = v
	case []any:
		for _, x := range v {
			p := ints(x)
			if len(p) != 2 {
				return false
			}
			pairs = append(pairs, p)
		}
	default:
		return false
	}
	if len(pairs) < 2 {
		return false
	}
	last := pairs[len(pairs)-1]
	if len(last) != 2 || last[0] != u16(en) || last[1] != u16(zh) {
		return false
	}
	prevA, prevB := 0, 0
	for _, p := range pairs {
		if len(p) != 2 || p[0] <= prevA || p[1] <= prevB {
			return false
		}
		prevA, prevB = p[0], p[1]
	}
	return true
}
