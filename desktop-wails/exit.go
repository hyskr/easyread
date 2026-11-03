package main

import (
	"errors"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) backendExited() bool {
	select {
	case <-a.backendDone:
		return true
	default:
		return false
	}
}

func (a *App) QuitFailed(message string) {
	a.quitMu.Lock()
	if a.quitPhase == "idle" {
		a.quitMu.Unlock()
		return
	}
	if a.quitTimer != nil {
		a.quitTimer.Stop()
		a.quitTimer = nil
	}
	a.quitPhase = "idle"
	a.quitMu.Unlock()
	wr.MessageDialog(a.ctx, wr.MessageDialogOptions{Type: wr.ErrorDialog, Title: "未退出", Message: message})
}

func (a *App) requestQuit() {
	a.quitMu.Lock()
	if a.closing.Load() || (a.quitPhase != "" && a.quitPhase != "idle") {
		a.quitMu.Unlock()
		return
	}
	a.quitPhase = "waiting"
	a.quitMu.Unlock()
	if a.backendExited() {
		answer, _ := wr.MessageDialog(a.ctx, wr.MessageDialogOptions{Type: wr.WarningDialog, Title: "后台服务已退出", Message: "无法确认页面中最后的修改是否保存。可先取消退出并复制未保存的笔记。", Buttons: []string{"取消", "退出"}, DefaultButton: "取消", CancelButton: "取消"})
		if answer != "退出" {
			a.quitMu.Lock()
			a.quitPhase = "idle"
			a.quitMu.Unlock()
			return
		}
		if err := a.QuitSaved(); err != nil {
			a.QuitFailed(err.Error())
		}
		return
	}
	a.quitMu.Lock()
	a.quitTimer = time.AfterFunc(20*time.Second, func() {
		a.QuitFailed("页面未在 20 秒内确认保存，已取消退出。请检查笔记后重试。")
	})
	a.quitMu.Unlock()
	wr.WindowExecJS(a.ctx, `document.querySelector('iframe').contentWindow.postMessage({easyreadSave:true},'*')`)
}

func (a *App) QuitSaved() error {
	a.quitMu.Lock()
	if err := acceptSavedPhase(&a.quitPhase); err != nil {
		a.quitMu.Unlock()
		return err
	}
	if a.quitTimer != nil {
		a.quitTimer.Stop()
		a.quitTimer = nil
	}
	a.quitMu.Unlock()
	// Read native window state while the service and page are still available.
	a.saveWindow()
	if !a.backendExited() {
		if err := a.stopService(); err != nil {
			if !a.backendExited() {
				return err
			}
		}
	}
	if err := a.launchInstaller(); err != nil {
		wr.MessageDialog(a.ctx, wr.MessageDialogOptions{Type: wr.ErrorDialog, Title: "更新安装未启动", Message: err.Error() + "。后台服务已关闭，应用将退出；可重新打开后重试更新。"})
	}
	a.closing.Store(true)
	// Return the binding result before stopping the native event loop.
	go func() { time.Sleep(100 * time.Millisecond); quitNative() }()
	return nil
}

// Only an active save request may advance to stopping the backend.
func acceptSavedPhase(phase *string) error {
	if *phase != "waiting" {
		return errors.New("退出请求已取消，请重新退出")
	}
	*phase = "stopping"
	return nil
}
