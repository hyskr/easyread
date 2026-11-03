package config

import (
	"encoding/json"
	"github.com/hyskr/easyread/backend/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveMasksAndScopesKeys(t *testing.T) {
	m, _ := New(t.TempDir())
	cfg, e := m.Save(Object{"unknown": Object{"keep": true}, "openai": Object{"base_url": "https://one.test/v1", "api_key": "secret-one"}})
	if e != nil {
		t.Fatal(e)
	}
	pub := Public(cfg)
	b, _ := json.Marshal(pub)
	if strings.Contains(string(b), "secret-one") {
		t.Fatal("public leaks key")
	}
	cfg, e = m.Save(Object{"openai": Object{"base_url": "https://two.test/v1", "api_key": "••••-one"}})
	if e != nil {
		t.Fatal(e)
	}
	if object(cfg["openai"])["api_key"] != "" {
		t.Fatal("key copied to another endpoint")
	}
	cfg, _ = m.Save(Object{"openai": Object{"base_url": "https://one.test/v1", "api_key": ""}})
	if object(cfg["openai"])["api_key"] != "secret-one" || object(cfg["unknown"])["keep"] != true {
		t.Fatal(cfg)
	}
}
func TestTemporaryLibraryNotSavedAndPrefsIsolated(t *testing.T) {
	m, _ := New(t.TempDir())
	tmp := t.TempDir()
	t.Setenv("EASYREAD_LIBRARY", tmp)
	cfg, e := m.Save(Object{"port": 9999})
	if e != nil || cfg["library_dir"] != tmp {
		t.Fatal(cfg, e)
	}
	saved, _ := store.ReadJSON(filepath.Join(m.Home, "config.json"))
	if saved["library_dir"] == tmp {
		t.Fatal("temporary override persisted")
	}
	if _, e = m.SavePrefs(Object{"reader": Object{"theme": "dark"}, "unallowed": Object{"x": true}}); e != nil {
		t.Fatal(e)
	}
	p, _ := m.Prefs()
	if _, ok := p["unallowed"]; ok {
		t.Fatal(p)
	}
	if _, e = os.Stat(filepath.Join(tmp, ".prefs.json")); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(m.Home, "prefs.json")); !os.IsNotExist(e) {
		t.Fatal("global preferences changed")
	}
}
func TestCorruptConfigCannotBeOverwritten(t *testing.T) {
	m, _ := New(t.TempDir())
	p := filepath.Join(m.Home, "config.json")
	os.WriteFile(p, []byte("{bad"), 0600)
	if _, e := m.Save(Object{"engine": "none"}); e == nil {
		t.Fatal("corrupt config overwritten")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "{bad" {
		t.Fatal(string(b))
	}
}
func TestMigration(t *testing.T) {
	m, _ := New(t.TempDir())
	store.WriteJSON(filepath.Join(m.Home, "config.json"), Object{"concurrency": 1})
	c, _ := m.Load()
	if num(c["concurrency"]) != 0 || num(c["concurrency_v"]) != 2 {
		t.Fatal(c)
	}
}
