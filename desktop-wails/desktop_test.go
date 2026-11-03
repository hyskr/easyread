package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeepLinkUsesIndependentScheme(t *testing.T) {
	target, err := openTarget("easyread-wails://open?id=paper&block=b-p3-2")
	if err != nil || target != "/open?block=b-p3-2&id=paper" {
		t.Fatalf("%q %v", target, err)
	}
	if _, err = openTarget("easyread://open?id=paper"); err == nil {
		t.Fatal("Electron scheme accepted")
	}
}
func TestShutdownPreservesBusyBackend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/library" {
			json.NewEncoder(w).Encode(map[string]string{"token": "test"})
			return
		}
		if r.Header.Get("X-Token") != "test" {
			t.Error("missing token")
		}
		http.Error(w, "busy", 400)
	}))
	defer server.Close()
	a := App{url: server.URL}
	if a.stopService() == nil {
		t.Fatal("busy service must prevent exit")
	}
}
func TestShutdownAcceptsIdleBackend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/library" {
			json.NewEncoder(w).Encode(map[string]string{"token": "test"})
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	a := App{url: server.URL}
	if err := a.stopService(); err != nil {
		t.Fatal(err)
	}
}
