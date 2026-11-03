package server

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Exact rule scenarios from tests/server/test_presence.py, with a deterministic clock.
func TestPresenceWaitsLongerBeforeFirstPage(t *testing.T) {
	base := time.Unix(100, 0)
	s := presenceState{}
	s.initialize(base)
	if s.shouldStop(base.Add(50*time.Second), 0, false, 10*time.Second, 100*time.Second) {
		t.Fatal("stopped before first-page wait")
	}
	if !s.shouldStop(base.Add(101*time.Second), 0, false, 10*time.Second, 100*time.Second) {
		t.Fatal("did not stop after first-page wait")
	}
}
func TestPresenceGraceAfterLastPageCloses(t *testing.T) {
	s := presenceState{}
	base := time.Unix(1000, 0)
	s.enter()
	s.enter()
	if s.shouldStop(base, 2, false, 10*time.Second, 100*time.Second) {
		t.Fatal("stopped with two pages")
	}
	if s.shouldStop(base.Add(time.Second), 1, false, 10*time.Second, 100*time.Second) {
		t.Fatal("stopped with one page")
	}
	if s.shouldStop(base.Add(2*time.Second), 0, false, 10*time.Second, 100*time.Second) {
		t.Fatal("no close grace")
	}
	s.enter()
	if s.shouldStop(base.Add(20*time.Second), 1, false, 10*time.Second, 100*time.Second) {
		t.Fatal("refresh stopped")
	}
	if s.shouldStop(base.Add(21*time.Second), 0, false, 10*time.Second, 100*time.Second) {
		t.Fatal("refresh grace missing")
	}
	if !s.shouldStop(base.Add(32*time.Second), 0, false, 10*time.Second, 100*time.Second) {
		t.Fatal("did not stop after refresh grace")
	}
}
func TestPresenceBusyBlocksExit(t *testing.T) {
	s := presenceState{}
	s.enter()
	for _, at := range []int64{1000, 5000} {
		if s.shouldStop(time.Unix(at, 0), 0, true, 10*time.Second, 100*time.Second) {
			t.Fatal("stopped with busy task")
		}
	}
	if s.shouldStop(time.Unix(5001, 0), 0, false, 10*time.Second, 100*time.Second) {
		t.Fatal("task completion skipped grace")
	}
	if !s.shouldStop(time.Unix(5012, 0), 0, false, 10*time.Second, 100*time.Second) {
		t.Fatal("did not stop after task grace")
	}
}

type presenceSocket struct {
	net.Conn
	reader  *bufio.Reader
	status  int
	headers http.Header
}

func connectPresence(t *testing.T, serverURL, origin string) *presenceSocket {
	t.Helper()
	host := strings.TrimPrefix(serverURL, "http://")
	conn, e := net.DialTimeout("tcp", host, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	request := fmt.Sprintf("GET /api/presence HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: keep-alive, Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n", host, key)
	if origin != "" {
		request += "Origin: " + origin + "\r\n"
	}
	if _, e = io.WriteString(conn, request+"\r\n"); e != nil {
		conn.Close()
		t.Fatal(e)
	}
	reader := bufio.NewReader(conn)
	resp, e := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if e != nil {
		conn.Close()
		t.Fatal(e)
	}
	if resp.StatusCode == 101 {
		digest := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		want := base64.StdEncoding.EncodeToString(digest[:])
		if resp.Header.Get("Sec-WebSocket-Accept") != want {
			conn.Close()
			t.Fatal("bad WebSocket accept")
		}
	}
	return &presenceSocket{conn, reader, resp.StatusCode, resp.Header}
}
func (s *presenceSocket) masked(op byte, payload []byte) error {
	frame := []byte{0x80 | op, 0x80 | byte(len(payload)), 1, 2, 3, 4}
	for i, v := range payload {
		frame = append(frame, v^byte(i%4+1))
	}
	_, e := s.Write(frame)
	return e
}
func waitPresence(t *testing.T, a *App, pages int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		got := a.pages
		a.mu.Unlock()
		if got == pages {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pages=%d, want %d", got, pages)
		}
		time.Sleep(time.Millisecond)
	}
}
func presenceFixture(t *testing.T) (*App, *httptest.Server) {
	t.Helper()
	a := &App{status: "idle", Token: "presence-test-token"}
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	return a, srv
}
func watchPresenceTest(t *testing.T, a *App) (chan time.Time, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ticks := make(chan time.Time)
	stopped := make(chan struct{})
	finished := make(chan struct{})
	a.Shutdown = func() { close(stopped) }
	go func() {
		defer close(finished)
		a.watchPresence(ctx, ticks, time.Unix(1000, 0), 10*time.Second, 100*time.Second, func() bool { return false })
	}()
	return ticks, stopped, finished
}
func TestPresenceServerExitsAfterLastPageCloses(t *testing.T) {
	a, srv := presenceFixture(t)
	ticks, stopped, finished := watchPresenceTest(t, a)
	one := connectPresence(t, srv.URL, srv.URL)
	defer one.Close()
	two := connectPresence(t, srv.URL, "")
	defer two.Close()
	if one.status != 101 || two.status != 101 {
		t.Fatal(one.status, two.status)
	}
	waitPresence(t, a, 2)
	if e := one.masked(8, nil); e != nil {
		t.Fatal(e)
	}
	closeReply := make([]byte, 2)
	if _, e := io.ReadFull(one.reader, closeReply); e != nil || closeReply[0] != 0x88 {
		t.Fatal(closeReply, e)
	}
	one.Close()
	waitPresence(t, a, 1)
	ticks <- time.Unix(1001, 0)
	ticks <- time.Unix(1100, 0)
	select {
	case <-stopped:
		t.Fatal("exited with one page")
	default:
	}
	two.Close()
	waitPresence(t, a, 0)
	ticks <- time.Unix(1101, 0)
	ticks <- time.Unix(1112, 0)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("did not exit after final disconnect")
	}
	<-finished
}
func TestPresenceServerRejectsOtherSite(t *testing.T) {
	a, srv := presenceFixture(t)
	socket := connectPresence(t, srv.URL, "http://evil.example")
	defer socket.Close()
	if socket.status != 403 {
		t.Fatal(socket.status)
	}
	waitPresence(t, a, 0)
}
func TestPresenceKeepTurnsOffAutoExit(t *testing.T) {
	a, srv := presenceFixture(t)
	ticks, stopped, finished := watchPresenceTest(t, a)
	req, e := http.NewRequest("POST", srv.URL+"/api/presence/keep", strings.NewReader("{}"))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("X-Token", a.Token)
	resp, e := srv.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	socket := connectPresence(t, srv.URL, "")
	if socket.status != 101 {
		t.Fatal(socket.status)
	}
	socket.Close()
	waitPresence(t, a, 0)
	ticks <- time.Unix(1500, 0)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop after keep")
	}
	select {
	case <-stopped:
		t.Fatal("keep still shut down server")
	default:
	}
}
func TestPresenceQuickConnectionUsesNormalGrace(t *testing.T) {
	a, srv := presenceFixture(t)
	ticks, stopped, _ := watchPresenceTest(t, a)
	socket := connectPresence(t, srv.URL, "")
	socket.Close()
	waitPresence(t, a, 0)
	a.mu.Lock()
	seen := a.presenceState.seen
	a.mu.Unlock()
	if !seen {
		t.Fatal("quick page missed before first watcher tick")
	}
	ticks <- time.Unix(1001, 0)
	ticks <- time.Unix(1011, 0)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("quick connection incorrectly used first-page grace")
	}
}
func TestPresencePingPongAndProtocolCleanup(t *testing.T) {
	a, srv := presenceFixture(t)
	socket := connectPresence(t, srv.URL, "")
	defer socket.Close()
	if e := socket.masked(9, []byte("hello")); e != nil {
		t.Fatal(e)
	}
	reply := make([]byte, 7)
	if _, e := io.ReadFull(socket.reader, reply); e != nil || reply[0] != 0x8a || string(reply[2:]) != "hello" {
		t.Fatal(reply, e)
	}
	if _, e := socket.Write([]byte{0x89, 0}); e != nil {
		t.Fatal(e)
	}
	reply = make([]byte, 4)
	if _, e := io.ReadFull(socket.reader, reply); e != nil || reply[0] != 0x88 || reply[2] != 3 || reply[3] != 234 {
		t.Fatal("unmasked frame not rejected", reply, e)
	}
	waitPresence(t, a, 0)
}
func TestPresenceRejectsInvalidHandshake(t *testing.T) {
	a, srv := presenceFixture(t)
	req, e := http.NewRequest("GET", srv.URL+"/api/presence", nil)
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", "not-a-key")
	req.Header.Set("Sec-WebSocket-Version", "13")
	resp, e := srv.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatal(resp.StatusCode)
	}
	waitPresence(t, a, 0)
}
func TestPresenceKeepRequiresToken(t *testing.T) {
	a, srv := presenceFixture(t)
	resp, e := srv.Client().Post(srv.URL+"/api/presence/keep", "application/json", strings.NewReader("{}"))
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal(resp.StatusCode)
	}
	a.mu.Lock()
	kept := a.keep
	a.mu.Unlock()
	if kept {
		t.Fatal("unauthenticated keep disabled auto-exit")
	}
}
func TestPresenceBackgroundCancellationClosesHijackedSocket(t *testing.T) {
	a := &App{status: "idle", Token: "test"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.background = ctx
	srv := httptest.NewServer(a)
	defer srv.Close()
	socket := connectPresence(t, srv.URL, "")
	defer socket.Close()
	waitPresence(t, a, 1)
	cancel()
	waitPresence(t, a, 0)
	if _, e := socket.reader.ReadByte(); e == nil {
		t.Fatal("shutdown left WebSocket open")
	}
}
func TestPresenceDefaultGraceDurations(t *testing.T) {
	base := time.Unix(100, 0)
	s := presenceState{}
	s.initialize(base)
	if s.shouldStop(base.Add(119*time.Second), 0, false, 15*time.Second, 120*time.Second) {
		t.Fatal("first wait too short")
	}
	if !s.shouldStop(base.Add(120*time.Second), 0, false, 15*time.Second, 120*time.Second) {
		t.Fatal("first wait too long")
	}
	s.enter()
	if s.shouldStop(base.Add(121*time.Second), 0, false, 15*time.Second, 120*time.Second) {
		t.Fatal("normal grace missing")
	}
	if s.shouldStop(base.Add(135*time.Second), 0, false, 15*time.Second, 120*time.Second) {
		t.Fatal("normal grace too short")
	}
	if !s.shouldStop(base.Add(136*time.Second), 0, false, 15*time.Second, 120*time.Second) {
		t.Fatal("normal grace too long")
	}
}
