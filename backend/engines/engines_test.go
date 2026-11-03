package engines

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestChatAndResponses(t *testing.T) {
	for _, api := range []string{"chat", "responses"} {
		t.Run(api, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer secret" {
					t.Error("missing credential")
				}
				var b Object
				json.NewDecoder(r.Body).Decode(&b)
				if str(b["model"]) != "test" {
					t.Error("missing model")
				}
				w.Header().Set("Content-Type", "application/json")
				if api == "responses" {
					if r.URL.Path != "/responses" {
						t.Error(r.URL.Path)
					}
					fmt.Fprint(w, `{"status":"completed","output":[{"content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":2,"output_tokens":3}}`)
				} else {
					fmt.Fprint(w, `{"choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`)
				}
			}))
			defer srv.Close()
			c := Object{"engine": "openai", "openai": Object{"base_url": srv.URL, "api": api, "model": "test", "api_key": "secret"}}
			s, u, e := Run(context.Background(), c, "prompt", "", nil)
			if e != nil || s != "hello" || num(u["input"]) != 2 {
				t.Fatalf("%q %#v %v", s, u, e)
			}
		})
	}
}
func TestStreaming(t *testing.T) {
	for _, api := range []string{"chat", "responses"} {
		t.Run(api, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if api == "chat" {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
				} else {
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":4,\"output_tokens\":1}}}\n\n")
				}
			}))
			defer srv.Close()
			var s strings.Builder
			u, e := Stream(context.Background(), Object{"engine": "openai", "openai": Object{"base_url": srv.URL, "model": "test", "api": api}}, "p", "", nil, func(p string) error { s.WriteString(p); return nil })
			if e != nil || s.String() != "hello" || num(u["input"]) != 4 {
				t.Fatalf("%s %#v %v", s.String(), u, e)
			}
		})
	}
}
func TestKeyIsolation(t *testing.T) {
	o := Object{"base_url": "https://one.example/v1", "api_key": "secret", "keys": Object{"": "legacy"}}
	if ResolveKey(o, "", "https://other.example/v1") != "" {
		t.Fatal("credential crossed hosts")
	}
	if ResolveKey(o, "", "https://one.example/v1/") != "secret" {
		t.Fatal("current key not resolved")
	}
	if !NeedsKey(Object{"base_url": "https://localhost.attacker.invalid/v1"}) {
		t.Fatal("host substring accepted")
	}
}
func TestStreamIncomplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer srv.Close()
	_, e := Stream(context.Background(), Object{"engine": "openai", "openai": Object{"base_url": srv.URL, "model": "test"}}, "p", "", nil, func(string) error { return nil })
	if e == nil {
		t.Fatal("truncated stream accepted")
	}
}
func TestErrorRedaction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, "invalid secret-value")
	}))
	defer srv.Close()
	_, _, e := Run(context.Background(), Object{"engine": "openai", "openai": Object{"base_url": srv.URL, "model": "test", "api_key": "secret-value"}}, "p", "", nil)
	if e == nil || strings.Contains(e.Error(), "secret-value") {
		t.Fatal(e)
	}
}
func TestCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fake executable fixture; Windows subprocess helper requires native test")
	}
	p := filepath.Join(t.TempDir(), "fake")
	if e := os.WriteFile(p, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"type\":\"result\",\"result\":\"hello\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	c := Object{"engine": "claude", "claude": Object{"command": p}}
	s, u, e := Run(context.Background(), c, "p", "", nil)
	if e != nil || s != "hello" || num(u["output"]) != 3 {
		t.Fatalf("%s %#v %v", s, u, e)
	}
	obj(c["claude"])["extra_args"] = []any{"--dangerously-skip-permissions"}
	if _, _, e = Run(context.Background(), c, "p", "", nil); e == nil {
		t.Fatal("unsafe args accepted")
	}
}
func TestCLICancelProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fake executable fixture; Windows subprocess helper requires native test")
	}
	p := filepath.Join(t.TempDir(), "fake")
	os.WriteFile(p, []byte("#!/bin/sh\nsleep 30\n"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, e := Run(ctx, Object{"engine": "claude", "claude": Object{"command": p}}, "p", "", nil)
	if e == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("cancellation failed: %v (%s)", e, time.Since(start))
	}
}
func TestAPICancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, e := Run(ctx, Object{"engine": "openai", "openai": Object{"base_url": srv.URL, "model": "test"}}, "p", "", nil)
	if e == nil {
		t.Fatal("cancelled API request succeeded")
	}
}
func TestResponsesIncomplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output_text":"partial"}`)
	}))
	defer srv.Close()
	_, _, e := Run(context.Background(), Object{"engine": "openai", "openai": Object{"base_url": srv.URL, "model": "test", "api": "responses"}}, "p", "", nil)
	if e == nil {
		t.Fatal("incomplete response accepted")
	}
}
func TestEngineConfigDoesNotShareKey(t *testing.T) {
	cfg := Object{"openai": Object{"base_url": "https://a.example/v1", "api_key": "a-secret"}, "chat": Object{"models": []any{Object{"id": "b", "engine": "openai", "base_url": "https://b.example/v1", "model": "m"}}}}
	c, _, e := EngineConfig(cfg, "b")
	if e != nil {
		t.Fatal(e)
	}
	if str(obj(c["openai"])["api_key"]) != "" {
		t.Fatal("custom endpoint inherited another host credential")
	}
	if str(obj(cfg["openai"])["api_key"]) != "a-secret" {
		t.Fatal("source configuration mutated")
	}
}
func TestEveryCLIAdapter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fake executable fixture; Windows subprocess helper requires native test")
	}
	fixtures := map[string]string{"claude": `{"type":"result","result":"answer"}`, "codex": `{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}`, "codebuddy": `{"type":"result","result":"answer"}`, "opencode": `{"type":"text","part":{"text":"answer"}}`, "zcode": `{"response":"answer"}`, "qoder": `{"type":"result","result":"answer"}`, "grok": `{"text":"answer"}`, "agy": `{"event":"result","result":{"status":"SUCCESS","response":"answer"}}`, "cursor": `{"type":"result","result":"answer"}`}
	for engine, fixture := range fixtures {
		t.Run(engine, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "fake")
			os.WriteFile(p, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '"+fixture+"'\n"), 0700)
			s, _, e := Run(context.Background(), Object{"engine": engine, engine: Object{"command": p}}, "prompt", "", nil)
			if e != nil || s != "answer" {
				t.Fatalf("%q %v", s, e)
			}
		})
	}
}
func TestSafeExtraArguments(t *testing.T) {
	for _, tc := range []struct {
		engine string
		args   []string
		ok     bool
	}{{"claude", []string{"--max-turns", "4", "--append-system-prompt=Be concise"}, true}, {"claude", []string{"--tools", "Bash"}, false}, {"codex", []string{"-c", `model_reasoning_effort="high"`}, true}, {"codex", []string{"-c", `sandbox_mode="danger-full-access"`}, false}, {"claude", []string{"--settings", "malicious.json"}, false}, {"cursor", []string{"--mode", "agent"}, false}, {"codebuddy", []string{"--max-budget-usd", "2"}, true}} {
		_, e := safeExtraArgs(tc.engine, tc.args)
		if (e == nil) != tc.ok {
			t.Fatalf("%s %v: %v", tc.engine, tc.args, e)
		}
	}
}
func TestCLIErrorRedaction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fake executable fixture; Windows subprocess helper requires native test")
	}
	p := filepath.Join(t.TempDir(), "fake")
	os.WriteFile(p, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"error\",\"result\":\"bad cli-secret api-secret\"}'\n"), 0700)
	_, _, e := Run(context.Background(), Object{"engine": "claude", "claude": Object{"command": p, "api_key": "cli-secret"}, "openai": Object{"api_key": "api-secret"}}, "p", "", nil)
	if e == nil || strings.Contains(e.Error(), "cli-secret") || strings.Contains(e.Error(), "api-secret") {
		t.Fatal(e)
	}
}
