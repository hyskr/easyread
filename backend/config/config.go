// Package config manages interoperable EasyRead settings and endpoint-scoped secrets.
package config

import (
	"encoding/json"
	"fmt"
	"github.com/hyskr/easyread/backend/store"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Object = map[string]any
type Manager struct{ Home string }

func New(home string) (*Manager, error) {
	if home == "" {
		home = os.Getenv("EASYREAD_HOME")
	}
	if home == "" {
		wd, _ := os.Getwd()
		if _, e := os.Stat(filepath.Join(wd, "pyproject.toml")); e == nil {
			home = wd
		} else {
			h, e := os.UserHomeDir()
			if e != nil {
				return nil, e
			}
			home = filepath.Join(h, "EasyRead")
		}
	}
	home = expand(home)
	p, e := filepath.Abs(home)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(p, 0755); e != nil {
		return nil, e
	}
	return &Manager{Home: p}, nil
}
func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	return p
}
func Defaults(home string) Object {
	d := Object{"library_dir": filepath.Join(home, "library"), "port": 8765, "engine": "claude", "auto_translate": true, "target": "zh", "translation_instructions": "", "translation_prompt": nil, "translate_figure_text": true, "check_updates": true, "batch_pages": 2, "paper_concurrency": 1, "page_cap": 60, "concurrency": 0, "concurrency_v": 2, "figure_images": Object{"enabled": false, "provider": "codex", "base_url": "", "api_key": "", "model": "", "timeout": 600}, "openai": Object{"preset": "", "base_url": "", "api": "chat", "api_key": "", "model": "", "vision": false, "timeout": 600}, "chat": Object{"models": []any{Object{"id": "opus", "name": "Claude Opus", "engine": "claude", "model": "opus"}, Object{"id": "sonnet", "name": "Claude Sonnet", "engine": "claude", "model": "sonnet"}, Object{"id": "gpt", "name": "GPT", "engine": "codex", "model": ""}}, "default": "opus"}}
	for _, name := range []string{"claude", "codex", "codebuddy", "zcode", "qoder", "grok", "agy", "cursor", "opencode"} {
		command := ""
		switch name {
		case "claude", "codex", "codebuddy", "zcode":
			command = name
		}
		v := Object{"command": command, "model": "", "extra_args": []any{}, "timeout": 1200}
		switch name {
		case "claude", "codex", "grok", "agy", "cursor":
			v["reasoning_effort"] = ""
		}
		if name == "codex" {
			v["service_tier"] = ""
		}
		d[name] = v
	}
	return d
}
func object(v any) Object {
	m, _ := v.(map[string]any)
	if m == nil {
		return Object{}
	}
	return m
}
func text(v any) string { s, _ := v.(string); return s }
func num(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}
func clone(v Object) Object {
	b, _ := json.Marshal(v)
	var c Object
	_ = json.Unmarshal(b, &c)
	if c == nil {
		return Object{}
	}
	return c
}
func Merge(base, over Object) Object {
	out := clone(base)
	for k, v := range over {
		a, aok := out[k].(map[string]any)
		b, bok := v.(map[string]any)
		if aok && bok {
			out[k] = Merge(a, b)
		} else {
			out[k] = v
		}
	}
	return out
}
func (m *Manager) saved() (Object, error) {
	v, e := store.ReadJSON(filepath.Join(m.Home, "config.json"))
	if e != nil {
		return nil, e
	}
	if v == nil {
		v = Object{}
	}
	if num(v["concurrency_v"]) != 2 {
		if num(v["concurrency"]) == 1 {
			v["concurrency"] = 0
		} else if num(v["concurrency"]) > 4 {
			v["concurrency"] = 4
		}
	}
	v["concurrency_v"] = 2
	return v, nil
}
func tempLibrary() string {
	if p := os.Getenv("EASYREAD_LIBRARY"); p != "" {
		return p
	}
	return os.Getenv("COREAD_LIBRARY")
}
func (m *Manager) Load() (Object, error) {
	v, e := m.saved()
	if e != nil {
		return nil, e
	}
	cfg := Merge(Defaults(m.Home), v)
	if p := tempLibrary(); p != "" {
		cfg["library_dir"] = p
	}
	return cfg, nil
}
func KeyID(preset, baseURL string) string {
	if preset != "" {
		return preset
	}
	return "url:" + strings.TrimRight(strings.TrimSpace(baseURL), "/")
}
func SavedKeys(section Object) Object {
	keys := clone(object(section["keys"]))
	current := KeyID(text(section["preset"]), text(section["base_url"]))
	legacy := text(keys[""])
	delete(keys, "")
	if text(section["preset"]) == "" && legacy != "" {
		if _, ok := keys[current]; !ok {
			keys[current] = legacy
		}
	}
	if key := text(section["api_key"]); key != "" && !strings.HasPrefix(key, "••••") {
		if _, ok := keys[current]; !ok {
			keys[current] = key
		}
	}
	return keys
}
func Resolve(section Object, preset, baseURL string) string {
	return text(SavedKeys(section)[KeyID(preset, baseURL)])
}
func WithKey(current, patch Object) Object {
	out := clone(patch)
	key := text(out["api_key"])
	delete(out, "api_key")
	preset := text(current["preset"])
	base := text(current["base_url"])
	if v, ok := out["preset"]; ok {
		preset = text(v)
	}
	if v, ok := out["base_url"]; ok {
		base = text(v)
	}
	keys := SavedKeys(current)
	for k, v := range object(patch["keys"]) {
		if key := text(v); key != "" && !strings.HasPrefix(key, "••••") {
			keys[k] = key
		}
	}
	slot := KeyID(preset, base)
	if key != "" && !strings.HasPrefix(key, "••••") {
		keys[slot] = strings.TrimSpace(key)
	}
	out["keys"] = keys
	out["api_key"] = text(keys[slot])
	delete(out, "has_key")
	delete(out, "saved_keys")
	return out
}
func (m *Manager) Save(patch Object) (Object, error) {
	unlock, e := store.Lock(m.Home)
	if e != nil {
		return nil, e
	}
	defer unlock()
	saved, e := m.saved()
	if e != nil {
		return nil, e
	}
	current := Merge(Defaults(m.Home), saved)
	patch = clone(patch)
	for _, name := range []string{"openai", "figure_images"} {
		if p, ok := patch[name].(map[string]any); ok {
			patch[name] = WithKey(object(current[name]), p)
		}
	}
	delete(patch, "translation_prompt_default")
	delete(patch, "translation_prompt_effective")
	cfg := Merge(current, patch)
	if e = store.WriteJSON(filepath.Join(m.Home, "config.json"), cfg); e != nil {
		return nil, e
	}
	if p := tempLibrary(); p != "" {
		cfg["library_dir"] = p
	}
	return cfg, nil
}
func Public(cfg Object) Object {
	out := clone(cfg)
	out["translation_prompt_default"] = DefaultPrompt(text(cfg["target"]), promptLocale())
	out["translation_prompt_effective"] = EffectivePrompt(cfg, promptLocale())
	for _, name := range []string{"openai", "figure_images"} {
		section, ok := out[name].(map[string]any)
		if !ok {
			continue
		}
		keys := SavedKeys(object(cfg[name]))
		names := []string{}
		for k, v := range keys {
			if text(v) != "" {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		delete(section, "keys")
		key := []rune(text(section["api_key"]))
		section["has_key"] = len(key) > 0
		section["api_key"] = ""
		if len(key) > 0 {
			start := len(key) - 4
			if start < 0 {
				start = 0
			}
			section["api_key"] = "••••" + string(key[start:])
		}
		section["saved_keys"] = names
	}
	return out
}
func (m *Manager) Public(cfg Object) Object {
	out := Public(cfg)
	locale := promptLocale()
	if os.Getenv("EASYREAD_LANG") == "" {
		if prefs, e := m.Prefs(); e == nil {
			if v := text(object(prefs["ui"])["lang"]); v == "zh" || v == "zh-Hant" || v == "en" {
				locale = v
			}
		}
	}
	out["translation_prompt_default"] = DefaultPrompt(text(cfg["target"]), locale)
	out["translation_prompt_effective"] = EffectivePrompt(cfg, locale)
	return out
}
func (m *Manager) LibraryDir() (string, error) {
	cfg, e := m.Load()
	if e != nil {
		return "", e
	}
	p := expand(text(cfg["library_dir"]))
	if p == "" {
		return "", fmt.Errorf("library_dir is empty")
	}
	p, e = filepath.Abs(p)
	if e != nil {
		return "", e
	}
	if e = os.MkdirAll(p, 0755); e != nil {
		return "", e
	}
	return p, nil
}
func (m *Manager) FirstRun() bool {
	_, e := os.Stat(filepath.Join(m.Home, "config.json"))
	return os.IsNotExist(e)
}
func (m *Manager) prefsPath() (string, error) {
	if tempLibrary() != "" {
		p, e := m.LibraryDir()
		if e != nil {
			return "", e
		}
		return filepath.Join(p, ".prefs.json"), nil
	}
	return filepath.Join(m.Home, "prefs.json"), nil
}
func (m *Manager) Prefs() (Object, error) {
	p, e := m.prefsPath()
	if e != nil {
		return nil, e
	}
	v, e := store.ReadJSON(p)
	if v == nil && e == nil {
		v = Object{}
	}
	return v, e
}
func (m *Manager) SavePrefs(patch Object) (Object, error) {
	p, e := m.prefsPath()
	if e != nil {
		return nil, e
	}
	unlock, e := store.Lock(filepath.Dir(p))
	if e != nil {
		return nil, e
	}
	defer unlock()
	v, e := store.ReadJSON(p)
	if e != nil {
		return nil, e
	}
	if v == nil {
		v = Object{}
	}
	for _, name := range []string{"reader", "keys", "ui", "library", "import"} {
		if over, ok := patch[name].(map[string]any); ok {
			cur := object(v[name])
			for k, x := range over {
				cur[k] = x
			}
			v[name] = cur
		}
	}
	if e = store.WriteJSON(p, v); e != nil {
		return nil, e
	}
	return v, nil
}
