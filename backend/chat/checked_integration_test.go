package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tr "github.com/hyskr/easyread/backend/translate"
)

func TestCheckedUserPathsBlockBeforeGeneration(t *testing.T) {
	var probes, generations atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/" {
			probes.Add(1)
			http.Error(w, "unsupported_country", 403)
			return
		}
		generations.Add(1)
		t.Error("generation called after failed preflight")
		http.Error(w, "should not execute", 500)
	}))
	defer provider.Close()
	cfg := fakeConfig(provider.URL)
	ws := workspace(t)
	ws.Write("reader", M{"notes": M{"n1": M{"anchor": "p1", "body": "Question"}}})
	for _, kind := range []string{"chat", "notehelp"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", nil)
		var e error
		if kind == "chat" {
			e = Handle(w, r, ws, cfg, M{"text": "Question"})
		} else {
			e = NoteHelp(w, r, ws, cfg, M{"mode": "draft"})
		}
		if e != nil {
			t.Fatal(kind, e)
		}
		if !strings.Contains(w.Body.String(), `"error"`) || strings.Contains(w.Body.String(), `"done":true`) {
			t.Fatal(kind, w.Body.String())
		}
	}
	if _, e := TranslateSelection(context.Background(), ws, cfg, M{"text": "word"}); e == nil {
		t.Fatal("selection ignored failed preflight")
	}
	if e := tr.Answer(context.Background(), ws, cfg, "n1"); e == nil {
		t.Fatal("answer ignored failed preflight")
	}
	if e := tr.Retranslate(context.Background(), ws, cfg, "p1", ""); e == nil {
		t.Fatal("retranslation ignored failed preflight")
	}
	if probes.Load() != 5 || generations.Load() != 0 {
		t.Fatal(probes.Load(), generations.Load())
	}
	threads, e := Threads(ws)
	if e != nil || len(threads) != 0 {
		t.Fatal("failed model persisted chat", threads, e)
	}
}

func TestCheckedSelectionCanceledBeforePreflight(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(204) }))
	defer provider.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := TranslateSelection(ctx, workspace(t), fakeConfig(provider.URL), M{"text": "word"})
	if !errors.Is(e, context.Canceled) || requests.Load() != 0 {
		t.Fatal(e, requests.Load())
	}
}
