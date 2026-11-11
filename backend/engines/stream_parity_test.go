package engines

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestThinkTagsEveryChunkBoundary(t *testing.T) {
	input := "前文<think>不应显示</think>回答<think>第二次思考</think>后文"
	for i := 0; i <= len(input); i++ {
		f := thinkFilter{}
		got := f.Push(input[:i]) + f.Push(input[i:]) + f.Finish()
		if got != "前文回答后文" {
			t.Fatal(i, got)
		}
	}
	f := thinkFilter{}
	var got strings.Builder
	for _, r := range input {
		got.WriteString(f.Push(string(r)))
	}
	got.WriteString(f.Finish())
	if got.String() != "前文回答后文" {
		t.Fatal(got.String())
	}
}
func TestUnclosedThinkHiddenPlainAnglesRemain(t *testing.T) {
	for _, tc := range []struct {
		chunks []string
		want   string
	}{{[]string{"答", "<think>私密", "内容"}, "答"}, {[]string{"x <", " y", "<thi"}, "x < y<thi"}} {
		f := thinkFilter{}
		var b strings.Builder
		for _, s := range tc.chunks {
			b.WriteString(f.Push(s))
		}
		b.WriteString(f.Finish())
		if b.String() != tc.want {
			t.Fatal(b.String())
		}
	}
}
func TestSSEFramingAndInvalidEvents(t *testing.T) {
	delta := `data: {"choices":[{"delta":{"content":"ok"}}]}`
	stop := `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`
	cases := []struct {
		name, payload string
		ok            bool
	}{{"multiline-comments", ": ping\r\nevent: message\r\ndata: {\"choices\": [\r\ndata: {\"delta\": {\"content\": \"ok\"},\"finish_reason\":\"stop\"}]}\r\n\r\ndata: [DONE]\r\n\r\n", true}, {"relay", delta + "\n" + stop + "\ndata: [DONE]\n", true}, {"no-final-blank", delta + "\n\n" + stop + "\n", true}, {"no-final-newline", delta + "\n\n" + stop, true}, {"done-without-finish", delta + "\n\ndata: [DONE]\n\n", false}, {"broken", "data: broken\n\ndata: [DONE]\n\n", false}, {"null", delta + "\n\ndata: null\n\ndata: [DONE]\n\n", false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, tc.payload)
			}))
			defer s.Close()
			var b strings.Builder
			_, e := Stream(context.Background(), Object{"engine": "openai", "openai": Object{"base_url": s.URL, "model": "test"}}, "p", "", nil, func(s string) error { b.WriteString(s); return nil })
			if (e == nil) != tc.ok {
				t.Fatal(e)
			}
			if tc.ok && b.String() != "ok" {
				t.Fatal(b.String())
			}
		})
	}
}
func TestSSETerminalFailuresNoRetry(t *testing.T) {
	for _, api := range []string{"chat", "responses"} {
		ends := []string{`{"choices":[{"finish_reason":"length"}]}`, `{"choices":[{"finish_reason":"content_filter"}]}`, `{"error":{"message":"secret"}}`, ""}
		first := `{"choices":[{"delta":{"content":"partial"}}]}`
		if api == "responses" {
			first = `{"type":"response.output_text.delta","delta":"partial"}`
			ends = []string{`{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`, `{"type":"response.failed","response":{"error":{"message":"secret"}}}`, `{"type":"error","message":"secret"}`, ""}
		}
		for i, end := range ends {
			t.Run(fmt.Sprintf("%s-%d", api, i), func(t *testing.T) {
				var calls atomic.Int32
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: %s\n\n", first)
					if end != "" {
						fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", end)
					}
				}))
				defer s.Close()
				_, e := Stream(context.Background(), Object{"engine": "openai", "openai": Object{"base_url": s.URL, "model": "test", "api": api, "api_key": "secret"}}, "p", "", nil, func(string) error { return nil })
				if e == nil || strings.Contains(e.Error(), "secret") || calls.Load() != 1 {
					t.Fatal(e, calls.Load())
				}
			})
		}
	}
}
func TestExplicitStreamRejectionFallsBackOnce(t *testing.T) {
	for _, api := range []string{"chat", "responses"} {
		t.Run(api, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				var body Object
				json.NewDecoder(r.Body).Decode(&body)
				if n == 1 {
					if body["stream"] != true {
						t.Error("not streaming")
					}
					http.Error(w, "stream is not supported", 400)
					return
				}
				if n != 2 || body["stream"] != nil || body["stream_options"] != nil {
					t.Error("unsafe repeated fallback", body)
				}
				if api == "chat" {
					fmt.Fprint(w, `{"choices":[{"message":{"content":"<think>hidden</think>complete"},"finish_reason":"stop"}]}`)
				} else {
					fmt.Fprint(w, `{"status":"completed","output_text":"<think>hidden</think>complete"}`)
				}
			}))
			defer s.Close()
			var b strings.Builder
			_, e := Stream(context.Background(), Object{"engine": "openai", "openai": Object{"base_url": s.URL, "model": "test", "api": api}}, "p", "", nil, func(s string) error { b.WriteString(s); return nil })
			if e != nil || calls.Load() != 2 || b.String() != "complete" {
				t.Fatal(e, calls.Load(), b.String())
			}
		})
	}
}
func TestAuthFailureNeverUsesStreamFallback(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "stream is not supported", 401)
	}))
	defer s.Close()
	_, e := Stream(context.Background(), Object{"engine": "openai", "openai": Object{"base_url": s.URL, "model": "test"}}, "p", "", nil, func(string) error { return nil })
	if e == nil || calls.Load() != 1 {
		t.Fatal(e, calls.Load())
	}
}
