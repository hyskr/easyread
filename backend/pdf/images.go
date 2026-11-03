package pdf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hyskr/easyread/backend/store"
	_ "golang.org/x/image/webp"
)

func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}
func boxValue(v any) []float64 {
	var a []float64
	switch b := v.(type) {
	case []float64:
		a = append(a, b...)
	case []any:
		for _, x := range b {
			switch x.(type) {
			case float64, int, int64, json.Number:
			default:
				return nil
			}
			a = append(a, number(x))
		}
	}
	if len(a) != 4 {
		return nil
	}
	for i, v := range a {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		a[i] = round(math.Max(0, math.Min(1, v)))
	}
	if a[2]-a[0] < .02 || a[3]-a[1] < .02 {
		return nil
	}
	return a
}
func decodeImage(path string) (image.Image, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	cfg, _, e := image.DecodeConfig(f)
	if e != nil {
		return nil, e
	}
	if cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels+MaxSide*2 {
		return nil, errors.New("image exceeds PDF memory limit")
	}
	if _, e = f.Seek(0, 0); e != nil {
		return nil, e
	}
	im, _, e := image.Decode(f)
	return im, e
}
func savePNG(path string, im image.Image) error {
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".image-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = png.Encode(f, im); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}

var cropNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

func Crop(ctx context.Context, ws *store.Workspace, page int, box []float64, name string) (string, error) {
	if e := secureDirs(ws.Root, "figures"); e != nil {
		return "", e
	}
	if e := acquireImage(ctx); e != nil {
		return "", e
	}
	defer releaseImage()
	box = boxValue(box)
	if page < 1 || box == nil {
		return "", errors.New("invalid page or crop rectangle")
	}
	source, e := ws.SourcePDF()
	if e != nil {
		return "", e
	}
	if name == "" {
		s, e := os.Stat(source)
		if e != nil {
			return "", e
		}
		identity, _ := json.Marshal([]any{page, box, s.Size(), s.ModTime().UnixNano()})
		hash := sha256.Sum256(identity)
		name = fmt.Sprintf("crop-%d-%s", page, hex.EncodeToString(hash[:10]))
	}
	if !cropNameRE.MatchString(name) {
		return "", errors.New("invalid crop filename")
	}
	rel := "figures/" + name + ".png"
	path := filepath.Join(ws.Root, rel)
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("crop cache must not be a symbolic link")
	}
	if _, e = os.Stat(path); e == nil {
		return rel, nil
	}
	temp, e := os.MkdirTemp("", "easyread-crop-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(temp)
	src := filepath.Join(temp, "page.png")
	if e = render(ctx, source, page, src, 3); e != nil {
		return "", e
	}
	im, e := decodeImage(src)
	if e != nil {
		return "", e
	}
	b := im.Bounds()
	rect := image.Rect(int(box[0]*float64(b.Dx())), int(box[1]*float64(b.Dy())), int(math.Ceil(box[2]*float64(b.Dx()))), int(math.Ceil(box[3]*float64(b.Dy())))).Intersect(b)
	if rect.Empty() {
		return "", errors.New("empty crop rectangle")
	}
	out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(out, out.Bounds(), im, rect.Min, draw.Src)
	if e = savePNG(path, out); e != nil {
		return "", e
	}
	return rel, nil
}
func blocks(p map[string]any) []map[string]any {
	out := []map[string]any{}
	switch v := p["blocks"].(type) {
	case []any:
		for _, x := range v {
			if b, ok := x.(map[string]any); ok {
				out = append(out, b)
			}
		}
	case []map[string]any:
		return v
	}
	return out
}
func ManualCrop(ctx context.Context, ws *store.Workspace, body map[string]any) (map[string]any, error) {
	id, _ := body["id"].(string)
	reset, _ := body["reset"].(bool)
	page := int(number(body["page"]))
	box := boxValue(body["box"])
	if !reset && (page < 1 || number(body["page"]) != float64(page) || box == nil) {
		return nil, errors.New("invalid page or crop rectangle")
	}
	var result map[string]any
	var src string
	if !reset {
		p, e := ws.Load("paper")
		if e != nil {
			return nil, e
		}
		found := false
		for _, b := range blocks(p) {
			if b["id"] == id && b["type"] == "figure" {
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("figure no longer exists; reload the paper")
		}

		src, e = Crop(ctx, ws, page, box, "")
		if e != nil {
			return nil, e
		}
	}
	e := ws.Update("paper", func(p map[string]any) error {
		var target map[string]any
		for _, b := range blocks(p) {
			if b["id"] == id && b["type"] == "figure" {
				target = b
				break
			}
		}
		if target == nil {
			return errors.New("figure no longer exists; reload the paper")
		}
		if reset {
			if old, ok := target["manual_crop"].(map[string]any); ok {
				for _, key := range []string{"src", "abox", "box", "page"} {
					delete(target, key)
					if v, ok := old[key]; ok {
						target[key] = v
					}
				}
				delete(target, "manual_crop")
			}
		} else {
			exists := false
			if meta, ok := p["meta"].(map[string]any); ok {
				switch pages := meta["pages"].(type) {
				case []any:
					for _, x := range pages {
						if pg, ok := x.(map[string]any); ok && int(number(pg["n"])) == page {
							exists = true
						}
					}
				case []map[string]any:
					for _, pg := range pages {
						if int(number(pg["n"])) == page {
							exists = true
						}
					}
				}
			}
			if !exists {
				return errors.New("page does not exist")
			}
			if _, ok := target["manual_crop"]; !ok {
				old := map[string]any{}
				for _, key := range []string{"src", "abox", "box", "page"} {
					if v, ok := target[key]; ok {
						old[key] = v
					}
				}
				target["manual_crop"] = old
			}
			target["src"] = src
			target["abox"] = box
			target["box"] = box
			target["page"] = page
		}
		result = map[string]any{}
		for k, v := range target {
			result[k] = v
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return map[string]any{"block": result}, nil
}
func PageVariant(ctx context.Context, ws *store.Workspace, rel string, width int) (string, error) {
	if e := acquireImage(ctx); e != nil {
		return "", e
	}
	defer releaseImage()
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if filepath.IsAbs(rel) {
		return "", errors.New("invalid page image path")
	}
	pages, e := filepath.EvalSymlinks(filepath.Join(ws.Root, "pages"))
	if e != nil {
		return "", e
	}
	src, e := filepath.EvalSymlinks(filepath.Join(ws.Root, filepath.FromSlash(rel)))
	if e != nil {
		return "", e
	}
	inside, e := filepath.Rel(pages, src)
	if e != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", errors.New("page image path escapes pages directory")
	}
	width = width / 100 * 100
	if width < 400 {
		width = 400
	}
	if width > 2000 {
		width = 2000
	}
	out := filepath.Join(pages, fmt.Sprintf("w%d", width), strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+".png")
	if e = secureDirs(ws.Root, filepath.Join("pages", fmt.Sprintf("w%d", width))); e != nil {
		return "", e
	}
	if cached, err := os.Stat(out); err == nil {
		if original, err := os.Stat(src); err == nil && !cached.ModTime().Before(original.ModTime()) {
			return out, nil
		}
	}
	im, e := decodeImage(src)
	if e != nil {
		return "", e
	}
	b := im.Bounds()
	if b.Dx() <= width {
		return src, nil
	}
	height := int(math.Round(float64(b.Dy()) * float64(width) / float64(b.Dx())))
	if height < 1 {
		height = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	// Bilinear filtering is dependency-free and avoids additional full-size buffers.
	for y := 0; y < height; y++ {
		if y%64 == 0 {
			if e = ctx.Err(); e != nil {
				return "", e
			}
		}
		sy := (float64(y)+.5)*float64(b.Dy())/float64(height) - .5
		y0 := int(math.Max(0, math.Floor(sy)))
		y1 := min(y0+1, b.Dy()-1)
		fy := math.Max(0, sy-float64(y0))
		for x := 0; x < width; x++ {
			sx := (float64(x)+.5)*float64(b.Dx())/float64(width) - .5
			x0 := int(math.Max(0, math.Floor(sx)))
			x1 := min(x0+1, b.Dx()-1)
			fx := math.Max(0, sx-float64(x0))
			c00 := rgba(im, x0, y0)
			c10 := rgba(im, x1, y0)
			c01 := rgba(im, x0, y1)
			c11 := rgba(im, x1, y1)
			off := dst.PixOffset(x, y)
			for k := 0; k < 4; k++ {
				v := (c00[k]*(1-fx)+c10[k]*fx)*(1-fy) + (c01[k]*(1-fx)+c11[k]*fx)*fy
				dst.Pix[off+k] = uint8(v / 257)
			}
		}
	}
	if e = savePNG(out, dst); e != nil {
		return "", e
	}
	return out, nil
}
func rgba(im image.Image, x, y int) [4]float64 {
	r, g, b, a := im.At(x+im.Bounds().Min.X, y+im.Bounds().Min.Y).RGBA()
	return [4]float64{float64(r), float64(g), float64(b), float64(a)}
}

var imageGate = make(chan struct{}, 1)

func acquireImage(ctx context.Context) error {
	select {
	case imageGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func releaseImage() { <-imageGate }
