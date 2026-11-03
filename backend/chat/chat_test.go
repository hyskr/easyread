package chat

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/hyskr/easyread/backend/store"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workspace(t *testing.T) *store.Workspace {
	t.Helper()
	w := store.NewWorkspace(t.TempDir())
	if e := w.Write("paper", M{"meta": M{"title_en": "Example"}, "blocks": []any{M{"id": "p1", "en": "Evidence"}}}); e != nil {
		t.Fatal(e)
	}
	return w
}
func TestLegacyThreadsAndMutation(t *testing.T) {
	w := workspace(t)
	w.Write("chat", M{"messages": []any{M{"role": "user", "content": "hi", "at": "2025"}, M{"role": "assistant", "id": "m1", "content": "answer", "anchor": "p1", "at": "2026"}}})
	ts, e := Threads(w)
	if e != nil || len(ts) != 1 || ts[0]["id"] != "t-first" {
		t.Fatalf("%v %v", ts, e)
	}
	if _, e = Mutate(w, "pin", M{"thread": "t-first", "id": "m1"}); e != nil {
		t.Fatal(e)
	}
	d, _ := w.Load("discussion")
	if len(arr(d["entries"])) != 1 {
		t.Fatal(d)
	}
	Mutate(w, "rename", M{"thread": "t-first", "title": "new"})
	ts, _ = Threads(w)
	if ts[0]["title"] != "new" {
		t.Fatal(ts)
	}
	Mutate(w, "delete", M{"thread": "t-first"})
	ts, _ = Threads(w)
	if len(ts) != 0 {
		t.Fatal(ts)
	}
}
func imageData() string {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}
func TestImageValidationAndPaths(t *testing.T) {
	w := workspace(t)
	names, e := ReceiveImages(w, []any{imageData()}, M{"engine": "openai"})
	if e != nil || len(names) != 1 {
		t.Fatalf("%v %v", names, e)
	}
	paths := imagePaths(w, []any{M{"images": names}})
	if len(paths) != 1 {
		t.Fatal(paths)
	}
	if _, e = ReceiveImages(w, []any{imageData(), "data:image/png;base64,Zm9v"}, M{"engine": "openai"}); e == nil {
		t.Fatal("bad image accepted")
	}
	entries, _ := os.ReadDir(filepath.Join(w.Root, "chat-media"))
	if len(entries) != 1 {
		t.Fatal("partially validated images persisted")
	}
	outside := t.TempDir()
	os.RemoveAll(filepath.Join(w.Root, "chat-media"))
	os.Symlink(outside, filepath.Join(w.Root, "chat-media"))
	if _, e = ReceiveImages(w, []any{imageData()}, M{"engine": "openai"}); e == nil {
		t.Fatal("symlink escape accepted")
	}
}
func fakeConfig(base string) M {
	return M{"engine": "openai", "openai": M{"base_url": base, "model": "test"}, "chat": M{"default": "test", "models": []any{M{"id": "test", "engine": "openai", "model": "test", "base_url": base}}}}
}
func TestNDJSONStoresAnswerAndImages(t *testing.T) {
	var hadImage bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		var b M
		json.NewDecoder(r.Body).Decode(&b)
		raw, _ := json.Marshal(b)
		hadImage = bytes.Contains(raw, []byte("image_url"))
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer s.Close()
	ws := workspace(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/chat", nil)
	e := Handle(w, r, ws, fakeConfig(s.URL), M{"text": "Explain", "images": []any{imageData()}})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(w.Body.String(), `"done":true`) || !hadImage {
		t.Fatalf("%s image=%v", w.Body.String(), hadImage)
	}
	ts, _ := Threads(ws)
	if len(ts) != 1 || len(arr(ts[0]["messages"])) != 2 {
		t.Fatal(ts)
	}
	files, _ := os.ReadDir(filepath.Join(ws.Root, "chat-media"))
	if len(files) != 1 {
		t.Fatal("attachment lost")
	}
}
func TestFailureCleansAttachments(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "model unavailable", 500)
	}))
	defer s.Close()
	ws := workspace(t)
	w := httptest.NewRecorder()
	e := Handle(w, httptest.NewRequest("POST", "/", nil), ws, fakeConfig(s.URL), M{"text": "Explain", "images": []any{imageData()}})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatal(w.Body.String())
	}
	files, _ := os.ReadDir(filepath.Join(ws.Root, "chat-media"))
	if len(files) != 0 {
		t.Fatal("orphan attachment")
	}
}

type brokenWriter struct {
	header http.Header
	writes int
}

func (w *brokenWriter) Header() http.Header { return w.header }
func (w *brokenWriter) WriteHeader(int)     {}
func (w *brokenWriter) Write(b []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, errors.New("broken pipe")
	}
	return len(b), nil
}
func TestBrokenPipeSavesPartial(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Partial\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer s.Close()
	ws := workspace(t)
	w := &brokenWriter{header: http.Header{}}
	e := Handle(w, httptest.NewRequest("POST", "/", nil), ws, fakeConfig(s.URL), M{"text": "Explain", "note": "n1", "images": []any{imageData()}})
	if e != nil {
		t.Fatal(e)
	}
	ts, _ := Threads(ws)
	if len(ts) != 1 {
		t.Fatal(ts)
	}
	ms := arr(ts[0]["messages"])
	if len(ms) != 2 || !strings.Contains(str(obj(ms[1])["content"]), "Partial") || obj(ms[1])["note"] != nil {
		t.Fatal(ms)
	}
	files, _ := os.ReadDir(filepath.Join(ws.Root, "chat-media"))
	if len(files) != 1 {
		t.Fatal("retained image deleted")
	}
}

func TestCLIImagesAndColorFilteredContext(t *testing.T) {
	w := workspace(t)
	for _, engine := range []string{"claude", "codex", "codebuddy", "opencode", "zcode", "qoder", "grok", "agy", "cursor"} {
		if _, e := ReceiveImages(w, []any{imageData()}, M{"engine": engine}); e != nil {
			t.Fatal(engine, e)
		}
	}
	w.Write("reader", M{"notes": M{"red": M{"color": "pink", "quote": "red-important", "anchor": "p1"}, "yellow": M{"color": "yellow", "quote": "yellow-ignore", "anchor": "p1"}}})
	plain := prompt(w, nil, M{"content": "Explain this"})
	if strings.Contains(plain, "red-important") || !strings.Contains(plain, "2 处标记") {
		t.Fatal(plain)
	}
	red := prompt(w, nil, M{"content": "标红的是什么"})
	if !strings.Contains(red, "red-important") || strings.Contains(red, "yellow-ignore") {
		t.Fatal(red)
	}
}

func TestInvalidModelSpecificEffortBeforeStreaming(t *testing.T) {
	ws := workspace(t)
	cfg := M{"engine": "claude", "claude": M{}, "chat": M{"default": "haiku", "models": []any{M{"id": "haiku", "engine": "claude", "model": "haiku"}}}}
	w := httptest.NewRecorder()
	e := Handle(w, httptest.NewRequest("POST", "/", nil), ws, cfg, M{"text": "Explain", "chat_options": M{"reasoning_effort": "high"}})
	if e == nil {
		t.Fatal("unsupported effort accepted")
	}
	if w.Body.Len() != 0 {
		t.Fatal("validation happened after streaming")
	}
}
