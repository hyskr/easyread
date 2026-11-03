package exporter

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"github.com/hyskr/easyread/backend/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownBundlesImagesAndRejectsTraversal(t *testing.T) {
	w := store.NewWorkspace(t.TempDir())
	os.Mkdir(filepath.Join(w.Root, "figures"), 0700)
	os.WriteFile(filepath.Join(w.Root, "figures", "a.png"), []byte("png"), 0600)
	w.Write("paper", M{"meta": M{"title_en": "Paper"}})
	w.Write("chat", M{"threads": []any{M{"id": "t1", "messages": []any{M{"role": "user", "content": "What?"}, M{"role": "assistant", "id": "a", "content": "![x](figures/a.png) ![bad](figures/../../secret.png)"}}}}})
	out, e := Markdown(w, M{"thread": "t1", "id": "a"})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasSuffix(str(out["filename"]), ".zip") {
		t.Fatal(out)
	}
	b, _ := base64.StdEncoding.DecodeString(str(out["data"]))
	z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil || len(z.File) != 2 {
		t.Fatalf("%v %v", z, e)
	}
}
func TestHTMLInlinesAssetsAndEscapesData(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "reader.html"), []byte(`<html><head><title>x</title><link rel="stylesheet" href="/web/style.css"><script src="/web/script.js"></script></head><!--PR:DATA--></html>`), 0600)
	os.WriteFile(filepath.Join(root, "style.css"), []byte("body{color:red}"), 0600)
	os.WriteFile(filepath.Join(root, "script.js"), []byte("window.ok=true;"), 0600)
	ws := store.NewWorkspace(t.TempDir())
	ws.Write("paper", M{"meta": M{"title_en": "<script>bad</script>"}})
	b, n, e := HTML(ws, root, false)
	if e != nil {
		t.Fatal(e)
	}
	s := string(b)
	if strings.Contains(s, `src="/web/`) || !strings.Contains(s, "window.ok=true") || !strings.Contains(s, `\u003cscript\u003e`) || n == "" {
		t.Fatal(s, n)
	}
}

func TestRealReaderOfflineAndMobile(t *testing.T) {
	web := filepath.Join("..", "..", "easyread", "web")
	if _, e := os.Stat(filepath.Join(web, "reader.html")); e != nil {
		t.Skip("source web assets unavailable")
	}
	ws := store.NewWorkspace(t.TempDir())
	ws.Write("paper", M{"meta": M{"title_en": "Test"}})
	for _, mobile := range []bool{false, true} {
		b, _, e := HTML(ws, web, mobile)
		if e != nil {
			t.Fatal(e)
		}
		s := string(b)
		if strings.Contains(s, `<script src="/web/`) || strings.Contains(s, `href="/web/`) {
			t.Fatal("unbundled web assets")
		}
		if !strings.Contains(s, `id="pr-data"`) {
			t.Fatal("missing paper payload")
		}
		if mobile && !strings.Contains(s, `document.body.classList.add("mobile-read")`) {
			t.Fatal("mobile code missing")
		}
	}
}

func TestLocalizedRealReader(t *testing.T) {
	web := filepath.Join("..", "..", "easyread", "web")
	if _, e := os.Stat(filepath.Join(web, "reader.html")); e != nil {
		t.Skip("source assets unavailable")
	}
	ws := store.NewWorkspace(t.TempDir())
	ws.Write("paper", M{"meta": M{"title_en": "Test"}})
	b, _, e := HTMLWithLanguage(ws, web, false, "en", "ja")
	if e != nil {
		t.Fatal(e)
	}
	s := string(b)
	if !strings.Contains(s, `<html lang="en" data-lang-choice="en" data-target="ja">`) || !strings.Contains(s, `id="pr-i18n"`) {
		t.Fatal("locale not injected")
	}
}
