package engines

import (
	"fmt"
	"strings"
)

// Only formatting and bounded-generation options are accepted. Permission,
// sandbox, hooks, plugin, MCP, config-file and alternate input/output flags stay
// exclusively managed by adapters. Values are passed as individual argv entries.
func safeExtraArgs(engine string, raw any) ([]string, error) {
	args := []string{}
	switch values := raw.(type) {
	case nil:
		return args, nil
	case []string:
		args = append(args, values...)
	case []any:
		for _, v := range values {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("extra_args must contain only strings")
			}
			args = append(args, s)
		}
	default:
		return nil, fmt.Errorf("extra_args must be a string array")
	}
	if len(args) == 0 {
		return args, nil
	}
	if engine == "qoder" || engine == "zcode" || engine == "opencode" {
		return nil, fmt.Errorf("%s does not support extra_args", Name(engine))
	}
	valued := map[string]bool{}
	switch engine {
	case "claude", "codebuddy":
		for _, k := range []string{"--max-turns", "--max-budget-usd", "--append-system-prompt", "--system-prompt", "--fallback-model"} {
			valued[k] = true
		}
	case "codex":
	case "grok", "agy", "cursor":
		for _, k := range []string{"--temperature", "--max-tokens", "--max-output-tokens"} {
			valued[k] = true
		}
	}
	flags := map[string]bool{"--verbose": true}
	out := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, val, inline := strings.Cut(arg, "=")
		if engine == "codex" && (key == "-c" || key == "--config") {
			if !inline {
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("missing value for %s", key)
				}
				val = args[i]
			}
			configKey, _, ok := strings.Cut(val, "=")
			safe := map[string]bool{"model_reasoning_effort": true, "model_reasoning_summary": true, "model_verbosity": true, "service_tier": true}
			if !ok || !safe[strings.TrimSpace(configKey)] || strings.ContainsAny(val, "\r\n") {
				return nil, fmt.Errorf("unsafe Codex config override in extra_args")
			}
			out = append(out, "-c", val)
			continue
		}
		if flags[key] && !inline {
			out = append(out, key)
			continue
		}
		if !valued[key] {
			return nil, fmt.Errorf("unsupported or unsafe %s extra argument: %s", Name(engine), key)
		}
		if !inline {
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("missing value for %s", key)
			}
			val = args[i]
		}
		if strings.HasPrefix(val, "--") || strings.ContainsRune(val, 0) {
			return nil, fmt.Errorf("invalid value for %s", key)
		}
		out = append(out, key, val)
	}
	return out, nil
}
