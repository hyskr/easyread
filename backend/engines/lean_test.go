package engines

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClaudeUnsupportedToolsFallbackOnlyBeforeOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fixture")
	}
	for _, tc := range []struct {
		name, stderr, output string
		wantCalls            int
	}{{"unknown", "error: unknown option '--tools'", "", 2}, {"network", "network down", "", 1}, {"partial", "error: unknown option '--tools'", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"billed"}}`, 1}, {"permission", "permission denied --tools", "", 1}, {"model", "unsupported model --tools", "", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			d := t.TempDir()
			log := filepath.Join(d, "calls")
			exe := filepath.Join(d, "claude")
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\ncase \" $* \" in *' --tools '*)\n"
			if tc.output != "" {
				script += "printf '%s\\n' '" + tc.output + "'\n"
			}
			script += "printf '%s\\n' \"" + tc.stderr + "\" >&2\nexit 1;; esac\nprintf '%s\\n' '{\"type\":\"result\",\"result\":\"ok\"}'\n"
			if e := os.WriteFile(exe, []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			answer, _, err := Run(ctx, Object{"engine": "claude", "claude": Object{"command": exe, "_chat": true, "model": "opus", "reasoning_effort": "high"}}, "test", d, nil)
			if ctx.Err() != nil {
				t.Fatal("mutated caller context")
			}
			if tc.wantCalls == 2 && (err != nil || answer != "ok") {
				t.Fatalf("%q %v", answer, err)
			}
			if tc.wantCalls == 1 && err == nil {
				t.Fatal("expected failure")
			}
			b, _ := os.ReadFile(log)
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			if len(lines) != tc.wantCalls {
				t.Fatalf("calls %d", len(lines))
			}
			for _, line := range lines {
				for _, required := range []string{"--disallowedTools Bash,Write,Edit,MultiEdit,NotebookEdit,Agent,Task", "--allowedTools Read,Glob,Grep,WebSearch,WebFetch", "--strict-mcp-config", "--model opus", "--effort high"} {
					if !strings.Contains(line, required) {
						t.Fatalf("lost safeguard %s: %s", required, line)
					}
				}
			}
		})
	}
}
func TestCodexLeanPreservesConfigDisablesMCPAndFeatures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	source := "\ufeffmodel_provider = \"my-provider\"\nmodel = \"my-model\"\nnotify = [\"do-not-run\"]\n[mcp_servers.foo]\ncommand = \"nope\"\n[mcp_servers.\"foo.bar\"]\n[mcp_servers.foo.env]\nX = \"y\"\n[mcp_servers.'quoted']\n"
	path := filepath.Join(home, "config.toml")
	os.WriteFile(path, []byte(source), 0600)
	args := strings.Join(codexLeanArgs(), " ")
	for _, part := range []string{`mcp_servers."foo".enabled=false`, `mcp_servers."foo.bar".enabled=false`, `mcp_servers."quoted".enabled=false`, `notify=[]`, `features.image_generation=false`, `features.plugins=false`, `features.multi_agent=false`} {
		if !strings.Contains(args, part) {
			t.Fatalf("missing %s in %s", part, args)
		}
	}
	if strings.Contains(args, "provider") || strings.Contains(args, "my-model") || strings.Contains(args, ".env") {
		t.Fatal(args)
	}
	b, _ := os.ReadFile(path)
	if string(b) != source {
		t.Fatal("modified user config")
	}
}
