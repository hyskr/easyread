package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestVersionOrdering(t *testing.T) {
	for _, x := range []struct {
		a, b string
		want bool
	}{{"v1.2.10", "1.2.9", true}, {"v1.2.0", "1.2.0", false}, {"1.1", "1.2", false}, {"wails-v1.5.2", "1.4.11", true}, {"junk", "1.0", false}} {
		if newer(x.a, x.b) != x.want {
			t.Fatal(x)
		}
	}
}
func TestUpdateThrottleDisableForceFailure(t *testing.T) {
	a, _ := testApp(t)
	var calls atomic.Int32
	bad := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if bad {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"tag_name":"v9.9.9","html_url":"https://example.invalid/release","body":"notes","published_at":"2026-11-03"}`)
	}))
	defer s.Close()
	a.UpdateURL = s.URL
	v, e := a.update(context.Background(), false)
	if e != nil || v["enabled"] != false || calls.Load() != 0 {
		t.Fatal(v, e)
	}
	v, e = a.update(context.Background(), true)
	if e != nil || v["newer"] != true || calls.Load() != 1 {
		t.Fatal(v, e)
	}
	_, e = a.Config.Save(Object{"check_updates": true})
	if e != nil {
		t.Fatal(e)
	}
	v, e = a.update(context.Background(), false)
	if e != nil || calls.Load() != 1 {
		t.Fatal(v, e)
	}
	bad = true
	v, e = a.update(context.Background(), true)
	if e != nil || v["failed"] != true || v["latest"] != "v9.9.9" {
		t.Fatal(v, e)
	}
	v, e = a.update(context.Background(), false)
	if e != nil || v["failed"] != true || calls.Load() != 2 {
		t.Fatal(v, e)
	}
}
func TestOpenLinksIDHashAndSafeAnchor(t *testing.T) {
	a, s := testApp(t)
	ws := fixture(t, a)
	p, _ := ws.Load("paper")
	sha := text(object(p["meta"])["source_sha256"])
	client := *s.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, x := range []struct{ query, want string }{{"id=" + ws.ID(), "/read/" + ws.ID()}, {"sha256=" + strings.ToUpper(sha) + "&block=p1", "/read/" + ws.ID() + "#p1"}, {"id=" + ws.ID() + "&block=%3Cscript%3E", "/read/" + ws.ID()}, {"id=missing", "/"}, {"sha256=bad", "/"}, {"id=..%2Fsecret", "/"}} {
		r, e := client.Get(s.URL + "/open?" + x.query)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode != 302 || r.Header.Get("Location") != x.want {
			t.Fatalf("%s => %d %s", x.query, r.StatusCode, r.Header.Get("Location"))
		}
	}
}
