package jobs

import (
	"context"
	"errors"
	"fmt"
	"github.com/hyskr/easyread/backend/config"
	"github.com/hyskr/easyread/backend/store"
	tr "github.com/hyskr/easyread/backend/translate"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, count int) (*store.Library, *store.Workspace, *config.Manager) {
	t.Helper()
	home := t.TempDir()
	lib, e := store.NewLibrary(filepath.Join(home, "library"))
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(lib.Root, "paper001")
	_ = os.MkdirAll(root, 0700)
	w := store.NewWorkspace(root)
	pages := []any{}
	for n := 1; n <= count; n++ {
		pages = append(pages, Object{"n": n})
	}
	if e = w.Write("paper", Object{"meta": Object{"page_count": count, "pages": pages}, "blocks": []any{}, "translation": Object{}}); e != nil {
		t.Fatal(e)
	}
	cfg, e := config.New(home)
	if e != nil {
		t.Fatal(e)
	}
	_, e = cfg.Save(Object{"engine": "openai", "paper_concurrency": 1, "batch_pages": 1, "concurrency": 1, "page_cap": 2})
	if e != nil {
		t.Fatal(e)
	}
	return lib, w, cfg
}
func waitState(t *testing.T, w *store.Workspace, want string) Object {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, e := w.Load("job")
		if e != nil {
			t.Fatal(e)
		}
		if j["state"] == want {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, _ := w.Load("job")
	t.Fatalf("wanted %s, got %#v", want, j)
	return nil
}
func fakeRun(ctx context.Context, cfg Object, prompt, cwd string, images []string) (string, Object, error) {
	n := 1
	for i := 1; i <= 20; i++ {
		if strings.Contains(prompt, fmt.Sprintf("PAGE %d =====", i)) {
			n = i
			break
		}
	}
	return fmt.Sprintf(`{"blocks":[{"id":"p%d","type":"para","page":%d,"en":"Source.","zh":"译文。"}]}`, n, n), Object{"calls": 1}, nil
}
func TestCapConfirmAndTargetRetention(t *testing.T) {
	lib, w, cfg := fixture(t, 3)
	old := tr.Run
	var calls atomic.Int32
	tr.Run = func(c context.Context, o Object, p, s string, i []string) (string, Object, error) {
		calls.Add(1)
		if o["translation_extra"] != "Keep symbols" {
			t.Error(o)
		}
		return fakeRun(c, o, p, s, i)
	}
	defer func() { tr.Run = old }()
	m := New(lib, cfg)
	defer m.Close()
	if _, e := m.Enqueue(w, Object{"translation_extra": "Keep symbols", "target": "ja"}); e != nil {
		t.Fatal(e)
	}
	j := waitState(t, w, "confirm")
	if calls.Load() != 0 || tr.Num(j["total"]) != 3 {
		t.Fatal(j)
	}
	_, _ = cfg.Save(Object{"target": "fr"})
	if _, e := m.Enqueue(w, Object{"confirmed": "yes"}); e != nil {
		t.Fatal(e)
	}
	waitState(t, w, "confirm")
	if calls.Load() != 0 {
		t.Fatal("truthy string bypassed cap")
	}
	if _, e := m.Enqueue(w, Object{"confirmed": true}); e != nil {
		t.Fatal(e)
	}
	waitState(t, w, "done")
	p, _ := w.Load("paper")
	if tr.Obj(p["meta"])["target"] != "ja" || calls.Load() != 3 {
		t.Fatal(p, calls.Load())
	}
}
func TestPartialRetryOnlyFailedAndKeepInstructions(t *testing.T) {
	lib, w, cfg := fixture(t, 2)
	old := tr.Run
	defer func() { tr.Run = old }()
	fail := atomic.Bool{}
	fail.Store(true)
	tr.Run = func(c context.Context, o Object, p, s string, i []string) (string, Object, error) {
		if strings.Contains(p, "PAGE 2 =====") && fail.Load() {
			return "", nil, errors.New("temporary test failure")
		}
		return fakeRun(c, o, p, s, i)
	}
	m := New(lib, cfg)
	defer m.Close()
	_, e := m.Enqueue(w, Object{"translation_extra": "Keep ABC"})
	if e != nil {
		t.Fatal(e)
	}
	j := waitState(t, w, "partial")
	if len(tr.Obj(j["failed"])) != 1 {
		t.Fatal(j)
	}
	fail.Store(false)
	_, e = m.Enqueue(w, Object{"failed": true})
	if e != nil {
		t.Fatal(e)
	}
	j = waitState(t, w, "done")
	if j["translation_extra"] != "Keep ABC" || tr.Num(j["total"]) != 1 {
		t.Fatal(j)
	}
	p, _ := w.Load("paper")
	if len(tr.List(tr.Obj(p["translation"])["done_pages"])) != 2 {
		t.Fatal(p)
	}
}
func TestCancelAndIndependentSmallQueue(t *testing.T) {
	lib, w, cfg := fixture(t, 1)
	old := tr.Run
	defer func() { tr.Run = old }()
	started := make(chan struct{})
	tr.Run = func(c context.Context, o Object, p, s string, i []string) (string, Object, error) {
		if strings.HasPrefix(p, "Answer this") {
			return "Answer", nil, nil
		}
		close(started)
		<-c.Done()
		return "", nil, c.Err()
	}
	_ = w.Write("reader", Object{"notes": Object{"note1": Object{"body": "Explain"}}})
	m := New(lib, cfg)
	defer m.Close()
	_, e := m.Enqueue(w, Object{})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("bulk did not start")
	}
	if _, e = m.Small("answer", w.ID(), Object{"note": "note1"}); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	done := false
	for time.Now().Before(deadline) {
		s := m.Status(w.ID())
		if len(s) > 0 && s[0]["state"] == "done" {
			done = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !done {
		t.Fatal(m.Status(w.ID()))
	}
	m.Cancel(w.ID())
	waitState(t, w, "cancelled")
	d, _ := w.Load("discussion")
	if len(tr.List(d["entries"])) != 1 {
		t.Fatal(d)
	}
}
func TestRestartResumesTextButNeverInFlightImage(t *testing.T) {
	lib, w, cfg := fixture(t, 1)
	_ = w.Write("job", Object{"state": "running", "type": "translate", "translate": true, "scope": "all"})
	old := tr.Run
	tr.Run = fakeRun
	defer func() { tr.Run = old }()
	m := New(lib, cfg)
	waitState(t, w, "done")
	m.Close()
	_ = w.Write("job", Object{"state": "running", "type": "figure-images", "translate": true})
	m = New(lib, cfg)
	defer m.Close()
	waitState(t, w, "cancelled")
}
func TestUnreadableJobPreserved(t *testing.T) {
	lib, w, cfg := fixture(t, 1)
	path := filepath.Join(w.Root, "job.json")
	_ = os.WriteFile(path, []byte("{corrupted"), 0600)
	m := New(lib, cfg)
	defer m.Close()
	if _, e := m.Enqueue(w, Object{}); e == nil {
		t.Fatal("corrupted state overwritten")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{corrupted" {
		t.Fatal(string(b))
	}
}
func TestExplicitPagesRestartSkipsCommittedCheckpoint(t *testing.T) {
	lib, w, cfg := fixture(t, 2)
	_ = w.Write("job", Object{"state": "running", "type": "translate", "translate": true, "pages": []int{1, 2}, "completed_pages": []int{1}, "scope": "all"})
	_ = w.Update("paper", func(p Object) error {
		p["blocks"] = []any{Object{"id": "p1", "type": "para", "page": 1, "en": "Already done", "zh": "Saved"}}
		p["translation"] = Object{"done_pages": []int{1}}
		return nil
	})
	old := tr.Run
	defer func() { tr.Run = old }()
	var calls atomic.Int32
	tr.Run = func(c context.Context, o Object, p, s string, i []string) (string, Object, error) {
		calls.Add(1)
		if strings.Contains(p, "===== PAGE 1 =====") {
			t.Error("checkpointed page retried")
		}
		return fakeRun(c, o, p, s, i)
	}
	m := New(lib, cfg)
	defer m.Close()
	waitState(t, w, "done")
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}
