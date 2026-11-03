package engines

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

//go:embed presets.json
var presetJSON []byte

func Presets() []Object { var p []Object; json.Unmarshal(presetJSON, &p); return p }
func Detect(cfg Object) []Object {
	out := make([]Object, len(CLIEngines))
	var wg sync.WaitGroup
	for i, engine := range CLIEngines {
		wg.Add(1)
		go func(i int, engine string) {
			defer wg.Done()
			p := cliPath(engine, obj(cfg[engine]))
			version := ""
			if p != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, p, "--version")
				cmd.WaitDelay = time.Second
				configureProcess(cmd)
				b, _ := cmd.Output()
				version = strings.TrimSpace(string(b))
				if len(version) > 200 {
					version = version[:200]
				}
			}
			out[i] = Object{"id": engine, "engine": engine, "name": Name(engine), "found": p != "", "version": version}
		}(i, engine)
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:11434/api/tags", nil)
	models := []string{}
	running := false
	if r, e := http.DefaultClient.Do(req); e == nil {
		defer r.Body.Close()
		var data Object
		if r.StatusCode == 200 && json.NewDecoder(r.Body).Decode(&data) == nil {
			running = true
			for _, v := range arr(data["models"]) {
				if n := str(obj(v)["name"]); n != "" {
					models = append(models, n)
				}
			}
		}
	}
	return append(out, Object{"id": "ollama", "engine": "ollama", "running": running, "found": running, "models": models})
}
func Ready(cfg Object) bool {
	e := str(cfg["engine"])
	if e == "openai" {
		o := obj(cfg[e])
		return str(o["base_url"]) != "" && str(o["model"]) != "" && (ResolveKey(o, str(o["preset"]), str(o["base_url"])) != "" || !NeedsKey(o))
	}
	if _, ok := names[e]; !ok {
		return false
	}
	return cliPath(e, obj(cfg[e])) != ""
}
func Test(ctx context.Context, cfg Object) Object {
	t := time.Now()
	answer, u, e := Run(ctx, cfg, "Reply with exactly OK.", "", nil)
	out := Object{"ok": e == nil, "engine": str(cfg["engine"]), "elapsed": time.Since(t).Seconds(), "usage": u}
	if e != nil {
		out["error"] = redact(e.Error(), obj(cfg["openai"]))
	} else {
		out["text"] = answer
		out["result"] = answer
	}
	return out
}
func defaultModels() []Object {
	return []Object{{"id": "opus", "name": "Claude Opus", "engine": "claude", "model": "opus"}, {"id": "sonnet", "name": "Claude Sonnet", "engine": "claude", "model": "sonnet"}, {"id": "gpt", "name": "GPT", "engine": "codex", "model": ""}}
}
func modelList(cfg Object) []Object {
	out := []Object{}
	for _, m := range arr(obj(cfg["chat"])["models"]) {
		if x := obj(m); len(x) > 0 {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		return defaultModels()
	}
	return out
}
func Label(m Object) string {
	if str(m["engine"]) == "claude" {
		if actual := str(seenModels()[str(m["model"])]); actual != "" {
			return Pretty(actual)
		}
	}
	if str(m["engine"]) == "codex" && (str(m["name"]) == "" || str(m["name"]) == "GPT" || str(m["name"]) == str(m["model"])) {
		slug := str(m["model"])
		if slug == "" {
			slug = codexConfigValue("model")
		}
		for _, v := range arr(obj(CLIModels(Object{})["codex"])["models"]) {
			if str(obj(v)["id"]) == slug {
				return str(obj(v)["name"])
			}
		}
		if slug != "" {
			return slug
		}
		return "GPT"
	}

	for _, k := range []string{"name", "model", "id"} {
		if v := str(m[k]); v != "" {
			return v
		}
	}
	return "模型"
}
func EngineConfig(cfg Object, modelID string) (Object, Object, error) {
	models := modelList(cfg)
	if modelID == "" {
		modelID = str(obj(cfg["chat"])["default"])
	}
	m := models[0]
	for _, v := range models {
		if str(v["id"]) == modelID {
			m = v
			break
		}
	}
	e := str(m["engine"])
	if _, ok := names[e]; !ok {
		return nil, nil, fmt.Errorf("unknown model engine: %s", e)
	}
	out := clone(cfg)
	out["engine"] = e
	c := clone(obj(out[e]))
	if e == "openai" {
		p := Object{}
		for _, v := range Presets() {
			if v["id"] == m["preset"] {
				p = v
				break
			}
		}
		base := str(m["base_url"])
		if base == "" {
			base = str(p["base_url"])
		}
		if base == "" {
			base = str(c["base_url"])
		}
		c["api_key"] = ResolveKey(obj(cfg[e]), str(m["preset"]), base)
		c["preset"] = str(m["preset"])
		c["base_url"] = base
		c["vision"] = false
		api := str(m["api"])
		if api == "" {
			api = str(p["api"])
		}
		if api == "" {
			api = "chat"
		}
		c["api"] = api
		c["model"] = str(m["model"])
		if c["model"] == "" {
			c["model"] = str(p["model"])
		}
	} else {
		c["model"] = str(m["model"])
	}
	for _, k := range []string{"reasoning_effort", "service_tier"} {
		c[k] = str(m[k])
	}
	out[e] = c
	return out, clone(m), nil
}
func ChatModels(cfg Object) Object {
	out := []Object{}
	translation := ""
	for _, m := range modelList(cfg) {
		item := clone(m)
		ec, _, err := EngineConfig(cfg, str(m["id"]))
		ready := err == nil && Ready(ec)
		item["label"] = Label(m)
		item["source"] = Name(str(m["engine"]))
		item["ready"] = ready
		item["hint"] = ""
		if !ready {
			item["hint"] = "请安装并登录 CLI，或配置 API 地址、模型与 Key"
		}
		item["detail"] = str(m["model"])
		out = append(out, item)
		if err == nil && cfg["engine"] == ec["engine"] {
			a, b := obj(cfg[str(cfg["engine"])]), obj(ec[str(ec["engine"])])
			equal := true
			for _, k := range []string{"model", "preset", "base_url", "reasoning_effort", "service_tier"} {
				if str(a[k]) != str(b[k]) {
					equal = false
				}
			}
			if equal && translation == "" {
				translation = str(m["id"])
			}
		}
	}
	d := str(obj(cfg["chat"])["default"])
	if d == "" && len(out) > 0 {
		d = str(out[0]["id"])
	}
	return Object{"models": out, "default": d, "translate": translation, "presets": Presets()}
}
func ApplyOptions(cfg Object, options Object) error {
	if effort, ok := options["reasoning_effort"]; ok {
		s, ok := effort.(string)
		if !ok {
			return fmt.Errorf("unsupported reasoning effort")
		}
		valid := map[string]bool{"": true, "none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true, "ultra": true}
		e := str(cfg["engine"])
		if e == "qoder" || e == "codebuddy" || e == "zcode" || e == "cursor" {
			valid = map[string]bool{"": true}
		}
		if e == "claude" {
			valid = map[string]bool{"": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}
			if strings.Contains(str(obj(cfg[e])["model"]), "haiku") {
				valid = map[string]bool{"": true}
			}
		}
		if e == "grok" {
			valid = map[string]bool{"": true, "low": true, "medium": true, "high": true}
		}
		if e == "agy" {
			valid = map[string]bool{"": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}
		}
		if e == "codex" {
			listing := obj(CLIModels(cfg)["codex"])
			model := str(obj(cfg[e])["model"])
			if model == "" {
				model = str(listing["default"])
			}
			for _, v := range arr(listing["models"]) {
				m := obj(v)
				if str(m["id"]) == model {
					levels, _ := m["reasoning_levels"].([]string)
					if len(levels) > 0 {
						valid = map[string]bool{"": true}
						for _, level := range levels {
							valid[level] = true
						}
					}
				}
			}
		}

		if !valid[s] {
			return fmt.Errorf("unsupported reasoning effort")
		}
		c := obj(cfg[e])
		c["reasoning_effort"] = s
		cfg[e] = c
	}
	return nil
}
