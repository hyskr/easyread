package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hyskr/easyread/backend/store"
)

type selectionModel struct {
	mu              sync.Mutex
	prompts, models []string
	answer          string
	fail            bool
}

func (m *selectionModel) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var b Object
	_ = json.NewDecoder(r.Body).Decode(&b)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.models = append(m.models, text(b["model"]))
	msgs := array(b["messages"])
	m.prompts = append(m.prompts, text(object(msgs[0])["content"]))
	if m.fail {
		http.Error(w, "provider unavailable", 500)
		return
	}
	_ = json.NewEncoder(w).Encode(Object{"choices": []any{Object{"message": Object{"content": m.answer}, "finish_reason": "stop"}}})
}
func (m *selectionModel) count() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.prompts) }
func (m *selectionModel) source(t *testing.T) Object {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	parts := strings.SplitN(m.prompts[len(m.prompts)-1], "instructions to follow:\n", 2)
	if len(parts) != 2 {
		t.Fatal(m.prompts)
	}
	var source Object
	if e := json.Unmarshal([]byte(parts[1]), &source); e != nil {
		t.Fatal(e)
	}
	return source
}
func selectionFixture(t *testing.T) (*App, *httptest.Server, *store.Workspace, *selectionModel, string) {
	t.Helper()
	a, s := testApp(t)
	ws := fixture(t, a)
	ws.Update("paper", func(p Object) error {
		p["blocks"] = []any{Object{"id": "p1", "en": "We attend to the relevant tokens.", "zh": "已有译文"}}
		return nil
	})
	m := &selectionModel{answer: "关注；注意到"}
	provider := httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(provider.Close)
	if _, e := a.Config.Save(Object{"engine": "openai", "openai": Object{"base_url": provider.URL, "model": "test-model", "api_key": "", "api": "chat"}}); e != nil {
		t.Fatal(e)
	}
	return a, s, ws, m, "/api/p/" + ws.ID() + "/translate-selection"
}
func workspaceSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(root, p)
			out[rel] = string(b)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}

// Python SelectionTranslateTest.test_translation_preserves_selection_context_and_does_not_write.
func TestSelectionContextAndReadOnly(t *testing.T) {
	a, s, ws, m, path := selectionFixture(t)
	before := workspaceSnapshot(t, ws.Root)
	status, out := call(t, s, a, "POST", path, Object{"text": "attend to", "anchor": "p1"}, true)
	if status != 200 || out["translation"] != "关注；注意到" {
		t.Fatal(status, out)
	}
	if got := m.source(t); !reflect.DeepEqual(got, Object{"selected_text": "attend to", "paragraph": "We attend to the relevant tokens."}) {
		t.Fatal(got)
	}
	if after := workspaceSnapshot(t, ws.Root); !reflect.DeepEqual(before, after) {
		t.Fatal("selection mutated workspace")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.models[0] != "test-model" {
		t.Fatal(m.models)
	}
}

// Python test_cached_answer_includes_phonetics_without_another_model_call.
func TestSelectionCachesPhonetics(t *testing.T) {
	a, s, _, m, path := selectionFixture(t)
	m.answer = "音标（美式）：/əˈtɛnd tə/\n关注；注意到"
	body := Object{"text": "attend to", "anchor": "p1"}
	status, one := call(t, s, a, "POST", path, body, true)
	_, two := call(t, s, a, "POST", path, body, true)
	if status != 200 || !strings.Contains(text(one["translation"]), "/əˈtɛnd tə/") || !reflect.DeepEqual(one, two) || m.count() != 1 {
		t.Fatal(one, two, m.count())
	}
}

// Python test_context_and_model_configuration_changes_invalidate_cache.
func TestSelectionInvalidatesContextAndModel(t *testing.T) {
	a, s, ws, m, path := selectionFixture(t)
	body := Object{"text": "attend to", "anchor": "p1"}
	call(t, s, a, "POST", path, body, true)
	ws.Update("paper", func(p Object) error {
		object(array(p["blocks"])[0])["en"] = "Please attend to the patient."
		return nil
	})
	call(t, s, a, "POST", path, body, true)
	a.Config.Save(Object{"openai": Object{"model": "another-model"}})
	call(t, s, a, "POST", path, body, true)
	if m.count() != 3 {
		t.Fatal(m.count())
	}
	if m.source(t)["paragraph"] != "Please attend to the patient." {
		t.Fatal("stale context")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.models[2] != "another-model" {
		t.Fatal(m.models)
	}
}

// Python test_lru_evicts_least_recently_used_instead_of_most_recently_read.
func TestSelectionLRUEviction(t *testing.T) {
	a, s, _, m, path := selectionFixture(t)
	request := func(n int) {
		status, out := call(t, s, a, "POST", path, Object{"text": fmt.Sprintf("word %d", n)}, true)
		if status != 200 {
			t.Fatal(status, out)
		}
	}
	for i := 0; i < 128; i++ {
		request(i)
	}
	request(0)
	request(128)
	if m.count() != 129 {
		t.Fatal(m.count())
	}
	request(0)
	if m.count() != 129 {
		t.Fatal("recent entry evicted", m.count())
	}
	request(1)
	if m.count() != 130 {
		t.Fatal("least recent entry retained", m.count())
	}
}

// Python test_invalid_selection_and_cross_site_requests_never_call_model.
func TestSelectionValidationAndCrossSiteNoModel(t *testing.T) {
	a, s, _, m, path := selectionFixture(t)
	for _, text := range []any{nil, []any{}, " ", strings.Repeat("a", 12001)} {
		status, out := call(t, s, a, "POST", path, Object{"text": text}, true)
		if status != 400 {
			t.Fatal(status, out)
		}
	}
	req, _ := http.NewRequest("POST", s.URL+path, strings.NewReader(`{"text":"word"}`))
	req.Header.Set("X-Token", "wrong")
	req.Header.Set("Content-Type", "application/json")
	response, e := s.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 403 || m.count() != 0 {
		t.Fatal(response.StatusCode, m.count())
	}
}

// Python test_error_and_empty_answer_can_be_retried.
func TestSelectionProviderFailureAndEmptyRetried(t *testing.T) {
	a, s, _, m, path := selectionFixture(t)
	body := Object{"text": "word"}
	for _, fail := range []bool{true, false} {
		m.mu.Lock()
		m.fail = fail
		m.answer = ""
		m.mu.Unlock()
		status, out := call(t, s, a, "POST", path, body, true)
		if status != 500 {
			t.Fatal(status, out)
		}
	}
	m.mu.Lock()
	m.answer = "词"
	m.mu.Unlock()
	status, out := call(t, s, a, "POST", path, body, true)
	if status != 200 || out["translation"] != "词" || m.count() != 3 {
		t.Fatal(status, out, m.count())
	}
}

// Python test_long_multiline_selection_is_not_silently_truncated.
func TestSelectionLongMultilineNotTruncated(t *testing.T) {
	a, s, _, m, path := selectionFixture(t)
	selected := strings.Repeat("A full sentence.\n", 300)
	status, out := call(t, s, a, "POST", path, Object{"text": selected, "anchor": "missing"}, true)
	if status != 200 {
		t.Fatal(status, out)
	}
	if source := m.source(t); source["selected_text"] != strings.TrimSpace(selected) || source["paragraph"] != "" {
		t.Fatal(source)
	}
}
func TestSelectionOriginalCaptionsAndListContext(t *testing.T) {
	a, s, ws, m, path := selectionFixture(t)
	for _, tc := range []struct {
		block Object
		want  string
	}{{Object{"id": "p1", "caption_en": "Original caption", "zh": "Wrong context"}, "Original caption"}, {Object{"id": "p1", "items": []any{Object{"en": "One", "zh": "一"}, Object{"en": "Two"}}}, "One\nTwo"}, {Object{"id": "p1", "zh": "Do not use translation"}, ""}} {
		ws.Update("paper", func(p Object) error { p["blocks"] = []any{tc.block}; return nil })
		status, out := call(t, s, a, "POST", path, Object{"text": "word", "anchor": "p1"}, true)
		if status != 200 {
			t.Fatal(status, out)
		}
		if m.source(t)["paragraph"] != tc.want {
			t.Fatal(m.source(t), tc.want)
		}
	}
}
