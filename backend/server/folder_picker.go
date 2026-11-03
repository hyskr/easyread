package server

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Static ASCII script keeps titles and paths out of PowerShell code and works
// with PowerShell 5.1. The Explorer dialog is owned by an invisible WinForms form.
const folderPickerPowerShell = `
$code = @'
using System;
using System.Runtime.InteropServices;
public static class EasyReadPicker {
  [ComImport, Guid("DC1C5A9C-E88A-4dde-A5A1-60F82A20AEF7")] class FileOpenDialog {}
  [ComImport, Guid("42f85136-db7e-439c-85f1-e4075d135fc8"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
  interface IFileDialog {
    [PreserveSig] int Show(IntPtr parent);
    void SetFileTypes(uint c, IntPtr t); void SetFileTypeIndex(uint i); void GetFileTypeIndex(out uint i);
    void Advise(IntPtr e, out uint c); void Unadvise(uint c);
    void SetOptions(uint o); void GetOptions(out uint o);
    void SetDefaultFolder(IShellItem s); void SetFolder(IShellItem s);
    void GetFolder(out IShellItem s); void GetCurrentSelection(out IShellItem s);
    void SetFileName([MarshalAs(UnmanagedType.LPWStr)] string n); void GetFileName(out IntPtr n);
    void SetTitle([MarshalAs(UnmanagedType.LPWStr)] string t);
    void SetOkButtonLabel([MarshalAs(UnmanagedType.LPWStr)] string t); void SetFileNameLabel([MarshalAs(UnmanagedType.LPWStr)] string t);
    void GetResult(out IShellItem s);
  }
  [ComImport, Guid("43826D1E-E718-42EE-BC55-A1E261C37BFE"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
  interface IShellItem {
    void BindToHandler(IntPtr p, ref Guid b, ref Guid r, out IntPtr v); void GetParent(out IShellItem s);
    void GetDisplayName(uint t, [MarshalAs(UnmanagedType.LPWStr)] out string n);
  }
  [DllImport("shell32.dll", CharSet = CharSet.Unicode, PreserveSig = false)]
  static extern void SHCreateItemFromParsingName(string path, IntPtr bc, ref Guid riid, out IShellItem item);
  [DllImport("user32.dll")] static extern bool SetForegroundWindow(IntPtr h);
  public static string Pick(string title, string start) {
    var d = (IFileDialog)new FileOpenDialog();
    uint o; d.GetOptions(out o); d.SetOptions(o | 0x20 | 0x40);  // FOS_PICKFOLDERS | FOS_FORCEFILESYSTEM
    if (!string.IsNullOrEmpty(title)) d.SetTitle(title);
    if (!string.IsNullOrEmpty(start)) {
      try { var g = typeof(IShellItem).GUID; IShellItem s; SHCreateItemFromParsingName(start, IntPtr.Zero, ref g, out s); d.SetFolder(s); } catch {}
    }
    // Background process: without a topmost owner the dialog opens behind the browser.
    var owner = new System.Windows.Forms.Form();
    owner.TopMost = true; owner.ShowInTaskbar = false; owner.Opacity = 0;
    owner.FormBorderStyle = System.Windows.Forms.FormBorderStyle.None;
    owner.StartPosition = System.Windows.Forms.FormStartPosition.CenterScreen;
    owner.Size = new System.Drawing.Size(1, 1);
    owner.Show(); owner.Activate(); SetForegroundWindow(owner.Handle);
    try { if (d.Show(owner.Handle) != 0) return ""; } finally { owner.Close(); }
    IShellItem r; d.GetResult(out r); string p; r.GetDisplayName(0x80058000, out p);  // SIGDN_FILESYSPATH
    return p;
  }
}
'@
Add-Type -TypeDefinition $code -Language CSharp -ReferencedAssemblies System.Windows.Forms, System.Drawing
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
[Console]::Out.Write([EasyReadPicker]::Pick($env:EASYREAD_PICK_TITLE, $env:EASYREAD_PICK_START))
`

type pickerResult struct {
	stdout, stderr string
	exit           int
}
type folderPicker struct {
	platform string
	home     string
	lookup   func(string) (string, error)
	run      func(context.Context, string, []string, []string) (pickerResult, error)
}

func runFolderDialog(ctx context.Context, name string, args, env []string) (pickerResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := pickerResult{stdout: stdout.String(), stderr: stderr.String()}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.exit = exit.ExitCode()
		return result, nil
	}
	return result, err
}
func pickFolder(ctx context.Context, title string) (Object, error) {
	home, _ := os.UserHomeDir()
	picker := folderPicker{platform: runtime.GOOS, home: home, lookup: exec.LookPath, run: runFolderDialog}
	return picker.pick(ctx, title)
}
func (p folderPicker) pick(ctx context.Context, title string) (Object, error) {
	unavailable := Object{"supported": false, "path": ""}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Choose a library folder"
	}
	runes := []rune(title)
	if len(runes) > 100 {
		title = string(runes[:100])
	}
	title = strings.ReplaceAll(title, "\x00", "")
	var name string
	var args []string
	switch p.platform {
	case "windows":
		name = "powershell"
		args = []string{"-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-Command", folderPickerPowerShell}
	case "darwin":
		name = "osascript"
		args = []string{"-e", `POSIX path of (choose folder with prompt (system attribute "EASYREAD_PICK_TITLE"))`}
	default:
		if resolved, err := p.lookup("zenity"); err == nil {
			name = resolved
			args = []string{"--file-selection", "--directory", "--title", title}
		} else if resolved, err := p.lookup("kdialog"); err == nil {
			name = resolved
			args = []string{"--getexistingdirectory", p.home, "--title", title}
		} else {
			return unavailable, nil
		}
	}
	if p.platform == "windows" || p.platform == "darwin" {
		resolved, err := p.lookup(name)
		if err != nil {
			return unavailable, nil
		}
		name = resolved
	}
	env := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "EASYREAD_PICK_TITLE=") && !strings.HasPrefix(v, "EASYREAD_PICK_START=") {
			env = append(env, v)
		}
	}
	env = append(env, "EASYREAD_PICK_TITLE="+title, "EASYREAD_PICK_START="+p.home)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	result, err := p.run(ctx, name, args, env)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return unavailable, nil
	}
	if result.exit != 0 {
		canceled := p.platform != "windows" && result.exit == 1
		if p.platform == "darwin" {
			canceled = strings.Contains(result.stderr, "-128") || strings.Contains(strings.ToLower(result.stderr), "user canceled")
		}
		if canceled {
			return Object{"supported": true, "path": ""}, nil
		}
		return unavailable, nil
	}
	path := strings.TrimSpace(result.stdout)
	// Preserve filesystem roots while removing the dialog's ordinary trailing slash.
	for len(path) > 1 && strings.ContainsAny(path[len(path)-1:], "/\\") {
		if len(path) == 3 && path[1] == ':' {
			break
		}
		path = path[:len(path)-1]
	}
	return Object{"supported": true, "path": path}, nil
}
