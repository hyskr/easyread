package pdf

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hyskr/easyread/backend/store"
)

func fixture(t *testing.T, rotation int, crop string) *store.Workspace {
	t.Helper()
	root := t.TempDir()
	content := "BT /F1 18 Tf 40 100 Td (Hello PDF world) Tj ET\n0.1 0.3 0.8 rg 40 30 90 40 re f\n"
	objs := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /CropBox [%s] /Rotate %d /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R /Annots [6 0 R] >>", crop, rotation), "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content), "<< /Type /Annot /Subtype /Link /Rect [40 95 160 120] /A << /S /URI /URI (https://example.com/paper) >> >>"}
	var out strings.Builder
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, o := range objs {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, off := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	if e := os.WriteFile(filepath.Join(root, "source.pdf"), []byte(out.String()), 0600); e != nil {
		t.Fatal(e)
	}
	return &store.Workspace{Root: root}
}
func requirePoppler(t *testing.T) {
	t.Helper()
	if Dependencies()["available"] != true {
		t.Skip("Poppler unavailable")
	}
}
func TestPrepareCropAndSelection(t *testing.T) {
	requirePoppler(t)
	ws := fixture(t, 0, "0 0 300 200")
	ctx := context.Background()
	pages, e := Prepare(ctx, ws)
	if e != nil {
		t.Fatal(e)
	}
	if len(pages) != 1 || pages[0]["w"] != float64(300) {
		t.Fatalf("pages: %#v", pages)
	}
	text, e := os.ReadFile(filepath.Join(ws.Root, "extract/page-001.txt"))
	if e != nil || !strings.Contains(string(text), "Hello PDF world") {
		t.Fatalf("text %s: %v", text, e)
	}
	p, e := SelectableText(ctx, ws, 1)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	var rows [][]any
	if e = json.Unmarshal(raw, &rows); e != nil || len(rows) < 10 {
		t.Fatalf("characters: %s %v", raw, e)
	}
	for _, r := range rows {
		for _, v := range r[1:] {
			if number(v) < 0 || number(v) > 1 {
				t.Fatalf("out of bounds: %v", r)
			}
		}
	}
	rel, e := Crop(ctx, ws, 1, []float64{.1, .1, .5, .5}, "")
	if e != nil {
		t.Fatal(e)
	}
	im, e := decodeImage(filepath.Join(ws.Root, rel))
	if e != nil || im.Bounds().Dx() < 100 {
		t.Fatalf("crop: %v", e)
	}
	if _, e = Crop(ctx, ws, 1, []float64{.1, .1, .5, .5}, "../bad"); e == nil {
		t.Fatal("accepted traversal")
	}
	if _, e = PageVariant(ctx, ws, "../source.pdf", 400); e == nil {
		t.Fatal("accepted traversal")
	}
	if _, e = PageVariant(ctx, ws, pages[0]["img"].(string), 400); e != nil {
		t.Fatal(e)
	}
	if _, e = SelectableText(ctx, ws, 2); e == nil {
		t.Fatal("accepted nonexistent page")
	}
}
func TestRotatedCropBox(t *testing.T) {
	requirePoppler(t)
	for _, rot := range []int{0, 90, 180, 270} {
		t.Run(fmt.Sprint(rot), func(t *testing.T) {
			ws := fixture(t, rot, "20 20 280 180")
			pages, e := Prepare(context.Background(), ws)
			if e != nil {
				t.Fatal(e)
			}
			w, h := 260., 160.
			if rot%180 != 0 {
				w, h = h, w
			}
			if pages[0]["w"] != w || pages[0]["h"] != h {
				t.Fatalf("wrong size: %#v", pages)
			}
			im, e := decodeImage(filepath.Join(ws.Root, pages[0]["img"].(string)))
			if e != nil {
				t.Fatal(e)
			}
			if math.Abs(float64(im.Bounds().Dx())/float64(im.Bounds().Dy())-w/h) > .02 {
				t.Fatal("wrong rotation")
			}
		})
	}
}
func TestUnicodeRowsAndNormalization(t *testing.T) {
	p, e := parseWords([]byte(`<doc><page width="100" height="100"><word xMin="10" yMin="20" xMax="40" yMax="30">A中😀</word></page></doc>`))
	if e != nil {
		t.Fatal(e)
	}
	rows := wordRows(p)
	if len(rows) != 3 || rows[2][0] != "😀" || rows[1][1] != .2 {
		t.Fatalf("unicode rows %#v", rows)
	}
	if string(normalize("ﬁ ＡＢＣ 中文 😀")) != "fiabc中文" {
		t.Fatal("normalization")
	}
}
func TestRenderLimits(t *testing.T) {
	for _, wh := range [][2]float64{{20000, 10000}, {100000, 1}, {1, 100000}, {5000, 5000}} {
		s := renderScale(wh[0], wh[1], 3)
		w, h := math.Ceil(wh[0]*s), math.Ceil(wh[1]*s)
		if w*h > MaxPixels || math.Max(w, h) > MaxSide {
			t.Fatalf("unbounded %v: %v %v", wh, w, h)
		}
	}
}
func TestManualCropResetAndLayout(t *testing.T) {
	requirePoppler(t)
	ws := fixture(t, 0, "0 0 300 200")
	ctx := context.Background()
	pages, e := Prepare(ctx, ws)
	if e != nil {
		t.Fatal(e)
	}
	paper := map[string]any{"meta": map[string]any{"pages": pages}, "blocks": []any{map[string]any{"id": "p1", "type": "para", "page": 1, "en": "Hello PDF world"}, map[string]any{"id": "f1", "type": "figure", "page": 1, "box": []float64{.1, .6, .5, .9}, "src": "old.png"}}}
	if e = ws.Write("paper", paper); e != nil {
		t.Fatal(e)
	}
	if e = RefreshLayout(ctx, ws); e != nil {
		t.Fatal(e)
	}
	layout, e := ws.Load("layout")
	if e != nil || layout["p1"] == nil {
		t.Fatalf("layout %#v %v", layout, e)
	}
	body := map[string]any{"id": "f1", "page": 1, "box": []float64{.1, .1, .5, .5}}
	result, e := ManualCrop(ctx, ws, body)
	if e != nil {
		t.Fatal(e)
	}
	b := result["block"].(map[string]any)
	if b["manual_crop"] == nil {
		t.Fatal("missing saved crop")
	}
	result, e = ManualCrop(ctx, ws, map[string]any{"id": "f1", "reset": true})
	if e != nil {
		t.Fatal(e)
	}
	if result["block"].(map[string]any)["src"] != "old.png" {
		t.Fatal("reset lost original")
	}
}
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ws := fixture(t, 0, "0 0 300 200")
	if _, e := Prepare(ctx, ws); e == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestAnnotationLinks(t *testing.T) {
	requirePoppler(t)
	if _, e := executable("pdftohtml"); e != nil {
		t.Skip("pdftohtml missing")
	}
	ws := fixture(t, 0, "0 0 300 200")
	links, e := EnsureLinks(context.Background(), ws)
	if e != nil {
		t.Fatal(e)
	}
	if len(links["1"]) != 1 || links["1"][0].URL != "https://example.com/paper" {
		t.Fatalf("links %#v", links)
	}
	got := linkText("See Hello PDF world", links["1"])
	if !strings.Contains(got, "https://example.com/paper") {
		t.Fatalf("link backfill %q", got)
	}
	if safeURL("javascript:alert(1)") != "" || safeURL("https://example.com/ bad") != "" {
		t.Fatal("unsafe URL accepted")
	}
	if s := linkText("`Hello PDF world`", links["1"]); strings.Contains(s, "](https") {
		t.Fatal("modified code span")
	}
}
func TestRotationTextCoordinates(t *testing.T) {
	requirePoppler(t)
	for _, rot := range []int{0, 90, 180, 270} {
		ws := fixture(t, rot, "20 20 280 180")
		source, _ := ws.SourcePDF()
		b, e := run(context.Background(), "pdftotext", "-cropbox", "-bbox", source, "-")
		if e != nil {
			t.Fatal(e)
		}
		p, e := parseWords(b)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("rotation=%d page=%vx%v word=%#v", rot, p.W, p.H, p.Words[0])
	}
}

func TestCrossPageLocations(t *testing.T) {
	root := t.TempDir()
	for n, text := range []string{"A paragraph starts here and", "continues on another page."} {
		p := bboxPage{W: 100, H: 100, Words: []word{{X0: 10, Y0: 20, X1: 90, Y1: 30, Text: text}}}
		if e := writeJSON(filepath.Join(root, "extract", fmt.Sprintf("page-%03d.chars.json", n+1)), wordRows(p)); e != nil {
			t.Fatal(e)
		}
	}
	a, _ := loadStream(root, 1)
	b, _ := loadStream(root, 2)
	loc := locateBlock(map[string]any{"type": "para", "page": 1, "en": "A paragraph starts here and continues on another page."}, a, b, 0)
	if len(loc["regions"].([]map[string]any)) != 2 {
		t.Fatalf("lost cross-page regions %#v", loc)
	}
}
func TestColumnRegionsKeepGap(t *testing.T) {
	s := stream{}
	for i, b := range [][]float64{{.1, .1, .2, .12}, {.21, .1, .4, .12}, {.6, .1, .7, .12}, {.71, .1, .9, .12}} {
		s.Chars = append(s.Chars, glyph{"x", b})
		s.Index = append(s.Index, i)
	}
	boxes := regionBoxes(s, 0, 4)
	if len(boxes) != 2 {
		t.Fatalf("lost column gap %#v", boxes)
	}
}
func TestSymlinkVariantEscape(t *testing.T) {
	ws := fixture(t, 0, "0 0 300 200")
	if e := os.Mkdir(filepath.Join(ws.Root, "pages"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(ws.Root, "source.pdf"), filepath.Join(ws.Root, "pages", "escape.png")); e != nil {
		t.Skip(e)
	}
	if _, e := PageVariant(context.Background(), ws, "pages/escape.png", 400); e == nil {
		t.Fatal("followed outside symlink")
	}
}
func TestRotatedGlyphNormalization(t *testing.T) {
	requirePoppler(t)
	for _, rot := range []int{0, 90, 180, 270} {
		ws := fixture(t, rot, "20 20 280 180")
		source, _ := ws.SourcePDF()
		if e := extract(context.Background(), source, ws.Root, 1); e != nil {
			t.Fatal(e)
		}
		s, e := loadStream(ws.Root, 1)
		if e != nil || len(s.Chars) == 0 {
			t.Fatalf("rot %d: empty text %v", rot, e)
		}
		g := s.Chars[0]
		if g.Text != "H" {
			t.Fatalf("rot %d first character %q", rot, g.Text)
		}
		for _, v := range g.Box {
			if v < 0 || v > 1 {
				t.Fatalf("rot %d out of range %v", rot, g.Box)
			}
		}
		if rot == 90 && g.Box[0] < .45 {
			t.Fatalf("rotated width not corrected: %v", g.Box)
		}
		if rot == 270 && g.Box[1] < .88 {
			t.Fatalf("rotated direction not corrected: %v", g.Box)
		}
	}
}

func TestAutomaticFigureAndConservativeBlankPages(t *testing.T) {
	ws := &store.Workspace{Root: t.TempDir()}
	im := image.NewRGBA(image.Rect(0, 0, 300, 200))
	draw.Draw(im, im.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	path := filepath.Join(ws.Root, "pages/page-001.png")
	if e := savePNG(path, im); e != nil {
		t.Fatal(e)
	}
	blank, e := TextOnlyPages(context.Background(), ws, []int{1})
	if e != nil || !blank {
		t.Fatalf("blank page %v %v", blank, e)
	}
	draw.Draw(im, image.Rect(30, 30, 130, 90), &image.Uniform{color.RGBA{20, 50, 200, 255}}, image.Point{}, draw.Src)
	if e = savePNG(path, im); e != nil {
		t.Fatal(e)
	}
	blank, e = TextOnlyPages(context.Background(), ws, []int{1})
	if e != nil || blank {
		t.Fatalf("graphic page omitted %v %v", blank, e)
	}
	block := map[string]any{"id": "f", "type": "figure", "page": 1}
	loc := map[string]any{"page": 1, "src": "text", "box": []float64{.1, .6, .45, .7}}
	box := automaticFigureBox(context.Background(), ws, block, loc, map[string]any{"f": loc}, []map[string]any{block})
	if len(box) != 4 || box[1] > .16 || box[3] > .5 {
		t.Fatalf("auto figure crop %#v", box)
	}
}
func TestDerivedDirectorySymlinkIsRejected(t *testing.T) {
	ws := fixture(t, 0, "0 0 300 200")
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(ws.Root, "figures")); e != nil {
		t.Skip(e)
	}
	if _, e := Crop(context.Background(), ws, 1, []float64{.1, .1, .4, .4}, ""); e == nil {
		t.Fatal("wrote through external directory symlink")
	}
}

func TestMetadataTitleDoesNotOverrideUserTitle(t *testing.T) {
	for _, tc := range []struct{ title, want string }{{"", "PDF title"}, {"original", "PDF title"}, {"User title", "User title"}, {"source", "source"}} {
		p := map[string]any{"meta": map[string]any{"title_en": tc.title, "source": "original.pdf"}}
		applyTitle(p, "PDF title", "/tmp/source.pdf")
		if got := p["meta"].(map[string]any)["title_en"]; got != tc.want {
			t.Fatalf("title %q: got %v want %s", tc.title, got, tc.want)
		}
	}
}

func TestSelectableCacheReuseAndInvalidation(t *testing.T) {
	requirePoppler(t)
	ws := fixture(t, 0, "0 0 300 200")
	ctx := context.Background()
	path, e := SelectableText(ctx, ws, 1)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("[]"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = SelectableText(ctx, ws, 1); e != nil {
		t.Fatal(e)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "[]" {
		t.Fatal("cache not reused")
	}
	source, _ := ws.SourcePDF()
	f, e := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.WriteString("\n% source changed\n")
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = SelectableText(ctx, ws, 1); e != nil {
		t.Fatal(e)
	}
	data, _ = os.ReadFile(path)
	if string(data) == "[]" {
		t.Fatal("source identity change did not invalidate cache")
	}
}
