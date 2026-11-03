package server

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// presence handles only control frames. Frames are bounded before allocation.
func (a *App) presence(w http.ResponseWriter, r *http.Request) error {
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	decoded, e := base64.StdEncoding.DecodeString(key)
	if r.Method != http.MethodGet || !headerToken(r.Header.Get("Connection"), "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || e != nil || len(decoded) != 16 || r.Header.Get("Sec-WebSocket-Version") != "13" {
		return fmt.Errorf("expected WebSocket upgrade")
	}
	a.mu.Lock()
	if a.status == "moving" {
		a.mu.Unlock()
		return fmt.Errorf("library is moving")
	}
	a.pages++
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.pages > 0 {
			a.pages--
		}
		a.mu.Unlock()
	}()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return fmt.Errorf("WebSocket not available")
	}
	conn, rw, e := hijacker.Hijack()
	if e != nil {
		return e
	}
	defer conn.Close()
	connectionDone := make(chan struct{})
	defer close(connectionDone)
	if a.background != nil {
		go func() {
			select {
			case <-a.background.Done():
				_ = conn.Close()
			case <-connectionDone:
			}
		}()
	}
	digest := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(digest[:]))
	if rw.Flush() != nil {
		return nil
	}
	a.mu.Lock()
	a.presenceState.enter()
	a.mu.Unlock()
	sendClose := func(code uint16) {
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		_ = rw.WriteByte(0x88)
		_ = rw.WriteByte(2)
		_ = rw.WriteByte(byte(code >> 8))
		_ = rw.WriteByte(byte(code))
		_ = rw.Flush()
	}
	for {
		h := make([]byte, 2)
		if _, e = io.ReadFull(rw, h); e != nil {
			return nil
		}
		op := h[0] & 15
		if h[0]&0x70 != 0 || h[1]&128 == 0 || (op >= 8 && (h[0]&128 == 0 || h[1]&127 > 125)) || (op != 0 && op != 1 && op != 2 && op != 8 && op != 9 && op != 10) {
			sendClose(1002)
			return nil
		}
		n := uint64(h[1] & 127)
		if n == 126 {
			b := make([]byte, 2)
			if _, e = io.ReadFull(rw, b); e != nil {
				return nil
			}
			n = uint64(binary.BigEndian.Uint16(b))
		} else if n == 127 {
			b := make([]byte, 8)
			if _, e = io.ReadFull(rw, b); e != nil {
				return nil
			}
			n = binary.BigEndian.Uint64(b)
		}
		if n > 65536 {
			sendClose(1009)
			return nil
		}
		mask := make([]byte, 4)
		if _, e = io.ReadFull(rw, mask); e != nil {
			return nil
		}
		payload := make([]byte, int(n))
		if _, e = io.ReadFull(rw, payload); e != nil {
			return nil
		}
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
		if op == 8 {
			if len(payload) == 1 {
				sendClose(1002)
				return nil
			}
			if len(payload) >= 2 {
				code := binary.BigEndian.Uint16(payload[:2])
				if code < 1000 || code >= 5000 || code == 1004 || code == 1005 || code == 1006 || code == 1015 {
					sendClose(1002)
					return nil
				}
				if !utf8.Valid(payload[2:]) {
					sendClose(1007)
					return nil
				}
			}
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			rw.Write([]byte{0x88, byte(len(payload))})
			rw.Write(payload)
			rw.Flush()
			return nil
		}
		if op == 9 && len(payload) <= 125 {
			rw.Write([]byte{0x8a, byte(len(payload))})
			rw.Write(payload)
			if rw.Flush() != nil {
				return nil
			}
		}
	}
}

func headerToken(value, want string) bool {
	for _, token := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(token), want) {
			return true
		}
	}
	return false
}

// presenceState is protected by App.mu. Record entry immediately, even when a
// page opens and closes between watcher ticks. Idle starts again after any page
// or background task; browser refreshes therefore get the full grace period.
type presenceState struct {
	seen, initialized bool
	idleSince         time.Time
}

func (s *presenceState) enter() { s.seen = true; s.idleSince = time.Time{} }
func (s *presenceState) initialize(now time.Time) {
	if !s.initialized {
		s.initialized = true
		if !s.seen {
			s.idleSince = now
		}
	}
}
func (s *presenceState) shouldStop(now time.Time, pages int, busy bool, grace, firstWait time.Duration) bool {
	s.initialize(now)
	if pages > 0 || busy {
		s.idleSince = time.Time{}
		return false
	}
	if s.idleSince.IsZero() {
		s.idleSince = now
	}
	wait := firstWait
	if s.seen {
		wait = grace
	}
	return now.Sub(s.idleSince) >= wait
}

// WatchPresence implements browser launcher lifetime without affecting desktop serve.
func (a *App) WatchPresence(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	a.watchPresence(ctx, ticker.C, time.Now(), 15*time.Second, 120*time.Second, a.Busy)
}
func (a *App) watchPresence(ctx context.Context, ticks <-chan time.Time, started time.Time, grace, firstWait time.Duration, busy func() bool) {
	a.mu.Lock()
	a.presenceState.initialize(started)
	a.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case now, ok := <-ticks:
			if !ok {
				return
			}
			a.mu.Lock()
			keep := a.keep
			a.mu.Unlock()
			if keep {
				return
			}
			working := busy()
			a.mu.Lock()
			stop := !a.keep && a.presenceState.shouldStop(now, a.pages, working, grace, firstWait)
			keep = a.keep
			a.mu.Unlock()
			if keep {
				return
			}
			if stop && a.Shutdown != nil {
				a.Shutdown()
				return
			}
		}
	}
}
