package translate

import (
	"github.com/hyskr/easyread/backend/store"
	"sort"
)

func coordinates(v any) []float64 {
	if a, ok := v.([]float64); ok {
		return a
	}
	a := []float64{}
	for _, x := range List(v) {
		switch n := x.(type) {
		case float64:
			a = append(a, n)
		case int:
			a = append(a, float64(n))
		default:
			return nil
		}
	}
	if len(a) != 4 {
		return nil
	}
	return a
}
func overlapX(a []float64, x0, x1 float64) bool { return max(a[0], x0) < min(a[2], x1)-.01 }
func afterFloat(loc Object, page int, box []float64) bool {
	if Num(loc["page"]) != page {
		return Num(loc["page"]) > page
	}
	src := Str(loc["src"])
	if src != "text" && src != "head" && src != "graphic" && src != "caption" && src != "manual" {
		return false
	}
	top := coordinates(loc["box"])
	if boxes := List(loc["boxes"]); len(boxes) > 0 {
		top = coordinates(boxes[0])
	}
	if len(top) != 4 {
		return false
	}
	wide := box[2]-box[0] > .5
	if !wide && top[2]-top[0] <= .5 && !overlapX(top, box[0], box[2]) {
		return top[0] >= .45 && box[2] <= .55
	}
	return top[1] >= box[1]-.005 && (wide || overlapX(top, box[0], box[2]))
}
func FloatOrder(blocks []any, layout Object) []string {
	moving, rest := []any{}, []any{}
	for _, x := range blocks {
		b := Obj(x)
		loc := Obj(layout[Str(b["id"])])
		if (b["type"] == "table" || b["type"] == "figure") && len(coordinates(loc["box"])) == 4 {
			moving = append(moving, b)
		} else {
			rest = append(rest, b)
		}
	}
	if len(moving) == 0 {
		return nil
	}
	for _, x := range moving {
		f := Obj(x)
		loc := Obj(layout[Str(f["id"])])
		page, box := Num(loc["page"]), coordinates(loc["box"])
		pos := len(rest)
		for i, x := range rest {
			b := Obj(x)
			other := Obj(layout[Str(b["id"])])
			if len(other) == 0 {
				if Num(b["page"]) > page {
					pos = i
					break
				}
				continue
			}
			if afterFloat(other, page, box) {
				pos = i
				break
			}
		}
		start := pos
		for start > 0 {
			prev := Obj(rest[start-1])
			if layout[Str(prev["id"])] != nil || Num(prev["page"]) != page {
				break
			}
			start--
		}
		if start != pos {
			top, bottom := .08, .92
			if start > 0 {
				prev := Obj(layout[Str(Obj(rest[start-1])["id"])])
				if Num(prev["page"]) == page && len(coordinates(prev["box"])) == 4 {
					top = coordinates(prev["box"])[3]
				}
			}
			if pos < len(rest) {
				next := Obj(layout[Str(Obj(rest[pos])["id"])])
				if Num(next["page"]) == page && len(coordinates(next["box"])) == 4 {
					bottom = coordinates(next["box"])[1]
				}
			}
			if bottom-box[3] > box[1]-top {
				pos = start
			}
		}
		rest = append(rest, nil)
		copy(rest[pos+1:], rest[pos:])
		rest[pos] = f
	}
	ids := []string{}
	changed := false
	for i, x := range rest {
		id := Str(Obj(x)["id"])
		ids = append(ids, id)
		if id != Str(Obj(blocks[i])["id"]) {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return ids
}
func ReorderFloats(ws *store.Workspace) error {
	paper, e := ws.Load("paper")
	if e != nil {
		return e
	}
	layout, e := ws.Load("layout")
	if e != nil {
		return e
	}
	ids := FloatOrder(List(paper["blocks"]), layout)
	if len(ids) == 0 {
		return nil
	}
	return ws.Update("paper", func(p Object) error {
		rank := map[string]int{}
		for i, id := range ids {
			rank[id] = i
		}
		known := []any{}
		blocks := List(p["blocks"])
		for _, x := range blocks {
			if _, ok := rank[Str(Obj(x)["id"])]; ok {
				known = append(known, x)
			}
		}
		sort.SliceStable(known, func(i, j int) bool { return rank[Str(Obj(known[i])["id"])] < rank[Str(Obj(known[j])["id"])] })
		j := 0
		for i, x := range blocks {
			if _, ok := rank[Str(Obj(x)["id"])]; ok {
				blocks[i] = known[j]
				j++
			}
		}
		p["blocks"] = blocks
		return nil
	})
}
