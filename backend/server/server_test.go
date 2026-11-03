package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/hyskr/easyread/backend/config"
	"github.com/hyskr/easyread/backend/store"
	web "github.com/hyskr/easyread/web"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T) (*App, *httptest.Server) {
	t.Helper()
	home := t.TempDir()
	cfg, e := config.New(home)
	if e != nil {
		t.Fatal(e)
	}
	_, e = cfg.Save(Object{"engine": "none", "check_updates": false})
	if e != nil {
		t.Fatal(e)
	}
	a, e := New(home, web.Files)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewServer(a)
	t.Cleanup(func() { s.Close(); a.Close() })
	return a, s
}
func call(t *testing.T, s *httptest.Server, a *App, method, p string, v any, token bool) (int, Object) {
	t.Helper()
	var body io.Reader
	if v != nil {
		b, _ := json.Marshal(v)
		body = bytes.NewReader(b)
	}
	r, _ := http.NewRequest(method, s.URL+p, body)
	if token {
		r.Header.Set("X-Token", a.Token)
	}
	resp, e := s.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	var out Object
	if e = json.NewDecoder(resp.Body).Decode(&out); e != nil {
		t.Fatal(e)
	}
	return resp.StatusCode, out
}
func fixture(t *testing.T, a *App) *store.Workspace {
	t.Helper()
	ws, _, e := a.Library.ImportPDF([]byte("%PDFfixture"), "Test.pdf", nil)
	if e != nil {
		t.Fatal(e)
	}
	e = ws.Update("paper", func(p Object) error {
		object(p["meta"])["pages"] = []any{Object{"n": 1, "w": 100, "h": 100, "img": "pages/page-001.png"}}
		p["blocks"] = []any{Object{"id": "p1", "type": "para", "page": 1, "en": "Hello", "zh": "你好"}}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return ws
}
func TestHTTPWireContracts(t *testing.T) {
	a, s := testApp(t)
	ws := fixture(t, a)
	status, lib := call(t, s, a, "GET", "/api/library", nil, false)
	if status != 200 || lib["token"] != a.Token || len(array(lib["items"])) != 1 {
		t.Fatal(status, lib)
	}
	status, st := call(t, s, a, "GET", "/api/p/"+ws.ID()+"/state", nil, false)
	if status != 200 || st["id"] != ws.ID() || object(st["item"])["status"] != "reading" {
		t.Fatal(status, st)
	}
	status, op := call(t, s, a, "POST", "/api/p/"+ws.ID()+"/ops", Object{"ops": []any{Object{"op": "paper_note", "body": "note", "at": "2026-01-01T00:00:00Z"}}}, true)
	if status != 200 || op["rev"] != float64(1) {
		t.Fatal(status, op)
	}
	status, part := call(t, s, a, "GET", "/api/p/"+ws.ID()+"/part/reader", nil, false)
	if status != 200 || object(object(part["data"])["paper_note"])["body"] != "note" {
		t.Fatal(status, part)
	}
	status, ver := call(t, s, a, "GET", "/api/p/"+ws.ID()+"/versions", nil, false)
	if status != 200 || ver["reader"] != part["version"] {
		t.Fatal(status, ver)
	}
	for _, p := range []string{"/api/version", "/api/config", "/api/prefs", "/api/jobs", "/api/trash", "/api/library/location", "/api/migrate/status", "/api/chat/models", "/api/p/" + ws.ID() + "/chat", "/api/update"} {
		status, v := call(t, s, a, "GET", p, nil, false)
		if status != 200 || v == nil {
			t.Fatalf("%s: %d %+v", p, status, v)
		}
	}
}
func TestHTTPTokenOriginAndInvalidBody(t *testing.T) {
	a, s := testApp(t)
	status, _ := call(t, s, a, "POST", "/api/prefs", Object{"ui": Object{"lang": "en"}}, false)
	if status != 403 {
		t.Fatal(status)
	}
	req, _ := http.NewRequest("POST", s.URL+"/api/prefs", strings.NewReader(`{"ui":{}}`))
	req.Header.Set("X-Token", a.Token)
	req.Header.Set("Origin", "https://evil.example")
	r, e := s.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	for _, data := range []string{`[]`, `null`, `{} {}`, `{"x":`} {
		req, _ = http.NewRequest("POST", s.URL+"/api/prefs", strings.NewReader(data))
		req.Header.Set("X-Token", a.Token)
		r, e = s.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Fatalf("%s: %d", data, r.StatusCode)
		}
	}
}
func TestHTTPCorruptReaderPreserved(t *testing.T) {
	a, s := testApp(t)
	ws := fixture(t, a)
	p := filepath.Join(ws.Root, "reader.json")
	os.WriteFile(p, []byte("{bad"), 0600)
	code, _ := call(t, s, a, "POST", "/api/p/"+ws.ID()+"/ops", Object{"ops": []any{Object{"op": "paper_note", "body": "overwrite"}}}, true)
	if code < 400 {
		t.Fatal(code)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "{bad" {
		t.Fatal("corrupt data was overwritten")
	}
}
func TestHTTPSettingsDoNotLeakOrMoveLibrary(t *testing.T) {
	a, s := testApp(t)
	status, res := call(t, s, a, "POST", "/api/config", Object{"library_dir": "/tmp/forbidden", "translation_extra": "temporary", "openai": Object{"base_url": "https://example.invalid/v1", "api_key": "secret-value-1234"}}, true)
	if status != 200 {
		t.Fatal(status, res)
	}
	cfg := object(res["config"])
	if object(cfg["openai"])["api_key"] != "••••1234" {
		t.Fatal(cfg)
	}
	raw, _ := json.Marshal(res)
	if bytes.Contains(raw, []byte("secret-value")) {
		t.Fatal("secret leaked")
	}
	saved, e := a.Config.Load()
	if e != nil {
		t.Fatal(e)
	}
	if saved["library_dir"] == "/tmp/forbidden" || saved["translation_extra"] != nil {
		t.Fatal(saved)
	}
	status, _ = call(t, s, a, "POST", "/api/config", Object{"page_cap": true}, true)
	if status != 400 {
		t.Fatal(status)
	}
}
func TestHTTPTraversalAndEmbeddedAssets(t *testing.T) {
	a, s := testApp(t)
	_ = a
	for _, p := range []string{"/", "/web/tabs.html", "/web/js/common/confirm.js", "/web/css/base.css"} {
		r, e := s.Client().Get(s.URL + p)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || len(b) == 0 {
			t.Fatalf("%s %d", p, r.StatusCode)
		}
	}
	ws := fixture(t, a)
	os.MkdirAll(filepath.Join(ws.Root, "pages"), 0700)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(secret, []byte("secret"), 0600)
	if e := os.Symlink(secret, filepath.Join(ws.Root, "pages", "escape.txt")); e == nil {
		r, e := s.Client().Get(s.URL + "/p/" + ws.ID() + "/pages/escape.txt")
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatal(r.StatusCode)
		}
	}
}
func TestHTTPRestartReadOnlyGate(t *testing.T) {
	a, s := testApp(t)
	ws := fixture(t, a)
	a.mu.Lock()
	a.status = "restart_required"
	a.mu.Unlock()
	status, _ := call(t, s, a, "POST", "/api/prefs", Object{}, true)
	if status != 409 {
		t.Fatal(status)
	}
	status, _ = call(t, s, a, "GET", "/api/p/"+ws.ID()+"/state", nil, false)
	if status != 200 {
		t.Fatal(status)
	}
	item, _ := ws.Load("item")
	if item["status"] != "unread" {
		t.Fatal(item)
	}
}
func TestHTTPImportAndPrepareRealPDF(t *testing.T) {
	a, s := testApp(t)
	data := tinyPDF()
	req, _ := http.NewRequest("POST", s.URL+"/api/import?name=sample.pdf&translate=0", bytes.NewReader(data))
	req.Header.Set("X-Token", a.Token)
	r, e := s.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	var v Object
	json.NewDecoder(r.Body).Decode(&v)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode, v)
	}
	id := text(v["id"])
	ws, e := a.Library.Workspace(id)
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		j, e := ws.Load("job")
		if e != nil {
			t.Fatal(e)
		}
		if j["state"] == "done" {
			p, _ := ws.Load("paper")
			if len(array(object(p["meta"])["pages"])) != 1 {
				t.Fatal(p)
			}
			return
		}
		if j["state"] == "error" {
			t.Fatal(j)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("preparation did not finish")
}
func tinyPDF() []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 300] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	stream := "BT /F1 12 Tf 20 260 Td (Hello native Go backend) Tj ET"
	objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, n := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", n)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return b.Bytes()
}
func TestRejectDNSRebindingHost(t *testing.T) {
	a, s := testApp(t)
	r, _ := http.NewRequest("GET", s.URL+"/api/library", nil)
	r.Host = "attacker.example"
	resp, e := s.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 403 || bytes.Contains(b, []byte(a.Token)) {
		t.Fatal(resp.StatusCode, string(b))
	}
}
