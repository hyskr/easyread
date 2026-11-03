package engines

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func netConfig(raw string) Object {
	return Object{"engine": "openai", "openai": Object{"base_url": raw}}
}
func TestNetworkAnyHTTPResponseAndCache(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/" || r.Header.Get("Authorization") != "" {
			t.Error("probe leaked path or authorization")
		}
		w.WriteHeader(501)
	}))
	defer s.Close()
	cfg := netConfig(s.URL + "/v1")
	obj(cfg["openai"])["api_key"] = "secret"
	for i := 0; i < 2; i++ {
		if m := NetworkProblem(context.Background(), cfg, time.Second); m != "" {
			t.Fatal(m)
		}
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}
func TestNetworkUnreachableLocal(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := l.Addr().String()
	l.Close()
	m := NetworkProblem(context.Background(), netConfig("http://"+address+"/v1"), time.Second)
	if !strings.Contains(m, "先把它打开") || !Offline(m) {
		t.Fatal(m)
	}
}
func TestNetworkOverseasPreset(t *testing.T) {
	cfg := netConfig("https://x.example/v1")
	obj(cfg["openai"])["preset"] = "gemini"
	if !truth(NetworkTarget(cfg)["vpn"]) || truth(NetworkTarget(netConfig("https://api.deepseek.com/v1"))["vpn"]) {
		t.Fatal("wrong VPN classification")
	}
}
func TestNetworkExplainOnlyNetworkErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := Object{"engine": "codex"}
	for input, want := range map[string]string{"stream disconnected before completion": "梯子", "unsupported_country_region_territory": "节点", "模型输出里没有 JSON": "模型输出里没有 JSON"} {
		got := ExplainNetwork(cfg, input)
		if !strings.Contains(got, want) {
			t.Fatal(got)
		}
		if input == want && got != input {
			t.Fatal(got)
		}
	}
}
func TestNetworkCertificateNoVPNOrRetry(t *testing.T) {
	var handshakes atomic.Int32
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted TLS must not reach handler") }))
	defer s.Close()
	s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			handshakes.Add(1)
		}
	}
	s.StartTLS()
	m := NetworkProblem(context.Background(), netConfig(s.URL), time.Second)
	if !strings.Contains(m, "证书校验失败") || strings.Contains(m, "梯子") || !Offline(m) {
		t.Fatal(m)
	}
	if handshakes.Load() != 1 {
		t.Fatal(handshakes.Load())
	}
}
func TestNetworkRegionCancellationAndCredentials(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("userinfo leaked")
		}
		w.WriteHeader(403)
		w.Write([]byte("unsupported_country_region_territory"))
	}))
	defer s.Close()
	cfg := netConfig(strings.Replace(s.URL, "http://", "http://user:secret@", 1))
	if m := NetworkProblem(context.Background(), cfg, time.Second); !strings.Contains(m, "节点") {
		t.Fatal(m)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if m := NetworkProblem(ctx, cfg, time.Second); m != context.Canceled.Error() {
		t.Fatal(m)
	}
}

func TestNetworkCodexHomeProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if e := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model_provider = \"local\"\n[model_providers.local]\nbase_url = \"http://127.0.0.1:4321/v1\"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	target := NetworkTarget(Object{"engine": "codex"})
	if str(target["url"]) != "http://127.0.0.1:4321/v1" || truth(target["vpn"]) {
		t.Fatal(target)
	}
}
