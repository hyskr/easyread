package server

import (
	"context"
	"github.com/hyskr/easyread/backend/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPDFBridgeAuthentication(t *testing.T) {
	h := pdfBridge(context.Background(), "secret", Object{}, t.TempDir())
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestPDFBridgeFakeEngine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell BabelDOC/model fixture; Windows requires native helper")
	}
	p := filepath.Join(t.TempDir(), "fake-claude")
	os.WriteFile(p, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"type\":\"result\",\"result\":\"translated\"}'\n"), 0700)
	h := pdfBridge(context.Background(), "secret", Object{"engine": "claude", "claude": Object{"command": p}}, t.TempDir())
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"translate me"}]}`))
	r.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "translated") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
func TestPDFCopyNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(t.TempDir(), "paper.pdf")
	os.WriteFile(source, []byte("%PDF-test"), 0600)
	first, e := pdfCopy(source, dir)
	if e != nil {
		t.Fatal(e)
	}
	second, e := pdfCopy(source, dir)
	if e != nil || first == second {
		t.Fatalf("%s %s %v", first, second, e)
	}
	if filepath.Base(second) != "paper (2).pdf" {
		t.Fatal(second)
	}
}
func TestPDFRestartStatus(t *testing.T) {
	ws := &store.Workspace{Root: t.TempDir()}
	os.Mkdir(filepath.Join(ws.Root, "babeldoc"), 0700)
	store.WriteJSON(pdfRecord(ws), Object{"state": "running", "job_id": "old"})
	pdfJobs.Lock()
	v, e := pdfStatus(ws)
	pdfJobs.Unlock()
	if e != nil || v["state"] != "error" {
		t.Fatalf("%v %v", v, e)
	}
}
func TestPDFExportFakeExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell BabelDOC/model fixture; Windows requires native helper")
	}
	a, e := New(t.TempDir(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	ws, _, e := a.Library.ImportPDF([]byte("%PDF-1.4\n%%EOF"), "paper.pdf", nil)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	cli := filepath.Join(dir, "claude")
	os.WriteFile(cli, []byte("#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"result\":\"translated\"}'\n"), 0700)
	babel := filepath.Join(dir, "babeldoc")
	os.WriteFile(babel, []byte("#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in --output) out=\"$2\"; shift;; esac; shift; done\nprintf '%%PDF-1.4\\n%%%%EOF\\n' > \"$out/source.zh.mono.pdf\"\n"), 0700)
	t.Setenv("EASYREAD_BABELDOC", babel)
	if _, e = a.Config.Save(Object{"engine": "claude", "claude": Object{"command": cli}}); e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	if e = a.pdfExportPost(w, httptest.NewRequest(http.MethodPost, "/", nil), ws, nil, Object{}); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for a.PDFBusy() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	pdfJobs.Lock()
	v, e := pdfStatus(ws)
	pdfJobs.Unlock()
	if e != nil || v["state"] != "done" {
		t.Fatalf("%v %v", v, e)
	}
	if len(array(v["files"])) != 1 {
		t.Fatal(v)
	}
}
