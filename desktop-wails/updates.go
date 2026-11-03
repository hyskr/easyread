package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/mod/semver"
)

var version = "1.4.17"

type downloadProgress struct {
	app             *App
	total, received int64
	last            time.Time
}

func (p *downloadProgress) Write(b []byte) (int, error) {
	p.received += int64(len(b))
	if time.Since(p.last) > 200*time.Millisecond || p.received == p.total {
		p.last = time.Now()
		p.app.updateProgress("正在下载更新", p.received, p.total)
	}
	return len(b), nil
}
func (a *App) updateProgress(message string, received, total int64) {
	data, _ := json.Marshal(map[string]any{"message": message, "received": received, "total": total})
	wr.WindowExecJS(a.ctx, "window.showUpdateProgress?.("+string(data)+")")
}

func (a *App) CheckUpdate(manual bool) {
	if !a.updating.CompareAndSwap(false, true) {
		return
	}
	defer a.updating.Store(false)
	if manual {
		a.updateProgress("正在检查更新", 0, 0)
	}
	fail := func(e error) {
		a.updateProgress("更新失败："+e.Error(), 0, 0)
		wr.MessageDialog(a.ctx, wr.MessageDialogOptions{Type: wr.ErrorDialog, Title: "更新失败", Message: e.Error()})
	}
	client := &http.Client{Timeout: 3 * time.Minute}
	response, err := client.Get("https://api.github.com/repos/hyskr/easyread/releases?per_page=30")
	if err != nil {
		if manual {
			fail(err)
		}
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if manual {
			fail(fmt.Errorf("GitHub HTTP %d", response.StatusCode))
		}
		return
	}
	var releases []struct {
		Tag    string `json:"tag_name"`
		Draft  bool   `json:"draft"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err = json.NewDecoder(response.Body).Decode(&releases); err != nil {
		if manual {
			fail(err)
		}
		return
	}
	asset, newVersion := "", ""
	for _, release := range releases {
		if release.Draft || !strings.HasPrefix(release.Tag, "wails-v") {
			continue
		}
		v := strings.TrimPrefix(release.Tag, "wails-")
		if !semver.IsValid(v) || semver.Compare(v, "v"+version) <= 0 {
			continue
		}
		for _, file := range release.Assets {
			if file.Name == "EasyRead-Wails-arm64.zip" && semver.Compare(v, "v"+newVersion) > 0 {
				asset = file.URL
				newVersion = strings.TrimPrefix(v, "v")
			}
		}
	}
	if asset == "" {
		if manual {
			a.updateProgress("当前已是最新版本", 0, 0)
		}
		if manual {
			wr.MessageDialog(a.ctx, wr.MessageDialogOptions{Title: "检查更新", Message: "当前已是最新的 Wails 版本。"})
		}
		return
	}
	answer, _ := wr.MessageDialog(a.ctx, wr.MessageDialogOptions{Title: "发现新版本", Message: "EasyRead Wails " + newVersion, Buttons: []string{"下载并安装", "稍后"}, DefaultButton: "下载并安装"})
	if answer != "下载并安装" {
		a.updateProgress("已取消更新", 0, 0)
		return
	}
	binary, _ := os.Executable()
	current := filepath.Clean(filepath.Join(filepath.Dir(binary), "../.."))
	requirement, err := exec.Command("codesign", "-d", "-r-", current).CombinedOutput()
	if err != nil {
		fail(err)
		return
	}
	parts := strings.SplitN(string(requirement), "designated => ", 2)
	if len(parts) != 2 || strings.Contains(parts[1], "cdhash") {
		wr.BrowserOpenURL(a.ctx, "https://github.com/hyskr/easyread/releases")
		fail(fmt.Errorf("本机临时签名版需手动安装；自动安装需要固定签名的发布版"))
		return
	}
	dir, err := os.MkdirTemp("", "easyread-wails-update-")
	if err != nil {
		fail(err)
		return
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(dir)
		}
	}()
	a.updateProgress("正在连接下载服务器", 0, 0)
	client.Timeout = 30 * time.Minute
	r, err := client.Get(asset)
	if err != nil {
		fail(err)
		return
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		fail(fmt.Errorf("下载 HTTP %d", r.StatusCode))
		return
	}
	zipPath := filepath.Join(dir, "update.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		fail(err)
		return
	}
	progress := &downloadProgress{app: a, total: r.ContentLength}
	a.updateProgress("正在下载更新", 0, r.ContentLength)
	_, err = io.Copy(file, io.TeeReader(r.Body, progress))
	file.Close()
	if err != nil {
		fail(err)
		return
	}
	a.updateProgress("正在解压更新", 0, 0)
	if output, err := exec.Command("ditto", "-x", "-k", zipPath, dir).CombinedOutput(); err != nil {
		fail(fmt.Errorf("解压失败：%s", output))
		return
	}
	a.updateProgress("正在验证更新签名", 0, 0)
	updated := filepath.Join(dir, "EasyRead Wails.app")
	if output, err := exec.Command("codesign", "--verify", "--deep", "--strict", "-R", strings.TrimSpace(parts[1]), updated).CombinedOutput(); err != nil {
		fail(fmt.Errorf("更新包签名不匹配：%s", output))
		return
	}
	a.installMu.Lock()
	a.installDir = dir
	a.installSource = updated
	a.installTarget = current
	a.installMu.Unlock()
	keep = true
	a.updateProgress("正在保存笔记，准备安装", 0, 0)
	a.requestQuit()
}
func (a *App) launchInstaller() error {
	a.installMu.Lock()
	defer a.installMu.Unlock()
	if a.installDir == "" {
		return nil
	}
	script := `while kill -0 "$1" 2>/dev/null; do sleep 1; done
backup="$3.previous"
if ! mv "$3" "$backup"; then open "$4"; exit 1; fi
if ditto "$2" "$3"; then rm -rf "$backup"; open "$3"; rm -rf "$4"; else mv "$backup" "$3"; open "$3"; fi`
	return exec.Command("/bin/sh", "-c", script, "easyread-update", fmt.Sprint(os.Getpid()), a.installSource, a.installTarget, a.installDir).Start()
}
