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

	"golang.org/x/mod/semver"
)

var version = "1.4.22"

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
func (a *App) UpdateStatus() map[string]any {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	result := map[string]any{"current": version, "busy": a.updating.Load(), "supported": false, "message": "尚未检查更新", "cached": a.cachedUpdate()}
	for k, v := range a.updateState {
		result[k] = v
	}
	return result
}
func (a *App) updateProgress(message string, received, total int64) {
	a.updateMu.Lock()
	if a.updateState == nil {
		a.updateState = map[string]any{}
	}
	a.updateState["message"] = message
	a.updateState["received"] = received
	a.updateState["total"] = total
	a.updateMu.Unlock()

}

func updateCache() string {
	home, _ := os.UserCacheDir()
	return filepath.Join(home, "EasyRead Wails", "updates")
}
func (a *App) cachedUpdate() string {
	entries, _ := os.ReadDir(updateCache())
	best := ""
	for _, entry := range entries {
		v := entry.Name()
		if !entry.IsDir() || !semver.IsValid("v"+v) || semver.Compare("v"+v, "v"+version) <= 0 {
			continue
		}
		if _, err := os.Stat(filepath.Join(updateCache(), v, "ready")); err == nil && semver.Compare("v"+v, "v"+best) > 0 {
			best = v
		}
	}
	return best
}
func (a *App) DownloadUpdate() { a.checkUpdate(true, true) }
func (a *App) InstallUpdate() error {
	v := a.cachedUpdate()
	if v == "" {
		return fmt.Errorf("没有已下载的更新")
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	a.installMu.Lock()
	a.installDir = filepath.Join(updateCache(), v)
	a.installSource = filepath.Join(a.installDir, "EasyRead Wails.app")
	a.installTarget = filepath.Clean(filepath.Join(filepath.Dir(binary), "../.."))
	a.installMu.Unlock()
	a.updateProgress("正在保存笔记，准备安装", 0, 0)
	a.requestQuit()
	return nil
}
func (a *App) CheckUpdate(manual bool) { a.checkUpdate(manual, false) }
func (a *App) checkUpdate(manual, download bool) {
	if !a.updating.CompareAndSwap(false, true) {
		return
	}
	defer a.updating.Store(false)
	if manual {
		a.updateProgress("正在检查更新", 0, 0)
	}
	fail := func(e error) {
		a.updateProgress("更新失败："+e.Error(), 0, 0)

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

		return
	}
	a.updateMu.Lock()
	a.updateState = map[string]any{"message": "发现新版本 " + newVersion, "latest": newVersion}
	a.updateMu.Unlock()
	if !download {
		return
	}
	if a.cachedUpdate() == newVersion {
		a.updateProgress("更新已下载，可重启安装", 0, 0)
		return
	}
	dir := filepath.Join(updateCache(), newVersion)
	if err := os.MkdirAll(dir, 0700); err != nil {
		fail(err)
		return
	}
	zipPath := filepath.Join(dir, "update.zip")
	if _, statErr := os.Stat(zipPath); os.IsNotExist(statErr) {
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
		file, err := os.Create(zipPath + ".part")
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
		if err = os.Rename(zipPath+".part", zipPath); err != nil {
			fail(err)
			return
		}

	}
	a.updateProgress("正在解压更新", 0, 0)
	if output, err := exec.Command("ditto", "-x", "-k", zipPath, dir).CombinedOutput(); err != nil {
		fail(fmt.Errorf("解压失败：%s", output))
		return
	}
	updated := filepath.Join(dir, "EasyRead Wails.app")
	if _, err := os.Stat(filepath.Join(updated, "Contents", "MacOS", "EasyRead Wails")); err != nil {
		fail(fmt.Errorf("更新包缺少应用程序：%w", err))
		return
	}
	if err = os.WriteFile(filepath.Join(dir, "ready"), []byte(newVersion), 0600); err != nil {
		fail(err)
		return
	}
	a.updateProgress("更新已下载，可重启安装", 0, 0)

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
if ditto "$2" "$3"; then rm -rf "$backup"; open "$3"; else mv "$backup" "$3"; open "$3"; fi`
	return exec.Command("/bin/sh", "-c", script, "easyread-update", fmt.Sprint(os.Getpid()), a.installSource, a.installTarget, a.installDir).Start()
}
