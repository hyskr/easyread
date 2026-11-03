package engines

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var networkOK = struct {
	sync.Mutex
	origins map[string]time.Time
}{origins: map[string]time.Time{}}
var networkPattern = regexp.MustCompile(`(?i)ECONNREFUSED|ETIMEDOUT|ENOTFOUND|ECONNRESET|EAI_AGAIN|fetch failed|connection error|unable to connect|error sending request|stream disconnected|timed out|getaddrinfo|连不上|can't connect|connection refused|no such host`)
var regionPattern = regexp.MustCompile(`(?i)unsupported_country|not available in your (country|region)|request not allowed`)
var certPattern = regexp.MustCompile(`(?i)CERTIFICATE_VERIFY_FAILED|certificate verify failed|self.signed certificate|unknown issuer|certificate signed by unknown authority|certificate verification failed|certificate is valid for|certificate has expired|failed to verify certificate`)

func officialHost(raw, domain string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Hostname() == domain || strings.HasSuffix(u.Hostname(), "."+domain))
}
func NetworkTarget(cfg Object) Object {
	engine := str(cfg["engine"])
	raw, name := "", ""
	vpn := false
	home, _ := os.UserHomeDir()
	switch engine {
	case "claude":
		raw = os.Getenv("ANTHROPIC_BASE_URL")
		if raw == "" {
			b, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
			var c Object
			_ = json.Unmarshal(b, &c)
			raw = str(obj(c["env"])["ANTHROPIC_BASE_URL"])
		}
		if raw == "" {
			raw = "https://api.anthropic.com"
		}
		vpn = officialHost(raw, "anthropic.com")
		name = "Claude"
		if !vpn {
			name = "Claude Code 配置的中转地址"
		}
	case "codex":
		raw = "https://chatgpt.com"
		b, _ := os.ReadFile(filepath.Join(codexHome(), "config.toml"))
		p := regexp.MustCompile(`(?m)^\s*model_provider\s*=\s*"([^"]+)"`).FindSubmatch(b)
		if len(p) > 1 && string(p[1]) != "openai" {
			section := regexp.MustCompile(`(?m)^\[model_providers\.` + regexp.QuoteMeta(string(p[1])) + `\]([^\[]*)`).FindSubmatch(b)
			if len(section) > 1 {
				v := regexp.MustCompile(`(?m)^\s*base_url\s*=\s*"([^"]+)"`).FindSubmatch(section[1])
				if len(v) > 1 {
					raw = string(v[1])
				}
			}
		}
		vpn = officialHost(raw, "chatgpt.com") || officialHost(raw, "openai.com")
		name = "OpenAI（Codex）"
		if !vpn {
			name = "Codex 配置的中转地址"
		}
	case "openai":
		o := obj(cfg["openai"])
		raw = strings.TrimRight(strings.TrimSpace(str(o["base_url"])), "/")
		if raw == "" {
			return nil
		}
		name = "接口"
		presets := Presets()
		var chosen Object
		for _, p := range presets {
			if str(p["id"]) == str(o["preset"]) {
				chosen = p
				break
			}
		}
		if chosen == nil {
			for _, p := range presets {
				if strings.TrimRight(str(p["base_url"]), "/") == raw {
					chosen = p
					break
				}
			}
		}
		if chosen != nil {
			name = str(chosen["name"])
			vpn = str(chosen["region"]) == "intl"
		}
	default:
		return nil
	}
	return Object{"name": name, "url": raw, "vpn": vpn}
}
func networkHint(t Object, kind string) string {
	u, _ := url.Parse(str(t["url"]))
	host := ""
	if u != nil {
		host = u.Hostname()
	}
	name := str(t["name"])
	if kind == "certificate" {
		return fmt.Sprintf("%s（%s）证书校验失败：请检查系统信任的根证书；使用代理时，也检查代理证书是否已获系统信任。", name, host)
	}
	if kind == "region" {
		return name + " 拒绝了当前地区的访问：梯子节点在它不支持的地区，换成美国、日本、新加坡等节点再重试。"
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return fmt.Sprintf("连不上 %s（%s）：先把它打开，再点重试。", name, u.Host)
	}
	if truth(t["vpn"]) {
		return fmt.Sprintf("连不上 %s（%s）。在国内要先打开梯子（VPN）再点重试；开着还不行，把梯子切到 TUN 或全局模式。", name, host)
	}
	return fmt.Sprintf("连不上 %s（%s）：检查网络和地址有没有填对；开着梯子的话，试试让国内网站直连。", name, host)
}
func Offline(message string) bool {
	for _, s := range []string{"梯子", "先把它打开", "VPN", "start it first", "证书校验失败", "certificate verification failed"} {
		if strings.Contains(message, s) {
			return true
		}
	}
	return false
}
func ExplainNetwork(cfg Object, message string) string {
	t := NetworkTarget(cfg)
	if t == nil || Offline(message) {
		return message
	}
	kind := ""
	if regionPattern.MatchString(message) {
		kind = "region"
	} else if certPattern.MatchString(message) {
		kind = "certificate"
	} else if networkPattern.MatchString(message) {
		kind = "network"
	}
	if kind == "" {
		return message
	}
	networkOK.Lock()
	networkOK.origins = map[string]time.Time{}
	networkOK.Unlock()
	return message + "\n" + networkHint(t, kind)
}

// NetworkProblem probes only the origin, without API keys or URL userinfo. A
// successful probe is cached for two minutes; certificate failures never retry.
func NetworkProblem(ctx context.Context, cfg Object, limit time.Duration) string {
	if ctx.Err() != nil {
		return ctx.Err().Error()
	}
	t := NetworkTarget(cfg)
	if t == nil {
		return ""
	}
	u, e := url.Parse(str(t["url"]))
	if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	origin := u.Scheme + "://" + u.Host + "/"
	networkOK.Lock()
	last := networkOK.origins[origin]
	networkOK.Unlock()
	if time.Since(last) < 2*time.Minute {
		return ""
	}
	tr, e := outboundTransport()
	if e != nil {
		return networkHint(t, "certificate")
	}
	defer tr.CloseIdleConnections()
	if limit <= 0 {
		limit = 6 * time.Second
	}
	client := &http.Client{Transport: tr, Timeout: limit, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for attempt := 0; attempt < 2; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err().Error()
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", origin, nil)
		res, e := client.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err().Error()
			}
			if certPattern.MatchString(e.Error()) {
				return networkHint(t, "certificate")
			}
			if attempt == 0 {
				continue
			}
			return networkHint(t, "network")
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 2000))
		res.Body.Close()
		if res.StatusCode >= 400 && regionPattern.Match(raw) {
			return networkHint(t, "region")
		}
		networkOK.Lock()
		networkOK.origins[origin] = time.Now()
		networkOK.Unlock()
		return ""
	}
	return ""
}
