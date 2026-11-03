package pdf

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test executable also acts as four deterministic native utility processes.
// This exercises exec.Start/Wait and OS cleanup, not a mocked in-process runner.
func TestMain(m *testing.M) {
	if os.Getenv("EASYREAD_NATIVE_TEST_HELPER") == "1" {
		if e := nativeHelper(); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func nativeHelper() error {
	root := os.Getenv("EASYREAD_NATIVE_TEST_ROOT")
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	lock := filepath.Join(root, "active")
	f, e := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		_ = os.WriteFile(filepath.Join(root, "overlap"), []byte(name), 0600)
		return errors.New("native process overlap")
	}
	f.Close()
	events, e := os.OpenFile(filepath.Join(root, "events"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		os.Remove(lock)
		return e
	}
	fmt.Fprintf(events, "start %s %d\n", name, os.Getpid())
	defer func() { fmt.Fprintf(events, "end %s %d\n", name, os.Getpid()); events.Close(); os.Remove(lock) }()
	// A deliberate cleanup interval makes missing serialization visible even on
	// fast hosts. The exclusive marker remains until all native output is saved.
	time.Sleep(20 * time.Millisecond)
	switch name {
	case "pdfinfo":
		fmt.Print("Pages: 1\nPage 1 size: 60 x 80 pts\nPage 1 rot: 0\nPage 1 CropBox: 0 0 60 80\n")
	case "pdftotext":
		if containsArg("-bbox") {
			fmt.Print(`<doc><page width="60" height="80"><word xMin="5" yMin="10" xMax="40" yMax="20">Native test</word></page></doc>`)
		} else {
			fmt.Print("Native test\n")
		}
	case "pdftoppm":
		dpi := ""
		for i, a := range os.Args {
			if a == "-r" && i+1 < len(os.Args) {
				dpi = os.Args[i+1]
			}
		}
		log, e := os.OpenFile(filepath.Join(root, "dpi"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		fmt.Fprintln(log, dpi)
		log.Close()
		mode := os.Getenv("EASYREAD_NATIVE_TEST_FAILURE")
		first := !fileExists(filepath.Join(root, "failed-once"))
		if mode == "oom-always" || mode == "oom-once" && first || mode == "disk" {
			_ = os.WriteFile(filepath.Join(root, "failed-once"), []byte("1"), 0600)
			// Failed native render can leave a partial bitmap. Retry must not reuse it.
			_ = os.WriteFile(os.Args[len(os.Args)-1]+".png", []byte("partial"), 0600)
			if mode == "disk" {
				return errors.New("permission denied")
			}
			return errors.New("out of memory")
		}
		im := image.NewRGBA(image.Rect(0, 0, 60, 80))
		draw.Draw(im, im.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
		f, e := os.Create(os.Args[len(os.Args)-1] + ".png")
		if e != nil {
			return e
		}
		e = png.Encode(f, im)
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	default:
		return fmt.Errorf("unexpected helper %s", name)
	}
	if os.Getenv("EASYREAD_NATIVE_TEST_HOLD_CLEANUP") == "1" {
		if e := os.WriteFile(filepath.Join(root, "output-produced"), []byte(name), 0600); e != nil {
			return e
		}
		deadline := time.Now().Add(10 * time.Second)
		for !fileExists(filepath.Join(root, "release-cleanup")) {
			if time.Now().After(deadline) {
				return errors.New("cleanup barrier timed out")
			}
			time.Sleep(time.Millisecond)
		}
	}
	return nil
}
func containsArg(want string) bool {
	for _, arg := range os.Args {
		if arg == want {
			return true
		}
	}
	return false
}
func nativeTestTools(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if e := os.Mkdir(bin, 0755); e != nil {
		t.Fatal(e)
	}
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(self)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"pdfinfo", "pdftoppm", "pdftotext", "pdftohtml"} {
		suffix := ""
		if strings.HasSuffix(self, ".exe") {
			suffix = ".exe"
		}
		if e = os.WriteFile(filepath.Join(bin, name+suffix), data, 0755); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("EASYREAD_PDF_BIN", bin)
	t.Setenv("EASYREAD_NATIVE_TEST_HELPER", "1")
	t.Setenv("EASYREAD_NATIVE_TEST_ROOT", root)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	return root
}
func assertNativeClean(t *testing.T, root string) {
	t.Helper()
	if _, e := os.Stat(filepath.Join(root, "active")); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("native process left active marker: %v", e)
	}
	if fileExists(filepath.Join(root, "overlap")) {
		t.Fatal("native utility processes overlapped")
	}
	b, e := os.ReadFile(filepath.Join(root, "events"))
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.FieldsFunc(strings.TrimSpace(string(b)), func(r rune) bool { return r == '\n' })
	if len(lines) == 0 || len(lines)%2 != 0 {
		t.Fatalf("unbalanced native events: %s", b)
	}
	for i := 0; i < len(lines); i += 2 {
		start, end := strings.Fields(lines[i]), strings.Fields(lines[i+1])
		if len(start) != 3 || len(end) != 3 || start[0] != "start" || end[0] != "end" || start[1] != end[1] || start[2] != end[2] {
			t.Fatalf("native lifecycle not serialized: %s", b)
		}
	}
}

// Port of test_render_extract_crop_and_model_images_cannot_overlap.
func TestNativeRenderExtractCropAndModelImagesCannotOverlap(t *testing.T) {
	helper := nativeTestTools(t)
	ws := fixture(t, 0, "0 0 60 80")
	source, _ := ws.SourcePDF()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := make(chan struct{})
	errs := make(chan error, 4)
	var ready sync.WaitGroup
	ready.Add(4)
	tasks := []func() error{
		func() error { _, e := EngineImage(ctx, ws, 1); return e },
		func() error { return render(ctx, source, 1, filepath.Join(ws.Root, "pages/page-001.png"), 1) },
		func() error { return extract(ctx, source, ws.Root, 1) },
		func() error { _, e := Crop(ctx, ws, 1, []float64{0, 0, 1, 1}, "test"); return e },
	}
	for _, task := range tasks {
		go func(fn func() error) { ready.Done(); <-start; errs <- fn() }(task)
	}
	ready.Wait()
	close(start)
	for range tasks {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
	assertNativeClean(t, helper)
	for _, rel := range []string{"extract/page-001.jpg", "pages/page-001.png", "figures/test.png"} {
		if _, e := decodeImage(filepath.Join(ws.Root, rel)); e != nil {
			t.Fatalf("invalid output %s: %v", rel, e)
		}
	}
	if !fileExists(filepath.Join(ws.Root, "extract/page-001.chars.json")) {
		t.Fatal("missing extracted characters")
	}
}

// Port of test_pdf_handles_close_after_save_error. Child process reaping is
// the Go/Poppler equivalent of releasing the original in-process native handle.
func TestNativeProcessesCloseAfterModelImageSaveError(t *testing.T) {
	helper := nativeTestTools(t)
	ws := fixture(t, 0, "0 0 60 80")
	target := filepath.Join(ws.Root, "extract/page-001.jpg")
	if e := os.MkdirAll(target, 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := EngineImage(context.Background(), ws, 1); e == nil {
		t.Fatal("expected forced destination save error")
	}
	assertNativeClean(t, helper)
	entries, e := os.ReadDir(filepath.Dir(target))
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatalf("temporary render/image output leaked: %s", entry.Name())
		}
	}
	if fileExists(target + ".stamp") {
		t.Fatal("failed save published valid cache stamp")
	}
	if e = os.Remove(target); e != nil {
		t.Fatal(e)
	}
	out, e := EngineImage(context.Background(), ws, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = decodeImage(out); e != nil {
		t.Fatal(e)
	}
	assertNativeClean(t, helper)
}

func TestNativeRenderMemoryFailureRetriesFreshProcessAtHalfScale(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		attempts int
		success  bool
	}{{"oom-once", 2, true}, {"oom-always", 2, false}, {"disk", 1, false}} {
		t.Run(tc.mode, func(t *testing.T) {
			helper := nativeTestTools(t)
			t.Setenv("EASYREAD_NATIVE_TEST_FAILURE", tc.mode)
			ws := fixture(t, 0, "0 0 60 80")
			source, _ := ws.SourcePDF()
			out := filepath.Join(ws.Root, "pages/page.png")
			e := render(context.Background(), source, 1, out, 2.4)
			if (e == nil) != tc.success {
				t.Fatalf("success=%v error=%v", tc.success, e)
			}
			b, err := os.ReadFile(filepath.Join(helper, "dpi"))
			if err != nil {
				t.Fatal(err)
			}
			rates := strings.Fields(string(b))
			if len(rates) != tc.attempts {
				t.Fatalf("attempts %v", rates)
			}
			if len(rates) == 2 {
				a, _ := strconv.ParseFloat(rates[0], 64)
				b, _ := strconv.ParseFloat(rates[1], 64)
				if math.Abs(b-a/2) > .00001 {
					t.Fatalf("retry scale not halved: %v", rates)
				}
			}
			if tc.success {
				if _, e = decodeImage(out); e != nil {
					t.Fatal(e)
				}
			} else if fileExists(out) {
				t.Fatal("failed native render published partial output")
			}
			assertNativeClean(t, helper)
			entries, _ := os.ReadDir(filepath.Dir(out))
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".render-") {
					t.Fatal("render temp directory leaked")
				}
			}
		})
	}
}
func TestInvalidRenderScaleDoesNotStartNativeProcess(t *testing.T) {
	root := nativeTestTools(t)
	for _, scale := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if e := render(context.Background(), "unused", 1, filepath.Join(root, "out.png"), scale); e == nil {
			t.Fatal("invalid scale accepted")
		}
	}
	if fileExists(filepath.Join(root, "events")) {
		t.Fatal("invalid scale started native operation")
	}
}

func TestNativeGateIsHeldUntilChildCleanupCompletes(t *testing.T) {
	root := nativeTestTools(t)
	t.Setenv("EASYREAD_NATIVE_TEST_HOLD_CLEANUP", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, e := run(ctx, "pdfinfo", "unused.pdf"); result <- e }()
	deadline := time.Now().Add(5 * time.Second)
	for !fileExists(filepath.Join(root, "output-produced")) {
		select {
		case e := <-result:
			t.Fatalf("process exited before cleanup barrier: %v", e)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("native helper did not reach cleanup barrier")
		}
		time.Sleep(time.Millisecond)
	}
	// The subprocess has emitted its result but has not released native state.
	// Acquiring the global permit must still be impossible at this exact point.
	select {
	case nativeGate <- struct{}{}:
		<-nativeGate
		t.Fatal("native gate released before child cleanup/Wait")
	default:
	}
	select {
	case e := <-result:
		t.Fatalf("run returned before native cleanup: %v", e)
	default:
	}
	if e := os.WriteFile(filepath.Join(root, "release-cleanup"), []byte("1"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := <-result; e != nil {
		t.Fatal(e)
	}
	assertNativeClean(t, root)
	select {
	case nativeGate <- struct{}{}:
		<-nativeGate
	default:
		t.Fatal("native gate leaked after child cleanup")
	}
}

func TestExtremeFiniteRenderDimensionsStayBounded(t *testing.T) {
	for _, size := range [][2]float64{{1e200, 1e200}, {1e300, 1}, {1, 1e300}} {
		scale := renderScale(size[0], size[1], 3)
		if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
			t.Fatalf("invalid scale for %v: %v", size, scale)
		}
		w, h := math.Ceil(size[0]*scale), math.Ceil(size[1]*scale)
		if math.Max(w, h) > MaxSide || w*h > MaxPixels {
			t.Fatalf("unbounded raster for %v: %v x %v", size, w, h)
		}
	}
}
