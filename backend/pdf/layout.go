package pdf

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/hyskr/easyread/backend/store"
)

var layoutGate = make(chan struct{}, 1)

type glyph struct {
	Text string
	Box  []float64
}
type stream struct {
	Text  []rune
	Index []int
	Chars []glyph
	Page  int
}

var markdownLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
var inlineMath = regexp.MustCompile(`\$[^$]*\$`)

func normalize(s string) []rune {
	s = markdownLink.ReplaceAllString(s, "$1")
	s = inlineMath.ReplaceAllString(s, "")
	s = strings.NewReplacer("ﬁ", "fi", "ﬂ", "fl", "ﬀ", "ff", "ﬃ", "ffi", "ﬄ", "ffl", "ﬅ", "st", "ﬆ", "st").Replace(strings.ToLower(s))
	out := []rune{}
	for _, r := range s {
		if r >= 'Ａ' && r <= 'Ｚ' {
			r = r - 'Ａ' + 'a'
		}
		if r >= 'ａ' && r <= 'ｚ' {
			r = r - 'ａ' + 'a'
		}
		if r >= '０' && r <= '９' {
			r = r - '０' + '0'
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			out = append(out, r)
		}
	}
	return out
}
func loadStream(root string, page int) (stream, error) {
	s := stream{Page: page}
	b, e := os.ReadFile(filepath.Join(root, "extract", fmt.Sprintf("page-%03d.chars.json", page)))
	if e != nil {
		return s, e
	}
	var rows [][]any
	if e = json.Unmarshal(b, &rows); e != nil {
		return s, e
	}
	for _, row := range rows {
		if len(row) != 5 {
			continue
		}
		text, _ := row[0].(string)
		box := []float64{number(row[1]), number(row[2]), number(row[3]), number(row[4])}
		s.Chars = append(s.Chars, glyph{text, box})
		for _, r := range normalize(text) {
			s.Text = append(s.Text, r)
			s.Index = append(s.Index, len(s.Chars)-1)
		}
	}
	return s, nil
}
func english(b map[string]any) string {
	key := "en"
	if b["type"] == "figure" || b["type"] == "table" {
		key = "caption_en"
	}
	if b["type"] == "list" {
		s := []string{}
		if items, ok := b["items"].([]any); ok {
			for _, v := range items {
				if item, ok := v.(map[string]any); ok {
					if en, ok := item["en"].(string); ok {
						s = append(s, en)
					}
				}
			}
		}
		return strings.Join(s, " ")
	}
	s, _ := b[key].(string)
	return s
}
func indexRunes(hay, needle []rune, start int) int {
	if len(needle) == 0 {
		return -1
	}
	for i := max(start, 0); i+len(needle) <= len(hay); i++ {
		ok := true
		for j, r := range needle {
			if hay[i+j] != r {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
func bounds(boxes [][]float64) []float64 {
	if len(boxes) == 0 {
		return nil
	}
	b := append([]float64{}, boxes[0]...)
	for _, r := range boxes[1:] {
		b[0] = math.Min(b[0], r[0])
		b[1] = math.Min(b[1], r[1])
		b[2] = math.Max(b[2], r[2])
		b[3] = math.Max(b[3], r[3])
	}
	return b
}
func regionBoxes(s stream, a, b int) [][]float64 {
	boxes := [][]float64{}
	seen := map[int]bool{}
	for i := max(0, a); i < min(b, len(s.Index)); i++ {
		idx := s.Index[i]
		g := s.Chars[idx]
		if seen[idx] || g.Box[2] <= g.Box[0] || g.Box[3] <= g.Box[1] {
			continue
		}
		seen[idx] = true
		boxes = append(boxes, append([]float64{}, g.Box...))
	}
	// Group measured glyphs by line and connected horizontal runs. This preserves
	// separate columns and avoids covering a figure between text fragments.
	sort.Slice(boxes, func(i, j int) bool {
		if math.Abs(boxes[i][1]-boxes[j][1]) > .004 {
			return boxes[i][1] < boxes[j][1]
		}
		return boxes[i][0] < boxes[j][0]
	})
	lines := [][]float64{}
	for _, b := range boxes {
		merged := false
		for i := len(lines) - 1; i >= 0; i-- {
			r := lines[i]
			if b[1]-r[3] > .015 {
				break
			}
			if math.Abs(b[1]-r[1]) < .008 && b[0] <= r[2]+.018 && b[2] >= r[0]-.018 {
				lines[i] = bounds([][]float64{r, b})
				merged = true
				break
			}
		}
		if !merged {
			lines = append(lines, b)
		}
	}
	return lines
}
func locateBlock(b map[string]any, s, next stream, cursor int) map[string]any {
	page := int(number(b["page"]))
	manual := boxValue(b["box"])
	if manual != nil && b["type"] != "figure" {
		return map[string]any{"page": page, "box": manual, "src": "manual"}
	}
	en := normalize(english(b))
	if len(en) == 0 {
		if manual != nil {
			return map[string]any{"page": page, "box": manual, "src": "manual"}
		}
		return nil
	}
	all := append(append([]rune{}, s.Text...), next.Text...)
	head := en[:min(28, len(en))]
	tail := en[max(0, len(en)-28):]
	a := indexRunes(all, head, cursor)
	if a < 0 {
		a = indexRunes(all, head, 0)
	}
	if a < 0 {
		if manual != nil {
			return map[string]any{"page": page, "box": manual, "src": "manual"}
		}
		return nil
	}
	end := indexRunes(all, tail, a)
	source := "text"
	if end < 0 {
		end = min(len(all), a+len(en))
		source = "head"
	} else {
		end += len(tail)
	}
	regions := []map[string]any{}
	for _, item := range []struct {
		s      stream
		offset int
	}{{s, 0}, {next, len(s.Text)}} {
		lo := max(a-item.offset, 0)
		hi := min(end-item.offset, len(item.s.Text))
		if hi <= lo {
			continue
		}
		boxes := regionBoxes(item.s, lo, hi)
		if len(boxes) > 0 {
			regions = append(regions, map[string]any{"page": item.s.Page, "box": bounds(boxes), "boxes": boxes})
		}
	}
	if len(regions) == 0 {
		return nil
	}
	loc := map[string]any{"page": regions[0]["page"], "box": regions[0]["box"], "boxes": regions[0]["boxes"], "src": source}
	if len(regions) > 1 {
		loc["regions"] = regions
	}
	if b["type"] == "figure" && manual != nil {
		loc["crop"] = manual
		loc["box"] = bounds([][]float64{manual, loc["box"].([]float64)})
	}
	return loc
}

// RefreshLayout derives reading locations and automatic figure crops. All
// workspace edits use the store's merge lock; user-selected crops are preserved.
func RefreshLayout(ctx context.Context, ws *store.Workspace) error {
	if e := secureDirs(ws.Root, "extract"); e != nil {
		return e
	}
	select {
	case layoutGate <- struct{}{}:
		defer func() { <-layoutGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	paper, e := ws.Load("paper")
	if e != nil {
		return e
	}
	layout := map[string]any{}
	cache := map[int]stream{}
	get := func(n int) stream {
		if s, ok := cache[n]; ok {
			return s
		}
		s, _ := loadStream(ws.Root, n)
		if len(cache) > 2 {
			cache = map[int]stream{}
		}
		cache[n] = s
		return s
	}
	bs := blocks(paper)
	for _, b := range bs {
		if e = ctx.Err(); e != nil {
			return e
		}
		id, _ := b["id"].(string)
		page := int(number(b["page"]))
		if id == "" || page < 1 {
			continue
		}
		loc := locateBlock(b, get(page), get(page+1), 0)
		if loc != nil {
			layout[id] = loc
		}
	}
	for i, b := range bs {
		id, _ := b["id"].(string)
		page := int(number(b["page"]))
		if id == "" || page < 1 || layout[id] != nil {
			continue
		}
		top, bottom := .08, .92
		x0, x1 := .12, .88
		for j := i - 1; j >= 0; j-- {
			if loc, ok := layout[fmt.Sprint(bs[j]["id"])].(map[string]any); ok && int(number(loc["page"])) == page {
				box := loc["box"].([]float64)
				top = box[3] + .002
				x0, x1 = box[0], box[2]
				break
			}
		}
		for j := i + 1; j < len(bs); j++ {
			if loc, ok := layout[fmt.Sprint(bs[j]["id"])].(map[string]any); ok && int(number(loc["page"])) == page {
				box := loc["box"].([]float64)
				if box[2] > x0 && box[0] < x1 {
					bottom = box[1] - .002
				}
				break
			}
		}
		if bottom <= top {
			bottom = math.Min(1, top+.04)
		}
		if top < bottom {
			layout[id] = map[string]any{"page": page, "box": []float64{x0, top, x1, bottom}, "src": "between"}
		}
	}
	for _, b := range bs {
		if b["type"] != "figure" || b["manual_crop"] != nil || boxValue(b["box"]) != nil {
			continue
		}
		if loc, ok := layout[fmt.Sprint(b["id"])].(map[string]any); ok {
			if crop := automaticFigureBox(ctx, ws, b, loc, layout, bs); crop != nil {
				loc["crop"] = crop
				loc["box"] = bounds([][]float64{crop, loc["box"].([]float64)})
			}
		}
	}
	if e = writeJSON(filepath.Join(ws.Root, "layout.json"), layout); e != nil {
		return e
	}
	if e = atomicFile(filepath.Join(ws.Root, "extract", "locate.version"), []byte("go-poppler-1")); e != nil {
		return e
	}
	type cropResult struct {
		src      string
		box      []float64
		old      string
		snapshot string
	}
	crops := map[string]cropResult{}
	for _, b := range bs {
		if b["type"] != "figure" || b["manual_crop"] != nil {
			continue
		}
		id, _ := b["id"].(string)
		old, _ := b["src"].(string)
		if old != "" && !strings.HasPrefix(old, "figures/crop-") {
			continue
		}
		box := boxValue(b["box"])
		if box == nil {
			if loc, ok := layout[id].(map[string]any); ok {
				box = boxValue(loc["crop"])
			}
		}
		if box == nil {
			continue
		}
		src, err := Crop(ctx, ws, int(number(b["page"])), box, "")
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		snapshot, _ := json.Marshal(b)
		crops[id] = cropResult{src, box, old, string(snapshot)}
	}
	if len(crops) > 0 {
		e = ws.Update("paper", func(p map[string]any) error {
			for _, b := range blocks(p) {
				id, _ := b["id"].(string)
				r, ok := crops[id]
				old, _ := b["src"].(string)
				live, _ := json.Marshal(b)
				if ok && b["manual_crop"] == nil && old == r.old && string(live) == r.snapshot {
					b["src"] = r.src
					b["abox"] = r.box
				}
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	if e = BackfillLinks(ctx, ws); e != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}
