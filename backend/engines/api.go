// Package engines implements cancellable local and OpenAI-compatible model adapters.
package engines

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Object = map[string]any

func obj(v any) Object {
	if o, ok := v.(map[string]any); ok && o != nil {
		return o
	}
	return Object{}
}
func str(v any) string { s, _ := v.(string); return s }
func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}
func truth(v any) bool { b, _ := v.(bool); return b }
func arr(v any) []any {
	a, _ := v.([]any)
	if a != nil {
		return a
	}
	if a, ok := v.([]Object); ok {
		r := []any{}
		for _, v := range a {
			r = append(r, v)
		}
		return r
	}
	return nil
}
func clone(o Object) Object {
	b, _ := json.Marshal(o)
	out := Object{}
	json.Unmarshal(b, &out)
	return out
}
func timeout(c Object, fallback int) time.Duration {
	n := num(c["timeout"])
	if n <= 0 {
		n = float64(fallback)
	}
	return time.Duration(n * float64(time.Second))
}
func KeyID(preset, base string) string {
	if preset != "" {
		return preset
	}
	return "url:" + strings.TrimRight(strings.TrimSpace(base), "/")
}
func ResolveKey(o Object, preset, base string) string {
	id := KeyID(preset, base)
	keys := obj(o["keys"])
	if k := str(keys[id]); k != "" {
		return k
	}
	if id == KeyID(str(o["preset"]), str(o["base_url"])) {
		if k := str(o["api_key"]); k != "" && !strings.Contains(k, "••") {
			return k
		}
		if preset == "" {
			return str(keys[""])
		}
	}
	return ""
}
func NeedsKey(o Object) bool {
	u, e := url.Parse(str(o["base_url"]))
	if e != nil {
		return true
	}
	h := u.Hostname()
	return h != "localhost" && h != "127.0.0.1" && h != "::1"
}
func redact(s string, o Object) string {
	keys := []string{str(o["api_key"])}
	for _, v := range obj(o["keys"]) {
		keys = append(keys, str(v))
	}
	for _, k := range keys {
		if k != "" {
			s = strings.ReplaceAll(s, k, "[REDACTED]")
		}
	}
	return s
}
func Usage(u Object) Object {
	input := num(u["input_tokens"])
	if _, ok := u["prompt_tokens"]; ok {
		input = num(u["prompt_tokens"])
	}
	output := num(u["output_tokens"])
	if _, ok := u["completion_tokens"]; ok {
		output = num(u["completion_tokens"])
	}
	cached := num(obj(u["input_tokens_details"])["cached_tokens"]) + num(obj(u["prompt_tokens_details"])["cached_tokens"])
	return Object{"input": input, "output": output, "cached": cached}
}
func mergeUsage(dst, src Object) {
	for k, v := range src {
		dst[k] = v
	}
}
func imageURL(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	m := mime.TypeByExtension(filepath.Ext(path))
	if m == "" {
		m = "image/png"
	}
	return "data:" + m + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}
func apiBody(o Object, prompt string, images []string, stream bool) (Object, error) {
	response := str(o["api"]) == "responses"
	parts := []any{}
	typ := "text"
	if response {
		typ = "input_text"
	}
	parts = append(parts, Object{"type": typ, "text": prompt})
	if truth(o["vision"]) {
		for _, p := range images {
			u, e := imageURL(p)
			if e != nil {
				return nil, e
			}
			if response {
				parts = append(parts, Object{"type": "input_image", "image_url": u})
			} else {
				parts = append(parts, Object{"type": "image_url", "image_url": Object{"url": u}})
			}
		}
	}
	b := Object{"model": str(o["model"])}
	if b["model"] == "" {
		return nil, errors.New("API model is required")
	}
	if response {
		b["input"] = []any{Object{"role": "user", "content": parts}}
		b["store"] = false
	} else {
		var content any = prompt
		if len(parts) > 1 {
			content = parts
		}
		b["messages"] = []any{Object{"role": "user", "content": content}}
	}
	if effort := str(o["reasoning_effort"]); effort != "" {
		if response {
			b["reasoning"] = Object{"effort": effort}
		} else {
			b["reasoning_effort"] = effort
		}
	} else if t, ok := o["temperature"]; ok {
		b["temperature"] = t
	}
	if tier := str(o["service_tier"]); tier != "" {
		b["service_tier"] = tier
	}
	if stream {
		b["stream"] = true
		if !response {
			b["stream_options"] = Object{"include_usage": true}
		}
	}
	return b, nil
}
func request(ctx context.Context, o Object, path string, body Object) (*http.Response, error) {
	base := strings.TrimRight(strings.TrimSpace(str(o["base_url"])), "/")
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, errors.New("valid HTTP(S) API base URL required (without embedded credentials)")
	}
	for attempt := 0; attempt < 3; attempt++ {
		var r io.Reader
		method := "GET"
		if body != nil {
			b, _ := json.Marshal(body)
			r = bytes.NewReader(b)
			method = "POST"
		}
		req, e := http.NewRequestWithContext(ctx, method, base+path, r)
		if e != nil {
			return nil, e
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "EasyRead-Go/1.0")
		key := ResolveKey(o, str(o["preset"]), base)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		if truth(body["stream"]) {
			req.Header.Set("Accept", "text/event-stream")
		}
		transport, e := outboundTransport()
		if e != nil {
			return nil, e
		}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errors.New("API redirects are disabled to protect credentials")
		}}
		res, e := client.Do(req)
		if e != nil {
			return nil, fmt.Errorf("API request failed: %s", redact(e.Error(), o))
		}
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			return res, nil
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 8192))
		res.Body.Close()
		detail := redact(string(raw), o)
		retry := false
		if res.StatusCode == 400 {
			for _, k := range []string{"temperature", "stream_options"} {
				if _, ok := body[k]; ok && strings.Contains(detail, k) {
					delete(body, k)
					retry = true
					break
				}
			}
		}
		if !retry && (res.StatusCode == 400 || res.StatusCode == 422) && truth(body["stream"]) {
			lower := strings.ToLower(detail)
			if strings.Contains(lower, "stream") && (strings.Contains(lower, "not supported") || strings.Contains(lower, "unsupported") || strings.Contains(lower, "not available")) {
				delete(body, "stream")
				delete(body, "stream_options")
				retry = true
			}
		}
		if retry {
			continue
		}
		return nil, fmt.Errorf("API HTTP %d: %s", res.StatusCode, detail)
	}
	return nil, errors.New("API rejected request options")
}
func responseText(o Object) string {
	if t := str(o["output_text"]); t != "" {
		return t
	}
	var b strings.Builder
	for _, v := range arr(o["output"]) {
		for _, c := range arr(obj(v)["content"]) {
			x := obj(c)
			if str(x["type"]) == "output_text" {
				b.WriteString(str(x["text"]))
			}
		}
	}
	return b.String()
}
func chatText(o Object) string {
	a := arr(o["choices"])
	if len(a) == 0 {
		return ""
	}
	m := obj(obj(a[0])["message"])
	if s, ok := m["content"].(string); ok {
		return s
	}
	var b strings.Builder
	for _, p := range arr(m["content"]) {
		b.WriteString(str(obj(p)["text"]))
	}
	return b.String()
}
func checkResult(o Object) error {
	if e, ok := o["error"]; ok && e != nil {
		return fmt.Errorf("model error: %v", e)
	}
	switch str(o["status"]) {
	case "failed", "cancelled", "incomplete":
		return fmt.Errorf("model response %s: %v", o["status"], o["incomplete_details"])
	}
	for _, v := range arr(o["choices"]) {
		switch str(obj(v)["finish_reason"]) {
		case "length":
			return errors.New("model output truncated by token limit")
		case "content_filter":
			return errors.New("model output blocked by content filter")
		}
	}
	return nil
}
func apiRun(ctx context.Context, o Object, prompt string, images []string, emit func(string) error) (string, Object, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout(o, 600))
	defer cancel()
	body, e := apiBody(o, prompt, images, emit != nil)
	if e != nil {
		return "", nil, e
	}
	path := "/chat/completions"
	responses := str(o["api"]) == "responses"
	if responses {
		path = "/responses"
	}
	res, e := request(ctx, o, path, body)
	if e != nil {
		return "", nil, e
	}
	defer res.Body.Close()
	used := Object{}
	if emit == nil || !strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		var data Object
		if e = json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(&data); e != nil {
			return "", used, e
		}
		if e = checkResult(data); e != nil {
			return "", used, errors.New(redact(e.Error(), o))
		}
		mergeUsage(used, Usage(obj(data["usage"])))
		t := chatText(data)
		if responses {
			t = responseText(data)
		}
		t = stripThink(t)
		if t == "" {
			return "", used, errors.New("model returned no text")
		}
		if emit != nil {
			e = emit(t)
		}
		return t, used, e
	}
	var full strings.Builder
	filter := thinkFilter{}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 4096), 8<<20)
	lines := []string{}
	done := false
	process := func() error {
		if len(lines) == 0 {
			return nil
		}
		raw := strings.Join(lines, "\n")
		lines = nil
		if raw == "[DONE]" {
			return nil
		}
		var ev Object
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return fmt.Errorf("invalid SSE JSON: %w", err)
		}
		if ev == nil {
			return errors.New("SSE event must be a JSON object")
		}
		if err := checkResult(ev); err != nil {
			return err
		}
		if u := obj(ev["usage"]); len(u) > 0 {
			mergeUsage(used, Usage(u))
		}
		piece := ""
		if responses {
			switch str(ev["type"]) {
			case "response.output_text.delta":
				piece = str(ev["delta"])
			case "response.completed":
				r := obj(ev["response"])
				if err := checkResult(r); err != nil {
					return err
				}
				mergeUsage(used, Usage(obj(r["usage"])))
				done = true
			case "response.failed", "response.incomplete", "error":
				return fmt.Errorf("response stream failed: %v", ev)
			}
		} else {
			for _, v := range arr(ev["choices"]) {
				ch := obj(v)
				piece += str(obj(ch["delta"])["content"])
				if str(ch["finish_reason"]) != "" {
					done = true
				}
			}
		}
		piece = filter.Push(piece)
		if piece != "" {
			full.WriteString(piece)
			return emit(piece)
		}
		return nil
	}
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if line == "" {
			if e = process(); e != nil {
				return full.String(), used, errors.New(redact(e.Error(), o))
			}
		} else if strings.HasPrefix(line, "data:") {
			// Some compatible relays omit blank separators between complete JSON events.
			if len(lines) > 0 {
				pending := strings.Join(lines, "\n")
				if pending == "[DONE]" || json.Valid([]byte(pending)) {
					if e = process(); e != nil {
						return full.String(), used, e
					}
				}
			}
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if e = sc.Err(); e != nil {
		return full.String(), used, e
	}
	if e = process(); e != nil {
		return full.String(), used, e
	}
	if !done {
		return full.String(), used, errors.New("model stream interrupted before completion")
	}
	if tail := filter.Finish(); tail != "" {
		full.WriteString(tail)
		if e = emit(tail); e != nil {
			return full.String(), used, e
		}
	}
	if full.Len() == 0 {
		return "", used, errors.New("model returned no text")
	}
	return full.String(), used, nil
}
func Run(ctx context.Context, cfg Object, prompt, cwd string, images []string) (answer string, usage Object, err error) {
	defer func() { err = safeError(err, cfg) }()
	if str(cfg["engine"]) == "openai" {
		return apiRun(ctx, obj(cfg["openai"]), prompt, images, nil)
	}
	return cliRun(ctx, cfg, prompt, cwd, images, nil)
}
func Stream(ctx context.Context, cfg Object, prompt, cwd string, images []string, emit func(string) error) (usage Object, err error) {
	defer func() { err = safeError(err, cfg) }()
	if emit == nil {
		return nil, errors.New("stream callback required")
	}
	var u Object
	var e error
	if str(cfg["engine"]) == "openai" {
		_, u, e = apiRun(ctx, obj(cfg["openai"]), prompt, images, emit)
	} else {
		_, u, e = cliRun(ctx, cfg, prompt, cwd, images, emit)
	}
	return u, e
}
func Models(ctx context.Context, options Object) ([]string, error) {
	if o := obj(options["openai"]); len(o) > 0 {
		options = o
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(options, 30))
	defer cancel()
	r, e := request(ctx, options, "/models", nil)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	var data Object
	if e = json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&data); e != nil {
		return nil, e
	}
	out := []string{}
	for _, v := range arr(data["data"]) {
		if id := str(obj(v)["id"]); id != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Defense in depth for CLI diagnostics and provider SSE error payloads.
func safeError(err error, cfg Object) error {
	if err == nil {
		return nil
	}
	original := err.Error()
	message := original
	var scrub func(Object)
	scrub = func(o Object) {
		message = redact(message, o)
		for key, value := range o {
			if key == "token" || key == "access_token" || key == "password" || key == "secret" {
				if secret := str(value); secret != "" {
					message = strings.ReplaceAll(message, secret, "[REDACTED]")
				}
			}
			if child, ok := value.(map[string]any); ok {
				scrub(child)
			}
		}
	}
	scrub(cfg)
	if message == original {
		return err
	}
	return errors.New(message)
}
