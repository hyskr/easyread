// Package pdf implements PDF preparation using the Poppler native utilities.
// No Python interpreter or Python business logic is required. Each native process
// handles one page, bounding accumulated native caches and enabling cancellation.
package pdf

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hyskr/easyread/backend/store"
)

const MaxPixels = 8000000
const MaxSide = 4096

var nativeGate = make(chan struct{}, 1)
var cacheOnce sync.Once
var nativeCache string
var cacheError error

func executable(name string) (string, error) {
	if d := os.Getenv("EASYREAD_PDF_BIN"); d != "" {
		p := filepath.Join(d, name)
		if _, e := os.Stat(p); e == nil {
			return p, nil
		}
		if _, e := os.Stat(p + ".exe"); e == nil {
			return p + ".exe", nil
		}
	}
	if self, e := os.Executable(); e == nil {
		for _, dir := range []string{filepath.Dir(self), filepath.Join(filepath.Dir(self), "bin")} {
			for _, suffix := range []string{"", ".exe"} {
				p := filepath.Join(dir, name+suffix)
				if s, e := os.Stat(p); e == nil && !s.IsDir() {
					return p, nil
				}
			}
		}
	}
	return exec.LookPath(name)
}
func Dependencies() map[string]any {
	result := map[string]any{"engine": "poppler", "python_required": false, "character_boxes": "interpolated from native word bounds"}
	ready := true
	for _, name := range []string{"pdfinfo", "pdftoppm", "pdftotext"} {
		p, e := executable(name)
		if e != nil {
			ready = false
			result[name] = nil
		} else {
			result[name] = p
		}
	}
	if p, e := executable("pdftohtml"); e == nil {
		result["pdftohtml"] = p
		result["inline_links"] = true
	} else {
		result["inline_links"] = false
	}
	result["available"] = ready
	return result
}

type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errors.New("PDF utility output exceeds safety limit")
	}
	return b.Buffer.Write(p)
}
func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	path, e := executable(name)
	if e != nil {
		return nil, fmt.Errorf("PDF dependency %s is missing: install Poppler or set EASYREAD_PDF_BIN: %w", name, e)
	}
	select {
	case nativeGate <- struct{}{}:
		defer func() { <-nativeGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cacheOnce.Do(func() { nativeCache, cacheError = os.MkdirTemp("", "easyread-native-cache-") })
	if cacheError != nil {
		return nil, cacheError
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = nativeCache
	cmd.Env = append(os.Environ(), "LC_ALL=C", "XDG_CACHE_HOME="+nativeCache, "PWD="+nativeCache)
	// Packaged Linux distributions may carry shared objects beside bin/.
	bundle := filepath.Dir(filepath.Dir(path))
	if fileExists(filepath.Join(bundle, "PDF-THIRD-PARTY-NOTICES.txt")) {
		if st, err := os.Stat(filepath.Join(bundle, "share", "poppler")); err == nil && st.IsDir() {
			cmd.Dir = bundle
			cmd.Env = append(cmd.Env, "PWD="+bundle)
		}
	}
	if st, err := os.Stat(filepath.Join(bundle, "lib")); err == nil && st.IsDir() {
		cmd.Env = append(cmd.Env, "LD_LIBRARY_PATH="+filepath.Join(bundle, "lib")+string(os.PathListSeparator)+os.Getenv("LD_LIBRARY_PATH"))
	}
	if config := filepath.Join(bundle, "fonts", "fonts.conf"); fileExists(config) {
		cmd.Env = append(cmd.Env, "FONTCONFIG_FILE="+config)
	}

	out := &cappedBuffer{max: 32 << 20}
	errout := &cappedBuffer{max: 64 << 10}
	cmd.Stdout = out
	cmd.Stderr = errout
	if e = cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &nativeError{tool: name, cause: e, stderr: strings.TrimSpace(errout.String())}
	}
	return out.Bytes(), nil
}
func atomicFile(path string, data []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".pdf-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func writeJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return atomicFile(path, b)
}

var countRE = regexp.MustCompile(`(?m)^Pages:\s+(\d+)`)
var sizeRE = regexp.MustCompile(`(?m)^(?:Page\s+\d+\s+size|Page size):\s+([\d.]+) x ([\d.]+)`)
var cropRE = regexp.MustCompile(`(?m)^(?:Page\s+\d+\s+)?CropBox:\s+([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)`)
var rotRE = regexp.MustCompile(`(?m)^(?:Page\s+\d+\s+rot|Page rot):\s+(-?\d+)`)

func geometry(ctx context.Context, source string, page int) (float64, float64, int, error) {
	b, e := run(ctx, "pdfinfo", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-box", source)
	if e != nil {
		return 0, 0, 0, e
	}
	m := sizeRE.FindStringSubmatch(string(b))
	if len(m) < 3 {
		return 0, 0, 0, errors.New("cannot read PDF page dimensions")
	}
	w, _ := strconv.ParseFloat(m[1], 64)
	h, _ := strconv.ParseFloat(m[2], 64)
	if c := cropRE.FindStringSubmatch(string(b)); len(c) == 5 {
		x0, _ := strconv.ParseFloat(c[1], 64)
		y0, _ := strconv.ParseFloat(c[2], 64)
		x1, _ := strconv.ParseFloat(c[3], 64)
		y1, _ := strconv.ParseFloat(c[4], 64)
		w = x1 - x0
		h = y1 - y0
	}
	rotation := 0
	if r := rotRE.FindStringSubmatch(string(b)); len(r) == 2 {
		v, _ := strconv.Atoi(r[1])
		rotation = ((v % 360) + 360) % 360
		if v%180 != 0 {
			w, h = h, w
		}
	}
	if w <= 0 || h <= 0 || math.IsNaN(w) || math.IsNaN(h) || math.IsInf(w, 0) || math.IsInf(h, 0) {
		return 0, 0, 0, errors.New("invalid PDF page dimensions")
	}
	return w, h, rotation, nil
}
func dimensions(ctx context.Context, source string, page int) (float64, float64, error) {
	w, h, _, e := geometry(ctx, source, page)
	return w, h, e
}
func renderScale(w, h, scale float64) float64 {
	return math.Min(scale, math.Min(float64(MaxSide-1)/math.Max(w, h), math.Sqrt(float64(MaxPixels-2*MaxSide))/math.Sqrt(w)/math.Sqrt(h)))
}
func render(ctx context.Context, source string, page int, path string, scale float64) error {
	if page < 1 || scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return errors.New("invalid PDF page or render scale")
	}
	w, h, e := dimensions(ctx, source, page)
	if e != nil {
		return e
	}
	scale = renderScale(w, h, scale)
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return errors.New("PDF dimensions exceed render limits")
	}
	if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	tmp, e := os.MkdirTemp(filepath.Dir(path), ".render-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	prefix := filepath.Join(tmp, "page")
	for attempt := 0; attempt < 2; attempt++ {
		_, e = run(ctx, "pdftoppm", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-singlefile", "-cropbox", "-r", strconv.FormatFloat(scale*72, 'g', 12, 64), "-png", source, prefix)
		if e == nil {
			break
		}
		if attempt != 0 || !nativeMemoryError(e) || ctx.Err() != nil {
			return e
		}
		// The failed process has been reaped by run. Drop any partial bitmap
		// before retrying with a fresh process and half the linear resolution.
		_ = os.Remove(prefix + ".png")
		scale /= 2
	}

	return os.Rename(prefix+".png", path)
}

type word struct {
	X0   float64 `xml:"xMin,attr"`
	Y0   float64 `xml:"yMin,attr"`
	X1   float64 `xml:"xMax,attr"`
	Y1   float64 `xml:"yMax,attr"`
	Text string  `xml:",chardata"`
}
type bboxPage struct {
	Rotation int     `xml:"-"`
	W        float64 `xml:"width,attr"`
	H        float64 `xml:"height,attr"`
	Words    []word  `xml:"word"`
}

func parseWords(data []byte) (bboxPage, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	for {
		tok, e := d.Token()
		if e != nil {
			return bboxPage{}, e
		}
		if s, ok := tok.(xml.StartElement); ok && s.Name.Local == "page" {
			var p bboxPage
			e = d.DecodeElement(&p, &s)
			return p, e
		}
	}
}
func round(v float64) float64 { return math.Round(v*10000) / 10000 }
func wordRows(p bboxPage) [][]any {
	rows := make([][]any, 0)
	if p.W <= 0 || p.H <= 0 {
		return rows
	}
	for i, w := range p.Words {
		runes := []rune(w.Text)
		if len(runes) == 0 || w.X1 <= 0 || w.Y1 <= 0 || w.X0 >= p.W || w.Y0 >= p.H {
			continue
		}
		if i > 0 {
			sep := " "
			prev := p.Words[i-1]
			if math.Abs(prev.Y0-w.Y0) > math.Max(1, (w.Y1-w.Y0)*.5) {
				sep = "\n"
			}
			rows = append(rows, []any{sep, 0., 0., 0., 0.})
		}
		for j, c := range runes {
			if c == utf8.RuneError {
				continue
			}
			lo, hi := float64(j)/float64(len(runes)), float64(j+1)/float64(len(runes))
			if p.Rotation == 180 || p.Rotation == 270 {
				lo, hi = 1-hi, 1-lo
			}
			x0, y0, x1, y1 := w.X0, w.Y0, w.X1, w.Y1
			if p.Rotation == 90 || p.Rotation == 270 {
				y0 = w.Y0 + (w.Y1-w.Y0)*lo
				y1 = w.Y0 + (w.Y1-w.Y0)*hi
			} else {
				x0 = w.X0 + (w.X1-w.X0)*lo
				x1 = w.X0 + (w.X1-w.X0)*hi
			}
			if x1 <= 0 || y1 <= 0 || x0 >= p.W || y0 >= p.H {
				continue
			}
			rows = append(rows, []any{string(c), round(math.Max(0, x0/p.W)), round(math.Max(0, y0/p.H)), round(math.Min(1, x1/p.W)), round(math.Min(1, y1/p.H))})
		}
	}
	return rows
}
func extract(ctx context.Context, source, root string, page int) error {
	args := []string{"-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-cropbox", "-enc", "UTF-8"}
	b, e := run(ctx, "pdftotext", append(args, "-bbox", source, "-")...)
	if e != nil {
		return e
	}
	p, e := parseWords(b)
	if e != nil && e != io.EOF {
		return e
	}
	p.W, p.H, p.Rotation, e = geometry(ctx, source, page)
	if e != nil {
		return e
	}
	rows := wordRows(p)
	text, e := run(ctx, "pdftotext", append(args, "-layout", "-nopgbrk", source, "-")...)
	if e != nil {
		return e
	}
	prefix := filepath.Join(root, "extract", fmt.Sprintf("page-%03d", page))
	if e = atomicFile(prefix+".txt", text); e != nil {
		return e
	}
	return writeJSON(prefix+".chars.json", rows)
}
func Prepare(ctx context.Context, ws *store.Workspace) ([]map[string]any, error) {
	if e := secureDirs(ws.Root, "pages", "extract"); e != nil {
		return nil, e
	}
	source, e := ws.SourcePDF()
	if e != nil {
		return nil, e
	}
	b, e := run(ctx, "pdfinfo", source)
	if e != nil {
		return nil, e
	}
	m := countRE.FindStringSubmatch(string(b))
	if len(m) != 2 {
		return nil, errors.New("PDF page count unavailable")
	}
	if titleMatch := regexp.MustCompile(`(?m)^Title:[ \t]*([^\r\n]*)`).FindStringSubmatch(string(b)); len(titleMatch) == 2 {
		title := strings.TrimSpace(titleMatch[1])
		if title != "" {
			if e = ws.Update("paper", func(p map[string]any) error {
				applyTitle(p, title, source)
				return nil
			}); e != nil {
				return nil, e
			}
		}
	}
	count, _ := strconv.Atoi(m[1])
	if count < 1 || count > 100000 {
		return nil, errors.New("invalid PDF page count")
	}
	pages := make([]map[string]any, 0, count)
	for n := 1; n <= count; n++ {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		w, h, e := dimensions(ctx, source, n)
		if e != nil {
			return nil, e
		}
		rel := fmt.Sprintf("pages/page-%03d.png", n)
		if e = render(ctx, source, n, filepath.Join(ws.Root, rel), 2.4); e != nil {
			return nil, e
		}
		if e = extract(ctx, source, ws.Root, n); e != nil {
			return nil, e
		}
		pages = append(pages, map[string]any{"n": n, "w": w, "h": h, "img": rel})
	}
	return pages, nil
}
func SelectableText(ctx context.Context, ws *store.Workspace, page int) (string, error) {
	if e := secureDirs(ws.Root, "extract"); e != nil {
		return "", e
	}
	if page < 1 {
		return "", errors.New("invalid PDF page")
	}
	source, e := ws.SourcePDF()
	if e != nil {
		return "", e
	}
	path := filepath.Join(ws.Root, "extract", fmt.Sprintf("select-v2-%03d.json", page))
	stat, e := os.Stat(source)
	if e != nil {
		return "", e
	}
	stamp := fmt.Sprintf("go-poppler-v1:%s:%d:%d", source, stat.Size(), stat.ModTime().UnixNano())
	old, _ := os.ReadFile(path + ".stamp")
	if string(old) == stamp {
		if _, e = os.Stat(path); e == nil {
			return path, nil
		}
	}
	if e = extract(ctx, source, ws.Root, page); e != nil {
		return "", e
	}
	b, e := os.ReadFile(filepath.Join(ws.Root, "extract", fmt.Sprintf("page-%03d.chars.json", page)))
	if e != nil {
		return "", e
	}
	if e = atomicFile(path, b); e != nil {
		return "", e
	}
	if e = atomicFile(path+".stamp", []byte(stamp)); e != nil {
		return "", e
	}
	return path, nil
}

func fileExists(path string) bool { s, e := os.Stat(path); return e == nil && !s.IsDir() }

// Reject derived-directory symlinks that escape the selected workspace.
func secureDirs(root string, names ...string) error {
	base, e := filepath.EvalSymlinks(root)
	if e != nil {
		return e
	}
	base, e = filepath.Abs(base)
	if e != nil {
		return e
	}
	for _, name := range names {
		path := filepath.Join(root, name)
		if e = os.MkdirAll(path, 0755); e != nil {
			return e
		}
		resolved, e := filepath.EvalSymlinks(path)
		if e != nil {
			return e
		}
		resolved, e = filepath.Abs(resolved)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(base, resolved)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("derived PDF directory escapes workspace")
		}
	}
	return nil
}

func applyTitle(p map[string]any, title, source string) {
	meta, ok := p["meta"].(map[string]any)
	if !ok {
		meta = map[string]any{}
		p["meta"] = meta
	}
	current, _ := meta["title_en"].(string)
	original, _ := meta["source"].(string)
	if original == "" {
		original = source
	}
	fallback := strings.TrimSuffix(filepath.Base(original), filepath.Ext(original))
	if strings.TrimSpace(current) == "" || current == fallback {
		meta["title_en"] = title
	}
}

type nativeError struct {
	tool   string
	cause  error
	stderr string
}

func (e *nativeError) Error() string { return fmt.Sprintf("%s: %v: %s", e.tool, e.cause, e.stderr) }
func (e *nativeError) Unwrap() error { return e.cause }
func nativeMemoryError(err error) bool {
	var e *nativeError
	if !errors.As(err, &e) {
		return false
	}
	text := strings.ToLower(e.stderr)
	for _, message := range []string{"out of memory", "cannot allocate memory", "std::bad_alloc", "failed to allocate memory"} {
		if strings.Contains(text, message) {
			return true
		}
	}
	var exit *exec.ExitError
	return errors.As(e.cause, &exit) && uint32(exit.ExitCode()) == 0xe0000008
}
