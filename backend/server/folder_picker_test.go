package server

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func fakePicker(platform string, result pickerResult, err error) *folderPicker {
	return &folderPicker{platform: platform, home: "/home/test", lookup: func(name string) (string, error) { return name, nil }, run: func(context.Context, string, []string, []string) (pickerResult, error) { return result, err }}
}

// Direct ports of tests/app/test_folder_pick.py, with no actual dialogs.
func TestFolderPickChosenPathAndEnvironment(t *testing.T) {
	p := fakePicker("windows", pickerResult{}, nil)
	p.run = func(ctx context.Context, name string, args, env []string) (pickerResult, error) {
		if name != "powershell" {
			t.Fatal(name)
		}
		if !reflect.DeepEqual(args[:5], []string{"-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-Command"}) {
			t.Fatal(args)
		}
		found := false
		for _, v := range env {
			if v == "EASYREAD_PICK_TITLE=选文件夹" {
				found = true
			}
		}
		if !found {
			t.Fatal(env)
		}
		return pickerResult{stdout: "D:\\坚果云\\论文\\\n"}, nil
	}
	v, e := p.pick(context.Background(), "选文件夹")
	if e != nil || v["supported"] != true || v["path"] != "D:\\坚果云\\论文" {
		t.Fatal(v, e)
	}
}
func TestFolderPickCancelReturnsEmpty(t *testing.T) {
	p := fakePicker("windows", pickerResult{}, nil)
	v, e := p.pick(context.Background(), "x")
	if e != nil || v["supported"] != true || v["path"] != "" {
		t.Fatal(v, e)
	}
}
func TestFolderPickWindowsFailureFallsBack(t *testing.T) {
	p := fakePicker("windows", pickerResult{exit: 1}, nil)
	v, e := p.pick(context.Background(), "x")
	if e != nil || v["supported"] != false {
		t.Fatal(v, e)
	}
}
func TestFolderPickUnavailable(t *testing.T) {
	p := fakePicker("windows", pickerResult{}, os.ErrNotExist)
	v, e := p.pick(context.Background(), "x")
	if e != nil || v["supported"] != false {
		t.Fatal(v, e)
	}
	p.platform = "linux"
	p.lookup = func(string) (string, error) { return "", os.ErrNotExist }
	p.run = func(context.Context, string, []string, []string) (pickerResult, error) {
		t.Fatal("missing tool must not run")
		return pickerResult{}, nil
	}
	v, e = p.pick(context.Background(), "x")
	if e != nil || v["supported"] != false {
		t.Fatal(v, e)
	}
}
func TestFolderPickPowerShellASCIIAndExplorerStyle(t *testing.T) {
	for _, r := range folderPickerPowerShell {
		if r > 127 {
			t.Fatal("PowerShell script must be ASCII")
		}
	}
	if !strings.Contains(folderPickerPowerShell, "FOS_PICKFOLDERS") || strings.Contains(folderPickerPowerShell, "FolderBrowserDialog") {
		t.Fatal("Explorer-style picker missing")
	}
}
func TestFolderPickLinuxSelectionAndCancel(t *testing.T) {
	for _, tool := range []string{"zenity", "kdialog"} {
		p := fakePicker("linux", pickerResult{}, nil)
		p.lookup = func(name string) (string, error) {
			if name == tool {
				return "/bin/" + tool, nil
			}
			return "", os.ErrNotExist
		}
		title := `title"; $(touch nope)`
		p.run = func(ctx context.Context, name string, args, env []string) (pickerResult, error) {
			if name != "/bin/"+tool || args[len(args)-1] != title {
				t.Fatal(name, args)
			}
			return pickerResult{exit: 1}, nil
		}
		v, e := p.pick(context.Background(), title)
		if e != nil || v["supported"] != true || v["path"] != "" {
			t.Fatal(v, e)
		}
	}
}
func TestFolderPickMacUsesEnvironmentAndCancellation(t *testing.T) {
	p := fakePicker("darwin", pickerResult{}, nil)
	p.run = func(ctx context.Context, name string, args, env []string) (pickerResult, error) {
		if strings.Contains(args[1], "untrusted") || !strings.Contains(args[1], "system attribute") {
			t.Fatal(args)
		}
		return pickerResult{exit: 1, stderr: "User canceled. (-128)"}, nil
	}
	v, e := p.pick(context.Background(), "untrusted\" script")
	if e != nil || v["supported"] != true || v["path"] != "" {
		t.Fatal(v, e)
	}
}
func TestFolderPickContextAndRootPaths(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := fakePicker("windows", pickerResult{}, nil)
	if _, e := p.pick(ctx, "x"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	for _, path := range []string{"/", `C:\`} {
		p.run = func(context.Context, string, []string, []string) (pickerResult, error) {
			return pickerResult{stdout: path}, nil
		}
		v, e := p.pick(context.Background(), "x")
		if e != nil || v["path"] != path {
			t.Fatal(v, e)
		}
	}
}
