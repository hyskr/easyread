package translate

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

func Workers(setting any, batches int) int {
	k := Num(setting)
	if k > 0 {
		if k > 8 {
			k = 8
		}
		return k
	}
	k = (batches + 1) / 2
	if k > 4 {
		k = 4
	}
	if k < 1 {
		k = 1
	}
	return k
}
func Batches(pages []int, size int, en []int) [][]int {
	if size < 1 {
		size = 1
	}
	out := [][]int{}
	for _, n := range pages {
		if len(out) > 0 {
			b := out[len(out)-1]
			if n == b[len(b)-1]+1 && len(b) < size && contains(en, b[0]) == contains(en, n) {
				out[len(out)-1] = append(b, n)
				continue
			}
		}
		out = append(out, []int{n})
	}
	return out
}

var noiseLine = regexp.MustCompile(`(?i)^\s*(?:\d{1,4}|[ivxl]{1,5}|page \d+.*|arxiv:.*)\s*$`)
var headingLine = regexp.MustCompile(`(?i)^\s*(?:(?:\d+(?:\.\d+)*\.?|[A-Z]\.?)\s+[A-Z][a-z]|\[\d+\]\s|appendix|references|abstract|acknowledg)`)
var wordRE = regexp.MustCompile(`[A-Za-z]{2,}`)
var captionLine = regexp.MustCompile(`(?i)^\s*(?:Table|Figure|Fig\.|Algorithm)\s*\d`)
var footnoteLine = regexp.MustCompile(`^\s*(?:[¹²³⁴⁵⁶⁷⁸⁹*†‡]|\d{1,2}\s*(?:https?|www\.)|\d{1,2}[A-Z][a-z])|https?://`)

func sourceText(root string, n int) string {
	b, _ := os.ReadFile(filepath.Join(root, "extract", fmt.Sprintf("page-%03d.txt", n)))
	return string(b)
}
func sourceLines(root string, n int) []string {
	out := []string{}
	for _, line := range strings.Split(sourceText(root, n), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !noiseLine.MatchString(line) {
			out = append(out, line)
		}
	}
	return out
}
func SeamCost(root string, prev, next int) float64 {
	if next != prev+1 {
		return 0
	}
	tail, head := []string{}, []string{}
	for _, line := range sourceLines(root, prev) {
		if len(wordRE.FindAllString(line, -1)) >= 4 && !footnoteLine.MatchString(line) {
			tail = append(tail, line)
		}
	}
	for _, line := range sourceLines(root, next) {
		if (headingLine.MatchString(line) && len(line) < 80) || (len(wordRE.FindAllString(line, -1)) >= 4 && !footnoteLine.MatchString(line) && !captionLine.MatchString(line)) {
			head = append(head, line)
		}
	}
	if len(tail) == 0 || len(head) == 0 {
		return 2
	}
	first, last := head[0], tail[len(tail)-1]
	if unicode.IsLower([]rune(first)[0]) || strings.HasSuffix(last, "-") || strings.HasSuffix(last, ",") {
		return 3
	}
	if headingLine.MatchString(first) && len(first) < 80 {
		return 0
	}
	if strings.ContainsRune(".?!:", []rune(last)[len([]rune(last))-1]) {
		return 1
	}
	return 2
}
func Plan(pages []int, size, k int, root string) [][]int {
	n := len(pages)
	if n == 0 {
		return nil
	}
	if size < 1 {
		size = 1
	}
	if k <= 1 || n == 1 {
		return [][]int{append([]int{}, pages...)}
	}
	if k > n {
		k = n
	}
	cap := ((n+size-1)/size + k - 1) / k * size
	best := make([][]float64, k+1)
	back := make([][]int, k+1)
	for j := range best {
		best[j] = make([]float64, n+1)
		back[j] = make([]int, n+1)
		for i := range best[j] {
			best[j][i] = math.Inf(1)
		}
	}
	best[0][0] = 0
	seams := make([]float64, n)
	for i := 1; i < n; i++ {
		seams[i] = .5 + SeamCost(root, pages[i-1], pages[i])
	}
	for j := 1; j <= k; j++ {
		for i := 1; i <= n; i++ {
			start := i - cap
			if start < 0 {
				start = 0
			}
			for s := start; s < i; s++ {
				cost := best[j-1][s] + seams[s] + .3*float64((i-s+size-1)/size)
				if cost < best[j][i] {
					best[j][i] = cost
					back[j][i] = s
				}
			}
		}
	}
	j := 1
	for a := 2; a <= k; a++ {
		if best[a][n] < best[j][n] {
			j = a
		}
	}
	out := [][]int{}
	i := n
	for j > 0 {
		s := back[j][i]
		out = append(out, append([]int{}, pages[s:i]...))
		i = s
		j--
	}
	for a, b := 0, len(out)-1; a < b; a, b = a+1, b-1 {
		out[a], out[b] = out[b], out[a]
	}
	return out
}
func FrontContext(root string, first int) string {
	if first <= 1 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nCONTEXT ONLY, DO NOT OUTPUT: prior source text for terminology and continuation.\n")
	if first > 2 {
		head := []rune(sourceText(root, 1))
		if len(head) > 1800 {
			head = head[:1800]
		}
		b.WriteString(string(head))
	}
	toc := []string{}
outer:
	for n := 1; n < first; n++ {
		for _, line := range sourceLines(root, n) {
			if refHeading.MatchString(line) {
				break outer
			}
			if len(line) < 80 && headingLine.MatchString(line) && !strings.HasPrefix(line, "[") {
				toc = append(toc, fmt.Sprintf("%s (page %d)", line, n))
			}
		}
	}
	if len(toc) > 60 {
		toc = toc[len(toc)-60:]
	}
	b.WriteString("\nOutline:\n" + strings.Join(toc, "\n"))
	prev := []rune(sourceText(root, first-1))
	if len(prev) > 5000 {
		prev = prev[len(prev)-5000:]
	}
	fmt.Fprintf(&b, "\nPrevious page %d:\n%s\nEND CONTEXT\n", first-1, string(prev))
	return b.String()
}
