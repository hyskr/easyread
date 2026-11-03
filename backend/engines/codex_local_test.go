package engines

import (
	"os"
	"os/exec"
	"testing"
)

func TestLocalCodexConfigStartup(t *testing.T) {
	if os.Getenv("EASYREAD_TEST_LOCAL_CODEX") != "1" {
		t.Skip("local Codex startup check")
	}
	env, err := isolatedCodexHome(t.TempDir(), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("codex", "login", "status")
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("startup: %v: %s", err, output)
	}
}
