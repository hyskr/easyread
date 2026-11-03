package engines

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func DetectMap(cfg Object) Object {
	out := Object{}
	for _, v := range Detect(cfg) {
		out[str(v["id"])] = v
	}
	return out
}
func readObject(path string) Object {
	b, e := os.ReadFile(path)
	if e != nil {
		return Object{}
	}
	var v Object
	if json.Unmarshal(b, &v) != nil || v == nil {
		return Object{}
	}
	return v
}
func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".codex")
}
func codexConfigValue(key string) string {
	b, e := os.ReadFile(filepath.Join(codexHome(), "config.toml"))
	if e != nil {
		return ""
	}
	s := strings.TrimPrefix(string(b), "\ufeff")
	if i := regexp.MustCompile(`(?m)^\s*\[`).FindStringIndex(s); i != nil {
		s = s[:i[0]]
	}
	m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*["']([^"']+)["']`).FindStringSubmatch(s)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}
func seenModels() Object {
	h := os.Getenv("EASYREAD_HOME")
	if h == "" {
		home, _ := os.UserHomeDir()
		h = filepath.Join(home, "EasyRead")
		if wd, e := os.Getwd(); e == nil {
			if _, e = os.Stat(filepath.Join(wd, "pyproject.toml")); e == nil {
				h = wd
			}
		}
	}
	return readObject(filepath.Join(h, ".models-seen.json"))
}
func Pretty(model string) string {
	m := regexp.MustCompile(`^claude-(opus|sonnet|haiku|fable)-(\d+)(?:-(\d{1,2}))?(?:-\d{8})?$`).FindStringSubmatch(model)
	if len(m) == 0 {
		return model
	}
	s := "Claude " + strings.ToUpper(m[1][:1]) + m[1][1:] + " " + m[2]
	if m[3] != "" {
		s += "." + m[3]
	}
	return s
}
func CLIModels(cfg Object) Object {
	out := Object{}
	home, _ := os.UserHomeDir()
	settings := readObject(filepath.Join(home, ".claude", "settings.json"))
	seen := seenModels()
	model := str(settings["model"])
	if model == "" {
		model = str(seen["_default"])
	} else if actual := str(seen[model]); actual != "" {
		model = actual
	}
	if model == "opus" || model == "sonnet" || model == "haiku" || model == "fable" {
		model = "Claude " + strings.ToUpper(model[:1]) + model[1:]
	}
	effort := os.Getenv("CLAUDE_CODE_EFFORT_LEVEL")
	if effort == "" {
		effort = str(settings["effortLevel"])
	}
	aliases := []Object{}
	for i, a := range []string{"opus", "sonnet", "haiku"} {
		aliases = append(aliases, Object{"id": a, "name": []string{"Opus", "Sonnet", "Haiku"}[i], "desc": []string{"最强", "快、省", "最快最省"}[i], "actual": Pretty(str(seen[a]))})
	}
	out["claude"] = Object{"default": Pretty(model), "configured_reasoning": effort, "models": aliases}
	cache := readObject(filepath.Join(codexHome(), "models_cache.json"))
	rows := []Object{}
	for _, v := range arr(cache["models"]) {
		m := obj(v)
		if m["visibility"] == "list" && str(m["slug"]) != "" {
			rows = append(rows, m)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := 99.0, 99.0
		if v, ok := rows[i]["priority"]; ok {
			a = num(v)
		}
		if v, ok := rows[j]["priority"]; ok {
			b = num(v)
		}
		return a < b
	})
	models := []Object{}
	for _, m := range rows {
		levels := []string{}
		for _, r := range arr(m["supported_reasoning_levels"]) {
			if e := str(obj(r)["effort"]); e != "" {
				levels = append(levels, e)
			}
		}
		name := str(m["display_name"])
		if name == "" {
			name = str(m["slug"])
		}
		models = append(models, Object{"id": m["slug"], "name": name, "desc": str(m["description"]), "reasoning_levels": levels, "default_reasoning": str(m["default_reasoning_level"])})
	}
	out["codex"] = Object{"default": codexConfigValue("model"), "configured_reasoning": codexConfigValue("model_reasoning_effort"), "configured_tier": codexConfigValue("service_tier"), "models": models}
	for _, engine := range CLIEngines {
		if out[engine] == nil {
			out[engine] = Object{"default": "", "models": []Object{}, "configured_reasoning": str(obj(cfg[engine])["reasoning_effort"]), "configured_tier": str(obj(cfg[engine])["service_tier"])}
		}
	}
	return out
}
func EngineLabel(cfg Object) string {
	e := str(cfg["engine"])
	name := Name(e)
	model := str(obj(cfg[e])["model"])
	if e == "openai" {
		name = "API"
		for _, p := range Presets() {
			if p["id"] == obj(cfg[e])["preset"] {
				name = str(p["name"])
				break
			}
		}
		name = strings.Split(name, "（")[0]
		if model == "" {
			model = "未填模型"
		}
	} else if e == "claude" {
		if actual := str(seenModels()[model]); actual != "" {
			model = Pretty(actual)
		} else if model == "opus" || model == "sonnet" || model == "haiku" {
			model = "Claude " + strings.ToUpper(model[:1]) + model[1:]
		} else if model == "" {
			model = str(obj(CLIModels(cfg)["claude"])["default"])
		}
	} else if e == "codex" {
		model = Label(Object{"engine": "codex", "model": model})
	}
	if model != "" && model != "GPT" {
		return name + " · " + model
	}
	return name
}
