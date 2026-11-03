package pdf

import (
	"context"
	"fmt"
	"image"
	"math"
	"path/filepath"

	"github.com/hyskr/easyread/backend/store"
)

// automaticFigureBox locates visible non-text content above a measured figure
// caption. It deliberately abstains when no sufficiently sized graphic exists.
// Unlike object-stream heuristics this also handles scanned and vector figures.
func automaticFigureBox(ctx context.Context, ws *store.Workspace, b map[string]any, loc map[string]any, layout map[string]any, bs []map[string]any) []float64 {
	if e := acquireImage(ctx); e != nil {
		return nil
	}
	defer releaseImage()
	if loc["src"] != "text" && loc["src"] != "head" {
		return nil
	}
	caption, ok := loc["box"].([]float64)
	if !ok {
		return nil
	}
	page := int(number(loc["page"]))
	x0, x1 := .04, .96
	if caption[2]-caption[0] < .48 {
		if (caption[0]+caption[2])/2 < .5 {
			x1 = .5
		} else {
			x0 = .5
		}
	}
	top, bottom := .04, caption[1]-.006
	for _, other := range bs {
		if other["id"] == b["id"] || other["type"] == "figure" {
			continue
		}
		o, ok := layout[fmt.Sprint(other["id"])].(map[string]any)
		if !ok || int(number(o["page"])) != page {
			continue
		}
		r, ok := o["box"].([]float64)
		if ok && r[2] > x0 && r[0] < x1 && r[3] < bottom && r[3] > top {
			top = r[3] + .008
		}
	}
	if bottom-top < .04 {
		return nil
	}
	im, e := decodeImage(filepath.Join(ws.Root, "pages", fmt.Sprintf("page-%03d.png", page)))
	if e != nil {
		return nil
	}
	ib := im.Bounds()
	w, h := float64(ib.Dx()), float64(ib.Dy())
	roi := image.Rect(int(x0*w), int(top*h), int(x1*w), int(bottom*h)).Intersect(ib)
	if roi.Empty() {
		return nil
	}
	// Mask text word rows so a running header or caption cannot look like a graphic.
	s, _ := loadStream(ws.Root, page)
	mask := make([][]image.Rectangle, ib.Dy())
	for _, g := range s.Chars {
		r := g.Box
		if r[2] <= r[0] || r[3] <= r[1] {
			continue
		}
		rect := image.Rect(int(r[0]*w)-2, int(r[1]*h)-2, int(math.Ceil(r[2]*w))+2, int(math.Ceil(r[3]*h))+2).Intersect(roi)
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			mask[y] = append(mask[y], rect)
		}
	}
	left, right, upper, lower := roi.Max.X, roi.Min.X, roi.Max.Y, roi.Min.Y
	count := 0
	for y := roi.Min.Y; y < roi.Max.Y; y++ {
		if y%64 == 0 && ctx.Err() != nil {
			return nil
		}
		for x := roi.Min.X; x < roi.Max.X; x++ {
			masked := false
			for _, r := range mask[y] {
				if x >= r.Min.X && x < r.Max.X {
					masked = true
					break
				}
			}
			if masked {
				continue
			}
			r, g, bl, a := im.At(x, y).RGBA()
			if a < 0x8000 || r > 0xeeee && g > 0xeeee && bl > 0xeeee {
				continue
			}
			count++
			left = min(left, x)
			right = max(right, x)
			upper = min(upper, y)
			lower = max(lower, y)
		}
	}
	if count < 100 || float64(right-left)/w < .05 || float64(lower-upper)/h < .03 {
		return nil
	}
	return boxValue([]float64{float64(left)/w - .01, float64(upper)/h - .01, float64(right+1)/w + .01, float64(lower+1)/h + .01})
}

// TextOnlyPages is intentionally stricter than its historical name: only fully
// blank rendered pages are proven safe to omit. Poppler's XML image list cannot
// rule out vector graphics, so visible text/graphics always keep the page.
func TextOnlyPages(ctx context.Context, ws *store.Workspace, pages []int) (bool, error) {
	if len(pages) == 0 {
		return false, nil
	}
	if e := acquireImage(ctx); e != nil {
		return false, e
	}
	defer releaseImage()
	for _, page := range pages {
		if page < 1 {
			return false, fmt.Errorf("invalid PDF page: %d", page)
		}
		im, e := decodeImage(filepath.Join(ws.Root, "pages", fmt.Sprintf("page-%03d.png", page)))
		if e != nil {
			return false, e
		}
		b := im.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			if y%64 == 0 {
				if e = ctx.Err(); e != nil {
					return false, e
				}
			}
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, a := im.At(x, y).RGBA()
				if a > 0 && (r < 0xffff || g < 0xffff || bl < 0xffff) {
					return false, nil
				}
			}
		}
	}
	return true, nil
}
