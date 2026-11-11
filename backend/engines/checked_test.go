package engines

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunCheckedProbesBeforeOneGeneration(t *testing.T) {
	var probes, calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected credentials")
		}
		if r.Method == "GET" {
			probes.Add(1)
			if r.URL.Path != "/" {
				t.Error(r.URL.Path)
			}
			w.WriteHeader(401)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer s.Close()
	cfg := netConfig(s.URL + "/v1")
	obj(cfg["openai"])["model"] = "fake"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, _, e := RunChecked(ctx, cfg, "hello", "", nil)
	if e != nil || out != "ok" || probes.Load() != 1 || calls.Load() != 1 || ctx.Err() != nil {
		t.Fatalf("%q %v probes=%d calls=%d", out, e, probes.Load(), calls.Load())
	}
}
func TestRunCheckedExplainsFailureWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.WriteHeader(200)
			return
		}
		calls.Add(1)
		w.WriteHeader(503)
		w.Write([]byte("connection refused"))
	}))
	defer s.Close()
	cfg := netConfig(s.URL)
	obj(cfg["openai"])["model"] = "fake"
	_, _, e := RunChecked(context.Background(), cfg, "test", "", nil)
	if e == nil || !strings.Contains(e.Error(), "先把它打开") || calls.Load() != 1 {
		t.Fatalf("%v calls=%d", e, calls.Load())
	}
}
func TestRunCheckedCancellationIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, e := RunChecked(ctx, netConfig("http://127.0.0.1:1"), "test", "", nil)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	ctx, cancel = context.WithCancel(context.Background())
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() }))
	defer s.Close()
	_, _, e = RunChecked(ctx, netConfig(s.URL), "test", "", nil)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestStreamCheckedProbeAndNoReplayAfterPartialOutput(t *testing.T) {
	var probes, calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected credentials")
		}
		if r.Method == "GET" {
			probes.Add(1)
			w.WriteHeader(401)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: {\"error\":{\"message\":\"stream disconnected before completion\"}}\n\n"))
	}))
	defer s.Close()
	cfg := netConfig(s.URL)
	obj(cfg["openai"])["model"] = "fake"
	var got strings.Builder
	_, e := StreamChecked(context.Background(), cfg, "test", "", nil, func(piece string) error { got.WriteString(piece); return nil })
	if e == nil || !strings.Contains(e.Error(), "先把它打开") || got.String() != "partial" || calls.Load() != 1 || probes.Load() != 1 {
		t.Fatalf("text=%q err=%v calls=%d probes=%d", got.String(), e, calls.Load(), probes.Load())
	}
}
func TestStreamCheckedCallbackAndCancellationIdentity(t *testing.T) {
	for _, mode := range []string{"callback", "cancel", "success"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					w.WriteHeader(200)
					return
				}
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
			}))
			defer s.Close()
			cfg := netConfig(s.URL)
			obj(cfg["openai"])["model"] = "fake"
			sentinel := errors.New("consumer stream disconnected")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, e := StreamChecked(ctx, cfg, "test", "", nil, func(string) error {
				if mode == "callback" {
					return sentinel
				}
				if mode == "cancel" {
					cancel()
				}
				return nil
			})
			switch mode {
			case "callback":
				if e != sentinel {
					t.Fatalf("lost callback identity %v", e)
				}
			case "cancel":
				if !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
			case "success":
				if e != nil || ctx.Err() != nil {
					t.Fatalf("%v %v", e, ctx.Err())
				}
			}
			if calls.Load() != 1 {
				t.Fatal(calls.Load())
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := StreamChecked(ctx, netConfig("http://127.0.0.1:1"), "test", "", nil, func(string) error { return nil })
	if e != context.Canceled {
		t.Fatal(e)
	}
}
