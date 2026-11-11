package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadedUpdateSurvivesNewAppInstance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := filepath.Join(updateCache(), "99.0.0")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("99.0.0"), 0600); err != nil {
		t.Fatal(err)
	}
	first := &App{}
	if first.cachedUpdate() != "99.0.0" {
		t.Fatal("cache not detected")
	}
	second := &App{}
	if second.UpdateStatus()["cached"] != "99.0.0" {
		t.Fatal("cache lost after restart")
	}
}
