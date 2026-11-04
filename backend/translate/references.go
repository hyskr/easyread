package translate

import (
	"crypto/sha1"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

func referenceContentID(text string) string {
	letters := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, text)
	return fmt.Sprintf("r%x", sha1.Sum([]byte(letters)))[:9]
}
func mergeReferences(existing, incoming []any) []any {
	type entry struct {
		text, key string
		data      Object
	}
	entries := []entry{}
	index := map[string]int{}
	variants := map[string]int{}
	for _, raw := range append(append([]any{}, existing...), incoming...) {
		source, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		text := strings.Join(strings.Fields(Str(source["text"])), " ")
		if text == "" {
			continue
		}
		key := strings.TrimSpace(Str(source["cite_key"]))
		if key == "" {
			key = strings.TrimSpace(Str(source["id"]))
			if strings.TrimRight(key, "x") == referenceContentID(text) {
				key = ""
			}
		}
		combined := text + "\x00" + key
		if pos, ok := index[combined]; ok {
			page := Num(source["page"])
			if page > 0 && (Num(entries[pos].data["page"]) == 0 || page < Num(entries[pos].data["page"])) {
				entries[pos].data["page"] = page
			}
			continue
		}
		copy := Object{}
		for k, v := range source {
			copy[k] = v
		}
		index[combined] = len(entries)
		entries = append(entries, entry{text, key, copy})
		variants[text]++
	}
	counts := map[string]int{}
	for _, e := range entries {
		if e.key != "" {
			counts[e.key]++
		}
	}
	used := map[string]bool{}
	for k := range counts {
		used[k] = true
	}
	result := []any{}
	for _, e := range entries {
		if e.key == "" && variants[e.text] > 1 {
			continue
		}
		if e.key != "" && counts[e.key] == 1 {
			e.data["id"] = e.key
		} else {
			if e.key != "" {
				e.data["cite_key"] = e.key
			}
			id := referenceContentID(e.text)
			for used[id] {
				id += "x"
			}
			used[id] = true
			e.data["id"] = id
		}
		result = append(result, e.data)
	}
	sort.SliceStable(result, func(i, j int) bool { return Num(Obj(result[i])["page"]) < Num(Obj(result[j])["page"]) })
	return result
}
