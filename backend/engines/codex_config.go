package engines

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Codex validates disabled MCP transports too, so remove unrelated integrations
// from a private config before invoking the CLI.
func isolatedCodexHome(work string, env []string) ([]string, error) {
	source := os.Getenv("CODEX_HOME")
	if source == "" {
		home, _ := os.UserHomeDir()
		source = filepath.Join(home, ".codex")
	}
	source, _ = filepath.Abs(source)
	dir := filepath.Join(work, "codex-home")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	config := map[string]any{}
	raw, err := os.ReadFile(filepath.Join(source, "config.toml"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(raw) > 0 {
		if err = toml.Unmarshal([]byte(strings.TrimPrefix(string(raw), "\ufeff")), &config); err != nil {
			return nil, fmt.Errorf("Codex config: %w", err)
		}
	}
	clean := map[string]any{}
	for _, key := range []string{"model", "model_provider", "model_providers", "model_reasoning_effort", "service_tier", "openai_base_url", "chatgpt_base_url", "cli_auth_credentials_store", "preferred_auth_method", "forced_login_method", "forced_chatgpt_workspace_id", "model_context_window", "model_auto_compact_token_limit"} {
		if value, ok := config[key]; ok {
			clean[key] = value
		}
	}
	// Resolve the selected profile's model/provider settings without importing its tools.
	if name, ok := config["profile"].(string); ok {
		if profiles, ok := config["profiles"].(map[string]any); ok {
			if profile, ok := profiles[name].(map[string]any); ok {
				for _, key := range []string{"model", "model_provider", "model_reasoning_effort", "service_tier"} {
					if value, ok := profile[key]; ok {
						clean[key] = value
					}
				}
			}
		}
	}
	raw, err = toml.Marshal(clean)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, "config.toml"), raw, 0600); err != nil {
		return nil, err
	}
	auth := filepath.Join(source, "auth.json")
	if _, err = os.Stat(auth); err == nil {
		// Keep refreshed file-based credentials shared with the user's existing login.
		if err = os.Symlink(auth, filepath.Join(dir, "auth.json")); err != nil {
			b, e := os.ReadFile(auth)
			if e != nil {
				return nil, e
			}
			if e = os.WriteFile(filepath.Join(dir, "auth.json"), b, 0600); e != nil {
				return nil, e
			}
		}
	}
	result := []string{}
	for _, entry := range env {
		if !strings.HasPrefix(entry, "CODEX_HOME=") {
			result = append(result, entry)
		}
	}
	return append(result, "CODEX_HOME="+dir), nil
}
