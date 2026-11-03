package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

type windowState struct {
	Width     int
	Height    int
	Maximized bool
}

func statePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "EasyRead Wails", "window.json")
}
func loadWindow() windowState {
	s := windowState{Width: 1440, Height: 960}
	b, e := os.ReadFile(statePath())
	if e == nil {
		json.Unmarshal(b, &s)
	}
	if s.Width < 700 {
		s.Width = 700
	}
	if s.Height < 500 {
		s.Height = 500
	}
	return s
}
func (a *App) saveWindow() {
	w, h := wr.WindowGetSize(a.ctx)
	s := windowState{w, h, wr.WindowIsMaximised(a.ctx)}
	if s.Maximized {
		old := loadWindow()
		s.Width = old.Width
		s.Height = old.Height
	}
	b, _ := json.Marshal(s)
	os.MkdirAll(filepath.Dir(statePath()), 0700)
	os.WriteFile(statePath(), b, 0600)
}
func openTarget(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "easyread-wails" || u.Host != "open" {
		return "", errors.New("无效的论文链接")
	}
	q := url.Values{}
	for _, key := range []string{"id", "sha256", "block"} {
		if v := u.Query().Get(key); v != "" {
			q.Set(key, v)
		}
	}
	if q.Get("id") == "" && q.Get("sha256") == "" {
		return "", errors.New("链接缺少论文标识")
	}
	return "/open?" + q.Encode(), nil
}
func (a *App) OpenLink(raw string) {
	target, e := openTarget(raw)
	if e != nil {
		return
	}
	a.linkMu.Lock()
	defer a.linkMu.Unlock()
	if !a.pageReady {
		a.pendingLink = target
		return
	}
	a.navigate(target)
}
func (a *App) navigate(target string) {
	b, _ := json.Marshal(target)
	wr.WindowExecJS(a.ctx, "document.querySelector('iframe').contentWindow.postMessage({easyreadOpen:"+string(b)+"},'*')")
	wr.WindowShow(a.ctx)
	wr.WindowUnminimise(a.ctx)
}
func (a *App) PageReady() {
	a.linkMu.Lock()
	defer a.linkMu.Unlock()
	a.pageReady = true
	if a.pendingLink != "" {
		a.navigate(a.pendingLink)
		a.pendingLink = ""
	}
}

type exportPayload struct {
	Filename string `json:"filename"`
	Choose   bool   `json:"choose"`
	Token    string `json:"token"`
	Data     string `json:"data"`
}

func (a *App) Export(p exportPayload) (interface{}, error) {
	name := filepath.Base(p.Filename)
	if name == "." || name == "" {
		return nil, errors.New("缺少文件名")
	}
	path := ""
	if p.Token != "" {
		a.exportMu.Lock()
		path = a.exports[p.Token]
		delete(a.exports, p.Token)
		a.exportMu.Unlock()
		if path == "" {
			return nil, errors.New("请重新选择保存位置")
		}
	}
	if path == "" {
		home, _ := os.UserHomeDir()
		dir := a.exportDir
		if dir == "" {
			dir = filepath.Join(home, "Downloads")
		}
		var err error
		path, err = wr.SaveFileDialog(a.ctx, wr.SaveDialogOptions{DefaultDirectory: dir, DefaultFilename: name})
		if err != nil {
			return nil, err
		}
		if path == "" {
			return false, nil
		}
		a.exportDir = filepath.Dir(path)
	}
	if p.Choose {
		a.exportMu.Lock()
		a.exportSerial++
		token := fmt.Sprint(a.exportSerial)
		a.exports[token] = path
		a.exportMu.Unlock()
		return map[string]string{"token": token, "path": path}, nil
	}
	data, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		return nil, err
	}
	answer, err := wr.MessageDialog(a.ctx, wr.MessageDialogOptions{Type: wr.InfoDialog, Title: "导出完成", Message: filepath.Base(path), Buttons: []string{"打开文件", "在 Finder 中显示", "完成"}, DefaultButton: "打开文件"})
	if err != nil {
		return true, nil
	}
	if answer == "打开文件" {
		exec.Command("open", path).Start()
	} else if answer == "在 Finder 中显示" {
		exec.Command("open", "-R", path).Start()
	}
	return true, nil
}
func (a *App) Save(filename, data string) (bool, error) {
	result, err := a.Export(exportPayload{Filename: filename, Data: data})
	return result == true, err
}
func (a *App) stopService() error {
	client := &http.Client{Timeout: 10 * time.Second}
	r, e := client.Get(a.url + "/api/library")
	if e != nil {
		return e
	}
	var state struct {
		Token string `json:"token"`
	}
	e = json.NewDecoder(r.Body).Decode(&state)
	r.Body.Close()
	if e != nil {
		return e
	}
	req, _ := http.NewRequest("POST", a.url+"/api/shutdown", bytes.NewBufferString("{}"))
	req.Header.Set("X-Token", state.Token)
	req.Header.Set("Content-Type", "application/json")
	r, e = client.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		b, _ := io.ReadAll(r.Body)
		return fmt.Errorf("后台任务尚未结束，已取消退出：%s", strings.TrimSpace(string(b)))
	}
	return nil
}
func (a *App) QuitSaved() error {
	if err := a.stopService(); err != nil {
		return err
	}
	a.saveWindow()
	if err := a.launchInstaller(); err != nil {
		return err
	}
	a.closing.Store(true)
	wr.Quit(a.ctx)
	return nil
}
func (a *App) requestQuit() {
	wr.WindowExecJS(a.ctx, `document.querySelector('iframe').contentWindow.postMessage({easyreadSave:true},'*')`)
}
func (a *App) menus() *menu.Menu {
	m := menu.NewMenu()
	app := m.AddSubmenu("EasyRead Wails")
	app.AddText("检查更新…", nil, func(*menu.CallbackData) { go a.CheckUpdate(true) })
	app.AddSeparator()
	app.AddText("退出", keys.CmdOrCtrl("q"), func(*menu.CallbackData) { a.requestQuit() })
	m.Append(menu.EditMenu())
	file := m.AddSubmenu("文件")
	file.AddText("文献库", keys.CmdOrCtrl("l"), func(*menu.CallbackData) {
		wr.WindowExecJS(a.ctx, `document.querySelector("iframe").contentWindow.postMessage({easyreadLibrary:true},"*")`)
	})
	file.AddText("关闭当前标签页", keys.CmdOrCtrl("w"), func(*menu.CallbackData) {
		wr.WindowExecJS(a.ctx, `document.querySelector('iframe').contentWindow.postMessage({easyreadCloseTab:true},'*')`)
	})
	view := m.AddSubmenu("窗口")
	view.AddText("全屏", keys.Combo("f", keys.CmdOrCtrlKey, keys.ControlKey), func(*menu.CallbackData) {
		if wr.WindowIsFullscreen(a.ctx) {
			wr.WindowUnfullscreen(a.ctx)
		} else {
			wr.WindowFullscreen(a.ctx)
		}
	})
	view.AddText("最小化", keys.CmdOrCtrl("m"), func(*menu.CallbackData) { wr.WindowMinimise(a.ctx) })
	return m
}
