package engines

import (
	"github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexIsolationRemovesInvalidDisabledTransport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	original := `model_provider="relay"
model="chosen"
[mcp_servers.ppt]
enabled=false
type="stdio"
command=123
[model_providers.relay]
name="Relay"
base_url="https://example.test/v1"
wire_api="responses"
`
	os.WriteFile(filepath.Join(home, "config.toml"), []byte(original), 0600)
	os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"test":"credential"}`), 0600)
	env, err := isolatedCodexHome(t.TempDir(), []string{"PATH=test", "CODEX_HOME=old"})
	if err != nil {
		t.Fatal(err)
	}
	var isolated string
	for _, e := range env {
		if strings.HasPrefix(e, "CODEX_HOME=") {
			isolated = strings.TrimPrefix(e, "CODEX_HOME=")
		}
	}
	raw, err := os.ReadFile(filepath.Join(isolated, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = toml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if _, ok := config["mcp_servers"]; ok {
		t.Fatal("MCP config leaked")
	}
	if config["model"] != "chosen" || config["model_provider"] != "relay" || config["model_providers"] == nil {
		t.Fatal("provider config lost")
	}
	after, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if string(after) != original {
		t.Fatal("modified global config")
	}
	auth, _ := os.ReadFile(filepath.Join(isolated, "auth.json"))
	if string(auth) != `{"test":"credential"}` {
		t.Fatal("login missing")
	}
}
