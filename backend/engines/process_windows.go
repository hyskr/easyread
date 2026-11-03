//go:build windows

package engines

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// taskkill's /T terminates descendants as well as the CLI. Use the OS-owned
// absolute path rather than PATH lookup, so project binaries cannot replace it.
func configureProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		root := os.Getenv("SystemRoot")
		if root == "" || !filepath.IsAbs(root) {
			root = `C:\Windows`
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		killer := exec.CommandContext(ctx, filepath.Join(root, "System32", "taskkill.exe"), "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
		if err := killer.Run(); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
