package engines

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func imageFixture(t *testing.T) (string, []byte) {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 80, 40))
	im.Set(3, 4, color.RGBA{20, 40, 60, 255})
	var b bytes.Buffer
	if e := png.Encode(&b, im); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(t.TempDir(), "source.png")
	if e := os.WriteFile(p, b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	return p, b.Bytes()
}
func imageResult(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Object{"data": []any{Object{"b64_json": base64.StdEncoding.EncodeToString(b)}}})
}
func assertImage(t *testing.T, b []byte) {
	t.Helper()
	im, format, e := image.Decode(bytes.NewReader(b))
	if e != nil || format != "png" || im.Bounds().Dx() != 80 || im.Bounds().Dy() != 40 {
		t.Fatalf("image format=%s err=%v", format, e)
	}
}
func TestImageEditMultipartActualImage(t *testing.T) {
	source, pngBytes := imageFixture(t)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/images/edits" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("wrong URL/auth", r.URL.Path)
		}
		if e := r.ParseMultipartForm(1 << 20); e != nil {
			t.Error(e)
			return
		}
		defer r.MultipartForm.RemoveAll()
		for k, v := range map[string]string{"model": "editor", "prompt": "Translate to Japanese", "n": "1"} {
			if r.FormValue(k) != v {
				t.Error(k, r.FormValue(k))
			}
		}
		f, h, e := r.FormFile("image")
		if e != nil {
			t.Error(e)
			return
		}
		defer f.Close()
		if h.Header.Get("Content-Type") != "image/png" {
			t.Error("incorrect multipart image MIME", h.Header)
		}
		b, _ := io.ReadAll(f)
		im, _, e := image.Decode(bytes.NewReader(b))
		if e != nil || im.Bounds().Dx() != 80 || im.Bounds().Dy() != 40 {
			t.Error("actual source missing", e)
		}
		if e == nil {
			rr, gg, bb, _ := im.At(3, 4).RGBA()
			if rr != 20*257 || gg != 40*257 || bb != 60*257 {
				t.Error("source pixels changed")
			}
		}
		imageResult(w, pngBytes)
	}))
	defer s.Close()
	out, e := ImageEdit(context.Background(), Object{"base_url": s.URL + "/v1/", "model": "editor", "api_key": "secret"}, source, "Translate to Japanese", "")
	if e != nil {
		t.Fatal(e)
	}
	assertImage(t, out)
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}
func TestImageSignedDownloadNoCredentials(t *testing.T) {
	source, pngBytes := imageFixture(t)
	var downloadCalls atomic.Int32
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloadCalls.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credential forwarded to signed URL")
		}
		if r.URL.Query().Get("sig") != "signed" {
			t.Error("signed URL changed")
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes)
	}))
	defer cdn.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Object{"data": []any{Object{"url": cdn.URL + "/image?sig=signed"}}})
	}))
	defer s.Close()
	out, e := ImageEdit(context.Background(), Object{"base_url": s.URL, "model": "editor", "api_key": "secret"}, source, "p", "")
	if e != nil {
		t.Fatal(e)
	}
	assertImage(t, out)
	if downloadCalls.Load() != 1 {
		t.Fatal(downloadCalls.Load())
	}
}
func TestImageEditRejectsInvalidOutputs(t *testing.T) {
	source, pngBytes := imageFixture(t)
	cases := []struct {
		name  string
		value any
	}{{"empty", Object{"data": []any{}}}, {"bad-base64", Object{"data": []any{Object{"b64_json": "garbage"}}}}, {"bad-bytes", Object{"data": []any{Object{"b64_json": base64.StdEncoding.EncodeToString([]byte("not an image"))}}}}, {"file-url", Object{"data": []any{Object{"url": "file:///etc/passwd"}}}}, {"embedded-credentials", Object{"data": []any{Object{"url": "https://user:password@example.test/image"}}}}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); json.NewEncoder(w).Encode(tc.value) }))
			defer s.Close()
			b, e := ImageEdit(context.Background(), Object{"base_url": s.URL, "model": "editor"}, source, "p", "")
			if e == nil || len(b) != 0 || calls.Load() != 1 {
				t.Fatal(len(b), e, calls.Load())
			}
		})
	}
	for _, tc := range []struct {
		mime string
		b    []byte
	}{{"text/html", pngBytes}, {"image/png", []byte("<html>not an image</html>")}} {
		t.Run(tc.mime, func(t *testing.T) {
			cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Content-Type", tc.mime); w.Write(tc.b) }))
			defer cdn.Close()
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(Object{"data": []any{Object{"url": cdn.URL}}})
			}))
			defer s.Close()
			if b, e := ImageEdit(context.Background(), Object{"base_url": s.URL, "model": "editor"}, source, "p", ""); e == nil || len(b) > 0 {
				t.Fatal("invalid download accepted", e)
			}
		})
	}
}
func TestImageHTTPFailureNoRetryOrLeak(t *testing.T) {
	source, _ := imageFixture(t)
	for _, status := range []int{400, 401, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "secret", status) }))
			defer s.Close()
			_, e := ImageEdit(context.Background(), Object{"base_url": s.URL, "model": "editor", "api_key": "secret"}, source, "p", "")
			if e == nil || strings.Contains(e.Error(), "secret") || calls.Load() != 1 {
				t.Fatal(e, calls.Load())
			}
		})
	}
}
func TestImageCancellationDiscardsLateResponse(t *testing.T) {
	source, pngBytes := imageFixture(t)
	started := make(chan struct{})
	finish := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-finish
		imageResult(w, pngBytes)
	}))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		b, e := ImageEdit(ctx, Object{"base_url": s.URL, "model": "editor"}, source, "p", "")
		if len(b) != 0 {
			done <- errors.New("cancelled image was returned")
			return
		}
		done <- e
	}()
	<-started
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		close(finish)
		t.Fatal("cancellation waited for provider")
	}
	close(finish)
}
func TestImagePreCancelledAndInvalidSourceNoRequest(t *testing.T) {
	source, _ := imageFixture(t)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := ImageEdit(ctx, Object{"base_url": s.URL, "model": "editor"}, source, "p", ""); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	os.WriteFile(source, []byte("fake image"), 0600)
	if _, e := ImageEdit(context.Background(), Object{"base_url": s.URL, "model": "editor"}, source, "p", ""); e == nil {
		t.Fatal("invalid source accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("unexpected paid call")
	}
}
func fakeImageCLI(t *testing.T, script string) (string, string) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fake Codex fixture; Windows requires native test executable")
	}
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-codex")
	log := filepath.Join(dir, "run.log")
	t.Setenv("IMAGE_TEST_LOG", log)
	body := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" >> \"$IMAGE_TEST_LOG\"\n" + script
	if e := os.WriteFile(p, []byte(body), 0700); e != nil {
		t.Fatal(e)
	}
	return p, log
}
func assertWorkCleaned(t *testing.T, log string) {
	t.Helper()
	b, e := os.ReadFile(log)
	if e != nil {
		t.Fatal(e)
	}
	path := strings.Split(strings.TrimSpace(string(b)), "\n")[0]
	if path == "" {
		t.Fatal("missing workspace")
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("temporary workspace leaked", path, e)
	}
}
func TestCodexImageSubscriptionAuthAndCleanup(t *testing.T) {
	source, _ := imageFixture(t)
	t.Setenv("OPENAI_API_KEY", "paid-secret")
	t.Setenv("CODEX_API_KEY", "other-secret")
	t.Setenv("OPENAI_BASE_URL", "https://paid.example")
	t.Setenv("HTTPS_PROXY", "http://proxy.example")
	p, log := fakeImageCLI(t, `printf '%s\n' "$@" > "$IMAGE_TEST_LOG.args"
env > "$IMAGE_TEST_LOG.env"
cat > "$IMAGE_TEST_LOG.prompt"
cp source.png result.png
`)
	out, e := ImageEdit(context.Background(), Object{"engine": "codex", "codex": Object{"command": p, "model": "chosen-model"}}, source, "Translate labels", "")
	if e != nil {
		t.Fatal(e)
	}
	assertImage(t, out)
	b, _ := os.ReadFile(log + ".args")
	for _, s := range []string{"--ignore-user-config", `forced_login_method="chatgpt"`, "features.image_generation=true", "chosen-model", "--sandbox\nworkspace-write"} {
		if !strings.Contains(string(b), s) {
			t.Error("missing safe argument", s)
		}
	}
	env, _ := os.ReadFile(log + ".env")
	for _, s := range []string{"OPENAI_API_KEY=", "CODEX_API_KEY=", "OPENAI_BASE_URL="} {
		if strings.Contains(string(env), s) {
			t.Error("paid credential leaked", s)
		}
	}
	if !strings.Contains(string(env), "HTTPS_PROXY=http://proxy.example") {
		t.Error("proxy discarded")
	}
	prompt, _ := os.ReadFile(log + ".prompt")
	if !strings.Contains(string(prompt), "built-in image generation") || !strings.Contains(string(prompt), "Do not use API keys") {
		t.Error("missing prompt restriction")
	}
	assertWorkCleaned(t, log)
}
func TestCodexImageNoToolResultNoRetry(t *testing.T) {
	source, _ := imageFixture(t)
	p, log := fakeImageCLI(t, "cat >/dev/null\nprintf '%s\\n' '{\"type\":\"turn.failed\"}'\n")
	b, e := ImageEdit(context.Background(), Object{"engine": "codex", "codex": Object{"command": p}}, source, "p", "")
	if e == nil || len(b) > 0 {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(log)
	if strings.Count(string(raw), "\n") != 1 {
		t.Fatal("CLI was retried")
	}
	assertWorkCleaned(t, log)
}
func TestCodexFreshGeneratedArtifactImported(t *testing.T) {
	source, _ := imageFixture(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	artifact := filepath.Join(home, "generated_images", "turn", "result.png")
	os.MkdirAll(filepath.Dir(artifact), 0700)
	t.Setenv("IMAGE_TEST_ARTIFACT", artifact)
	p, log := fakeImageCLI(t, `cat >/dev/null
cp source.png "$IMAGE_TEST_ARTIFACT"
printf '{"type":"item.completed","item":{"type":"agent_message","text":"![image](%s)"}}\n' "$IMAGE_TEST_ARTIFACT"
`)
	out, e := ImageEdit(context.Background(), Object{"engine": "codex", "codex": Object{"command": p}}, source, "p", "")
	if e != nil {
		t.Fatal(e)
	}
	assertImage(t, out)
	assertWorkCleaned(t, log)
	if _, e = os.Stat(artifact); e != nil {
		t.Fatal("original artifact removed")
	}
}
func TestCodexArtifactScopeFreshnessAndSymlinks(t *testing.T) {
	source, pngBytes := imageFixture(t)
	for _, kind := range []string{"old", "outside", "file-symlink", "directory-escape", "home-alias", "relative", "wrong-extension"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			images := filepath.Join(home, "generated_images")
			os.MkdirAll(images, 0700)
			artifact := filepath.Join(images, "result.png")
			os.WriteFile(artifact, pngBytes, 0600)
			accepted := false
			switch kind {
			case "old":
				old := time.Now().Add(-time.Hour)
				os.Chtimes(artifact, old, old)
			case "outside":
				artifact = filepath.Join(root, "outside.png")
				os.WriteFile(artifact, pngBytes, 0600)
			case "file-symlink":
				link := filepath.Join(images, "link.png")
				if e := os.Symlink(artifact, link); e != nil {
					t.Skip(e)
				}
				artifact = link
			case "directory-escape":
				outside := filepath.Join(root, "outside")
				os.Mkdir(outside, 0700)
				os.WriteFile(filepath.Join(outside, "result.png"), pngBytes, 0600)
				if e := os.Symlink(outside, filepath.Join(images, "escape")); e != nil {
					t.Skip(e)
				}
				artifact = filepath.Join(images, "escape", "result.png")
			case "home-alias":
				alias := filepath.Join(root, "alias")
				if e := os.Symlink(home, alias); e != nil {
					t.Skip(e)
				}
				home = alias
				artifact = filepath.Join(alias, "generated_images", "result.png")
				accepted = true
			case "relative":
				artifact = "source.png"
			case "wrong-extension":
				artifact = filepath.Join(images, "result.txt")
				os.WriteFile(artifact, pngBytes, 0600)
			}
			t.Setenv("CODEX_HOME", home)
			t.Setenv("IMAGE_TEST_ARTIFACT", artifact)
			p, _ := fakeImageCLI(t, `cat >/dev/null
printf '{"type":"item.completed","item":{"type":"agent_message","text":"![image](%s)"}}\n' "$IMAGE_TEST_ARTIFACT"
`)
			out, e := ImageEdit(context.Background(), Object{"engine": "codex", "codex": Object{"command": p}}, source, "p", "")
			if accepted {
				if e != nil {
					t.Fatal(e)
				}
				assertImage(t, out)
			} else if e == nil || len(out) > 0 {
				t.Fatal("unsafe artifact accepted", kind)
			}
		})
	}
}
func TestCodexImageInvalidOrSymlinkResultRejected(t *testing.T) {
	source, _ := imageFixture(t)
	for _, script := range []string{"cat >/dev/null\nprintf 'not an image' > result.png\n", "cat >/dev/null\nln -s source.png result.png\n"} {
		t.Run(script, func(t *testing.T) {
			p, log := fakeImageCLI(t, script)
			b, e := ImageEdit(context.Background(), Object{"engine": "codex", "codex": Object{"command": p}}, source, "p", "")
			if e == nil || len(b) > 0 {
				t.Fatal("bad result accepted")
			}
			assertWorkCleaned(t, log)
		})
	}
}
func TestCodexImageCancellationTimeoutAndPreCancelled(t *testing.T) {
	source, _ := imageFixture(t)
	for _, mode := range []string{"cancel", "timeout", "pre-cancelled"} {
		t.Run(mode, func(t *testing.T) {
			p, log := fakeImageCLI(t, "cat >/dev/null\nsleep 30\n")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := Object{"command": p}
			if mode == "timeout" {
				opts["timeout"] = 0.5
			} else if mode == "pre-cancelled" {
				cancel()
			} else {
				go func() {
					for i := 0; i < 100; i++ {
						if data, e := os.ReadFile(log); e == nil && strings.Contains(string(data), "\n") {
							cancel()
							return
						}
						time.Sleep(5 * time.Millisecond)
					}
					cancel()
				}()
			}
			start := time.Now()
			b, e := ImageEdit(ctx, Object{"engine": "codex", "codex": opts}, source, "p", "")
			if e == nil || len(b) > 0 || time.Since(start) > 5*time.Second {
				t.Fatal("cancellation failed", e)
			}
			if mode == "pre-cancelled" {
				if _, e = os.Stat(log); !os.IsNotExist(e) {
					t.Fatal("CLI started while cancelled")
				}
			} else {
				// Under heavy CI load a deadline may expire before exec starts.
				// No log then means no child workspace was observed, not a leak.
				if _, statErr := os.Stat(log); statErr == nil {
					assertWorkCleaned(t, log)
				} else if mode != "timeout" {
					t.Fatal(statErr)
				}
			}
		})
	}
}
