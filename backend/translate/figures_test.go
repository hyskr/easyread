package translate

import (
	"context"
	"errors"
	"github.com/hyskr/easyread/backend/store"
	"os"
	"path/filepath"
	"testing"
)

func figureWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w := workspace(t, Object{"meta": Object{"page_count": 1, "target": "ja"}, "blocks": []any{Object{"id": "fig1", "type": "figure", "page": 1, "src": "figures/source.png", "image_en": "X axis", "image_zh": "日本語", "caption_en": "Figure one"}}})
	_ = os.MkdirAll(filepath.Join(w.Root, "figures"), 0700)
	_ = os.WriteFile(filepath.Join(w.Root, "figures", "source.png"), []byte("source fixture"), 0600)
	return w
}
func TestImageGenerationReuseAndInputIdentity(t *testing.T) {
	w := figureWorkspace(t)
	old := EditImage
	defer func() { EditImage = old }()
	calls := 0
	EditImage = func(ctx context.Context, cfg Object, path, prompt, cwd string) ([]byte, error) {
		calls++
		if cfg["engine"] != "openai" || Obj(cfg["openai"])["model"] != "image-model" {
			t.Fatal(cfg)
		}
		return []byte("fake PNG image"), nil
	}
	cfg := Object{"figure_images": Object{"enabled": true, "provider": "api", "base_url": "https://unused.invalid/v1", "model": "image-model"}}
	for i := 0; i < 2; i++ {
		r, e := GenerateFigures(context.Background(), w, cfg, []string{"fig1"}, false, func(int, int, string) {})
		if e != nil {
			t.Fatal(e)
		}
		if i == 0 && Num(r["generated"]) != 1 {
			t.Fatal(r)
		}
		if i == 1 && Num(r["skipped"]) != 1 {
			t.Fatal(r)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	p, _ := w.Load("paper")
	b := Obj(List(p["blocks"])[0])
	if b["src"] != "figures/source.png" || Obj(b["translated_image"])["target"] != "ja" || b["image_zh"] != "日本語" {
		t.Fatal(b)
	}
	_ = os.Remove(filepath.Join(w.Root, Str(Obj(b["translated_image"])["src"])))
	_, e := GenerateFigures(context.Background(), w, cfg, []string{"fig1"}, false, func(int, int, string) {})
	if e != nil || calls != 2 {
		t.Fatal(calls, e)
	}
}
func TestImageLateResultCannotOverwriteEdits(t *testing.T) {
	w := figureWorkspace(t)
	old := EditImage
	defer func() { EditImage = old }()
	EditImage = func(context.Context, Object, string, string, string) ([]byte, error) {
		_ = w.Update("paper", func(p Object) error { Obj(List(p["blocks"])[0])["image_zh"] = "changed"; return nil })
		return []byte("image"), nil
	}
	r, e := GenerateFigures(context.Background(), w, Object{"figure_images": Object{"enabled": true}}, []string{"fig1"}, false, func(int, int, string) {})
	if e != nil || len(Obj(r["failed"])) != 1 {
		t.Fatal(r, e)
	}
	p, _ := w.Load("paper")
	if Obj(List(p["blocks"])[0])["translated_image"] != nil {
		t.Fatal(p)
	}
	files, _ := filepath.Glob(filepath.Join(w.Root, "figures", "translated-*"))
	if len(files) > 0 {
		t.Fatal(files)
	}
}
func TestImageCancelAfterResponseDoesNotPublish(t *testing.T) {
	w := figureWorkspace(t)
	old := EditImage
	defer func() { EditImage = old }()
	ctx, cancel := context.WithCancel(context.Background())
	EditImage = func(context.Context, Object, string, string, string) ([]byte, error) {
		cancel()
		return []byte("image"), nil
	}
	_, e := GenerateFigures(ctx, w, Object{"figure_images": Object{"enabled": true}}, []string{"fig1"}, false, func(int, int, string) {})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	p, _ := w.Load("paper")
	if Obj(List(p["blocks"])[0])["translated_image"] != nil {
		t.Fatal(p)
	}
}
func TestImageFailureDoesNotFallbackToPaidAPI(t *testing.T) {
	w := figureWorkspace(t)
	old := EditImage
	defer func() { EditImage = old }()
	calls := 0
	EditImage = func(ctx context.Context, cfg Object, path, prompt, cwd string) ([]byte, error) {
		calls++
		if cfg["engine"] != "codex" {
			t.Fatal(cfg)
		}
		return nil, errors.New("subscription unavailable")
	}
	r, e := GenerateFigures(context.Background(), w, Object{"figure_images": Object{"enabled": true, "provider": "codex", "base_url": "https://unused.invalid/v1", "model": "paid"}}, []string{"fig1"}, false, func(int, int, string) {})
	if e != nil || calls != 1 || len(Obj(r["failed"])) != 1 {
		t.Fatal(r, e, calls)
	}
}
