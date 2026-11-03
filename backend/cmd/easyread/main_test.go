package main

import (
	"encoding/json"
	"fmt"
	"github.com/hyskr/easyread/backend/config"
	"github.com/hyskr/easyread/backend/store"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var cliBinary string

func TestMain(m *testing.M) {
	dir, e := os.MkdirTemp("", "easyread-cli-test-")
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	cliBinary = filepath.Join(dir, "easyread")
	if runtime.GOOS == "windows" {
		cliBinary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", cliBinary, ".")
	if out, e := cmd.CombinedOutput(); e != nil {
		fmt.Fprintln(os.Stderr, string(out), e)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
func cliEnv(home string) []string {
	out := []string{}
	for _, v := range os.Environ() {
		if strings.HasPrefix(v, "EASYREAD_HOME=") || strings.HasPrefix(v, "EASYREAD_LIBRARY=") || strings.HasPrefix(v, "COREAD_LIBRARY=") {
			continue
		}
		out = append(out, v)
	}
	return append(out, "EASYREAD_HOME="+home, "EASYREAD_LANG=en")
}
func cliRun(home string, args ...string) (string, error) {
	cmd := exec.Command(cliBinary, args...)
	cmd.Env = cliEnv(home)
	b, e := cmd.CombinedOutput()
	return string(b), e
}
func cliFixture(t *testing.T) (string, *store.Workspace) {
	t.Helper()
	home := t.TempDir()
	lib, e := store.NewLibrary(filepath.Join(home, "library"))
	if e != nil {
		t.Fatal(e)
	}
	w, _, e := lib.ImportPDF([]byte("%PDF-1.4\nfixture"), "Fixture.pdf", nil)
	if e != nil {
		t.Fatal(e)
	}
	w.Update("paper", func(p M) error {
		p["blocks"] = []any{M{"id": "p1", "type": "para", "page": 1, "en": "hello", "zh": "你好"}}
		p["meta"].(map[string]any)["pages"] = []any{M{"n": 1}, M{"n": 2}}
		return nil
	})
	return home, w
}
func inputFile(t *testing.T, dir string, v any) string {
	t.Helper()
	p := filepath.Join(dir, fmt.Sprintf("input-%d.json", time.Now().UnixNano()))
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, b, 0600)
	return p
}
func TestCLIListStatusDoNotStartQueuedJobs(t *testing.T) {
	home, w := cliFixture(t)
	w.Write("job", M{"state": "queued", "type": "translate"})
	before, _ := os.ReadFile(filepath.Join(w.Root, "job.json"))
	text, e := cliRun(home, "list")
	if e != nil || !strings.Contains(text, w.ID()+"  Fixture  [0/0 页] queued") {
		t.Fatal(text, e)
	}
	text, e = cliRun(home, "status", w.ID()[:6])
	if e != nil || !strings.Contains(text, "用户改过的译文") || !strings.Contains(text, "后台任务：queued") {
		t.Fatal(text, e)
	}
	after, _ := os.ReadFile(filepath.Join(w.Root, "job.json"))
	if string(before) != string(after) {
		t.Fatal("read-only CLI started or changed job")
	}
}
func TestCLIMergeArrayAndDiscussionDedup(t *testing.T) {
	home, w := cliFixture(t)
	p := inputFile(t, home, []any{M{"op": "paper_note", "body": "offline note"}})
	if text, e := cliRun(home, "merge", w.ID(), "--from", p); e != nil || !strings.Contains(text, "并入 1 个操作") {
		t.Fatal(text, e)
	}
	r, _ := w.Load("reader")
	if r["paper_note"].(map[string]any)["body"] != "offline note" {
		t.Fatal(r)
	}
	p = inputFile(t, home, []any{M{"id": "d1", "anchor": "p1", "body": "first"}, M{"id": "d1", "anchor": "p1", "body": "second"}})
	if text, e := cliRun(home, "discuss", w.ID(), "--from", p); e != nil {
		t.Fatal(text, e)
	}
	d, _ := w.Load("discussion")
	items := d["entries"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["body"] != "second" {
		t.Fatal(d)
	}
	p = inputFile(t, home, M{"id": "d1", "body": "updated"})
	if text, e := cliRun(home, "discuss", w.ID(), "--from", p); e != nil {
		t.Fatal(text, e)
	}
	d, _ = w.Load("discussion")
	if len(d["entries"].([]any)) != 1 {
		t.Fatal(d)
	}
}
func TestCLIBlocksDoneWithoutReplace(t *testing.T) {
	home, w := cliFixture(t)
	p := inputFile(t, home, M{"blocks": []any{M{"id": "p2", "type": "para", "page": 2, "en": "second", "zh": "二"}}})
	if text, e := cliRun(home, "blocks", w.ID(), "--from", p, "--done", "2"); e != nil {
		t.Fatal(text, e)
	}
	paper, _ := w.Load("paper")
	if len(paper["blocks"].([]any)) != 2 {
		t.Fatal("existing block replaced", paper)
	}
	done := paper["translation"].(map[string]any)["done_pages"].([]any)
	if len(done) != 1 || done[0] != float64(2) {
		t.Fatal("completion not recorded", paper)
	}
}
func TestCLICheckReportsRefsAnchorsAndExitStatus(t *testing.T) {
	home, w := cliFixture(t)
	w.Update("paper", func(p M) error {
		p["references"] = []any{M{"id": 1}}
		p["blocks"] = []any{M{"id": "p1", "type": "para", "en": "Missing reference [2]"}}
		return nil
	})
	w.Write("discussion", M{"entries": []any{M{"id": "d1", "anchor": "absent", "body": "test"}}})
	text, e := cliRun(home, "check", w.ID())
	if e == nil || !strings.Contains(text, "引用 [2]") || !strings.Contains(text, "锚点不存在") {
		t.Fatal(text, e)
	}
}
func TestCLIMigrateCopiesUnknownAndPreservesExisting(t *testing.T) {
	home := t.TempDir()
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "source.pdf"), []byte("%PDF old"), 0600)
	store.WriteJSON(filepath.Join(src, "paper.json"), M{"meta": M{"title_en": "Old"}, "custom": true})
	store.WriteJSON(filepath.Join(src, "reader.json"), M{"notes": M{}, "custom": true})
	os.WriteFile(filepath.Join(src, ".preserve"), []byte("hidden"), 0600)
	os.WriteFile(filepath.Join(src, "old.html"), []byte("ignored"), 0600)
	text, e := cliRun(home, "migrate", src)
	if e != nil {
		t.Fatal(text, e)
	}
	lib, _ := store.NewLibrary(filepath.Join(home, "library"))
	all, _ := lib.All()
	if len(all) != 1 {
		t.Fatal(all)
	}
	w := all[0]
	if !fileExists(filepath.Join(w.Root, ".preserve")) || fileExists(filepath.Join(w.Root, "old.html")) {
		t.Fatal("legacy ignore rules wrong")
	}
	reader, _ := w.Load("reader")
	if reader["custom"] != true || reader["paper_note"] == nil {
		t.Fatal(reader)
	}
	w.Update("paper", func(p M) error { p["custom"] = "newer"; return nil })
	if text, e = cliRun(home, "migrate", src); e != nil {
		t.Fatal(text, e)
	}
	p, _ := w.Load("paper")
	if p["custom"] != "newer" {
		t.Fatal("duplicate migration overwrote library")
	}
}
func TestCLIServeReusesExistingWithoutStartingJobs(t *testing.T) {
	home, w := cliFixture(t)
	w.Write("job", M{"state": "queued"})
	keep := make(chan bool, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			json.NewEncoder(rw).Encode(M{"config": M{}})
		case "/api/library":
			json.NewEncoder(rw).Encode(M{"token": "token"})
		case "/api/presence/keep":
			if r.Header.Get("X-Token") != "token" {
				rw.WriteHeader(403)
				return
			}
			keep <- true
			rw.Write([]byte(`{}`))
		default:
			rw.WriteHeader(404)
		}
	}))
	defer srv.Close()
	store.WriteJSON(filepath.Join(home, ".server.json"), M{"url": srv.URL})
	text, e := cliRun(home, "serve")
	if e != nil || !strings.Contains(text, "服务已在运行") {
		t.Fatal(text, e)
	}
	select {
	case <-keep:
	default:
		t.Fatal("existing backend not kept alive")
	}
	job, _ := w.Load("job")
	if job["state"] != "queued" {
		t.Fatal("reuse started local workers")
	}
	cfg, _ := config.New(home)
	if runningServer(cfg) != srv.URL {
		t.Fatal("running server not recognized")
	}
}
func TestCLIReplaceOnlySelectedPages(t *testing.T) {
	home, w := cliFixture(t)
	w.Update("paper", func(p M) error {
		p["blocks"] = append(p["blocks"].([]any), M{"id": "old2", "type": "para", "page": 2, "en": "old"})
		return nil
	})
	p := inputFile(t, home, M{"blocks": []any{M{"id": "new2", "type": "para", "page": 2, "en": "new", "zh": "新"}}})
	text, e := cliRun(home, "blocks", w.ID(), "--from", p, "--done", "2", "--replace")
	if e != nil {
		t.Fatal(text, e)
	}
	paper, _ := w.Load("paper")
	blocks := paper["blocks"].([]any)
	if len(blocks) != 2 {
		t.Fatal(paper)
	}
	for _, v := range blocks {
		if v.(map[string]any)["id"] == "old2" {
			t.Fatal("selected old block retained")
		}
	}
}
func TestCLITeXRenderingAndEscapedMath(t *testing.T) {
	_, w := cliFixture(t)
	w.Update("paper", func(p M) error {
		p["blocks"] = []any{M{"id": "m1", "type": "math", "tex": `\frac{1}{2}`}, M{"id": "p1", "type": "para", "en": `Price \$5 and $x^2$`}}
		return nil
	})
	v, e := inspectPaper(w)
	if e != nil {
		t.Fatal(e)
	}
	if v["tex"] != 2 {
		t.Fatal(v)
	}
	if len(v["problems"].([]string)) != 0 {
		t.Fatal(v)
	}
}
