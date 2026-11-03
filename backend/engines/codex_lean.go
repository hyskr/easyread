package engines

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var mcpHeader = regexp.MustCompile(`(?m)^\s*\[\s*mcp_servers\s*\.\s*("(?:[^"\\]|\\.)*"|'[^']*'|[A-Za-z0-9_-]+)\s*\]`)

func codexLeanArgs() []string {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".codex")
	}
	b, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	text := strings.TrimPrefix(string(b), "\ufeff")
	args := []string{}
	seen := map[string]bool{}
	for _, m := range mcpHeader.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if strings.HasPrefix(name, `"`) {
			var decoded string
			if json.Unmarshal([]byte(name), &decoded) != nil {
				continue
			}
			name = decoded
		} else if strings.HasPrefix(name, "'") {
			name = name[1 : len(name)-1]
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		key, _ := json.Marshal(name)
		args = append(args, "-c", "mcp_servers."+string(key)+".enabled=false")
	}
	root := strings.SplitN(text, "\n[", 2)[0]
	if regexp.MustCompile(`(?m)^\s*notify\s*=`).MatchString(root) {
		args = append(args, "-c", "notify=[]")
	}
	for _, f := range []string{"apps", "plugins", "browser_use", "browser_use_external", "computer_use", "image_generation", "multi_agent", "memories", "goals", "skill_search", "in_app_browser", "tool_suggest", "realtime_conversation", "code_mode_host", "sleep_tool"} {
		args = append(args, "-c", "features."+f+"=false")
	}
	return args
}
