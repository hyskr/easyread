package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSaveAcknowledgementRequiresActiveRequest(t *testing.T) {
	for _, initial := range []string{"", "idle", "stopping"} {
		phase := initial
		if acceptSavedPhase(&phase) == nil {
			t.Fatalf("accepted stale or duplicate save acknowledgement in %q", initial)
		}
		if phase != initial {
			t.Fatal("failed acknowledgement changed quit phase")
		}
	}
	phase := "waiting"
	if err := acceptSavedPhase(&phase); err != nil || phase != "stopping" {
		t.Fatal("active save did not advance")
	}
}
func TestBackendExitObservedWithoutHTTP(t *testing.T) {
	done := make(chan struct{})
	app := &App{backendDone: done}
	if app.backendExited() {
		t.Fatal("running backend marked exited")
	}
	close(done)
	if !app.backendExited() {
		t.Fatal("exited backend not detected")
	}
}
func TestBusyBackendPreventsShutdown(t *testing.T) {
	posted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/library" {
			w.Write([]byte(`{"token":"test-token"}`))
			return
		}
		if r.URL.Path != "/api/shutdown" || r.Method != "POST" || r.Header.Get("X-Token") != "test-token" {
			t.Error("invalid shutdown request")
		}
		posted = true
		http.Error(w, `{"error":"busy"}`, http.StatusBadRequest)
	}))
	defer server.Close()
	app := &App{url: server.URL}
	if app.stopService() == nil {
		t.Fatal("busy backend allowed quit")
	}
	if !posted {
		t.Fatal("shutdown was not requested")
	}
	if app.closing.Load() {
		t.Fatal("busy backend marked app closing")
	}
}
func TestSuccessfulBackendShutdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/library" {
			w.Write([]byte(`{"token":"test-token"}`))
		} else {
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer server.Close()
	if err := (&App{url: server.URL}).stopService(); err != nil {
		t.Fatal(err)
	}
}
