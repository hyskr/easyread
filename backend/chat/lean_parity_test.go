package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tr "github.com/hyskr/easyread/backend/translate"
)

func leanFixture(t *testing.T, engine string) (M, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake local POSIX command fixture")
	}
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/" {
			t.Errorf("unexpected preflight: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(health.Close)
	if engine == "claude" {
		t.Setenv("ANTHROPIC_BASE_URL", health.URL)
	}
	if engine == "codex" {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		if e := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model_provider = \"fixture\"\n[model_providers.fixture]\nbase_url = \""+health.URL+"\"\n"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	root := t.TempDir()
	log := filepath.Join(root, "args")
	state := filepath.Join(root, "state")
	command := filepath.Join(root, "model")
	response := `{"type":"result","subtype":"success","result":"答","usage":{}}`
	if engine == "codex" {
		response = `{"type":"item.completed","item":{"type":"agent_message","text":"{\"zh\":\"新译文\"}"}}`
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf 'CALL\\n' >> '%s'\nprintf '%%s\\n' \"$@\" >> '%s'\nif [ -f paper.json ]; then printf 'paper\\n' >> '%s'; else printf 'isolated\\n' >> '%s'; fi\ncat >/dev/null\nprintf '%%s\\n' '%s'\n", log, log, state, state, response)
	if e := os.WriteFile(command, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	cfg := M{"engine": engine, engine: M{"command": command, "model": "opus", "reasoning_effort": "high"}, "chat": M{"default": "test", "models": []any{M{"id": "test", "engine": engine, "model": "opus", "reasoning_effort": "high"}}}}
	return cfg, log, state
}
func argValue(args []string, key string) string {
	for i, a := range args {
		if a == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// Python ChatClaudeLeanTest.test_read_search_web_tools_and_user_choices_kept.
func TestClaudeChatReadOnlyToolsAndChoices(t *testing.T) {
	cfg, log, state := leanFixture(t, "claude")
	ws := workspace(t)
	w := httptest.NewRecorder()
	before, _ := json.Marshal(cfg)
	if e := Handle(w, httptest.NewRequest("POST", "/", nil), ws, cfg, M{"text": "q", "chat_options": M{"reasoning_effort": "high"}}); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(w.Body.String(), `"done":true`) {
		t.Fatal(w.Body.String())
	}
	raw, _ := os.ReadFile(log)
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := map[string]bool{"Read": true, "Glob": true, "Grep": true, "WebSearch": true, "WebFetch": true}
	got := map[string]bool{}
	for _, name := range strings.Split(argValue(args, "--tools"), ",") {
		got[name] = true
	}
	if !reflect.DeepEqual(got, want) || argValue(args, "--allowedTools") != argValue(args, "--tools") || !strings.Contains(argValue(args, "--disallowedTools"), "Bash") || !strings.Contains(string(raw), "--strict-mcp-config") || argValue(args, "--model") != "opus" || argValue(args, "--effort") != "high" {
		t.Fatal(string(raw))
	}
	after, _ := json.Marshal(cfg)
	if string(before) != string(after) {
		t.Fatal("caller config mutated")
	}
	b, _ := os.ReadFile(state)
	if string(b) != "paper\n" {
		t.Fatal("chat workspace lacks paper context", string(b))
	}
}

// Python ChatClaudeLeanTest.test_cancel_event_untouched_after_answer.
func TestClaudeChatLeavesCallerContextActive(t *testing.T) {
	cfg, _, _ := leanFixture(t, "claude")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := httptest.NewRecorder()
	if e := Handle(w, httptest.NewRequest("POST", "/", nil).WithContext(ctx), workspace(t), cfg, M{"text": "q"}); e != nil {
		t.Fatal(e)
	}
	if ctx.Err() != nil || !strings.Contains(w.Body.String(), `"done":true`) {
		t.Fatal(ctx.Err(), w.Body.String())
	}
	if e := Handle(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil).WithContext(ctx), workspace(t), cfg, M{"text": "retry"}); e != nil {
		t.Fatal("same caller context cannot be reused", e)
	}
}

// Python OtherPathsLeanTest.test_codex_chat_answer_and_retranslate_use_lean_cfg.
func TestCodexChatAnswerRetranslateLeanIsolation(t *testing.T) {
	cfg, log, state := leanFixture(t, "codex")
	configPath := filepath.Join(os.Getenv("CODEX_HOME"), "config.toml")
	original, _ := os.ReadFile(configPath)
	os.WriteFile(configPath, append([]byte("notify = ['not-run']\n"), append(original, []byte("[mcp_servers.test]\ncommand = 'not-run'\n")...)...), 0600)
	ws := workspace(t)
	ws.Write("reader", M{"notes": M{"n1": M{"anchor": "p1", "body": "?"}}})
	before, _ := json.Marshal(cfg)
	w := httptest.NewRecorder()
	if e := Handle(w, httptest.NewRequest("POST", "/", nil), ws, cfg, M{"text": "q"}); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(w.Body.String(), `"done":true`) {
		t.Fatal(w.Body.String())
	}
	if e := tr.Answer(context.Background(), ws, cfg, "n1"); e != nil {
		t.Fatal(e)
	}
	if e := tr.Retranslate(context.Background(), ws, cfg, "p1", ""); e != nil {
		t.Fatal(e)
	}
	after, _ := json.Marshal(cfg)
	if string(before) != string(after) {
		t.Fatal("caller config mutated")
	}
	b, _ := os.ReadFile(state)
	if string(b) != "paper\nisolated\nisolated\n" {
		t.Fatal("chat permission leaked into translation", string(b))
	}
	raw, _ := os.ReadFile(log)
	calls := strings.Split(strings.TrimSpace(string(raw)), "CALL\n")
	if len(calls) != 4 {
		t.Fatal(string(raw))
	}
	for _, c := range calls[1:] {
		args := strings.Split(strings.TrimSpace(c), "\n")
		if argValue(args, "--model") != "opus" || argValue(args, "--sandbox") != "read-only" || !(strings.Contains(c, "mcp_servers.test.enabled=false") || strings.Contains(c, `mcp_servers."test".enabled=false`)) || !strings.Contains(c, "features.multi_agent=false") || !strings.Contains(c, "notify=[]") {
			t.Fatal("missing lean restrictions", c)
		}
	}
}
