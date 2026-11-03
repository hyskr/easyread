package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx     context.Context
	command *exec.Cmd
	url     string
	closing atomic.Bool
}

func (a *App) PickFolder() (string, error) {
	return wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "选择文献库"})
}
func (a *App) Save(filename, data string) (bool, error) {
	home, _ := os.UserHomeDir()
	path, err := wr.SaveFileDialog(a.ctx, wr.SaveDialogOptions{DefaultDirectory: filepath.Join(home, "Downloads"), DefaultFilename: filepath.Base(filename)})
	if err != nil || path == "" {
		return false, err
	}
	bytes, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, bytes, 0600)
}
func (a *App) Ready()                      { wr.WindowExecJS(a.ctx, `location.replace('/web/tabs.html')`) }
func (a *App) QuitSaved()                  { a.closing.Store(true); wr.Quit(a.ctx) }
func (a *App) startup(ctx context.Context) { a.ctx = ctx }
func main() {
	home, _ := os.UserHomeDir()
	binary, _ := os.Executable()
	backend := filepath.Join(filepath.Dir(binary), "../Resources/backend/easyread-backend")
	if _, err := os.Stat(backend); err != nil {
		backend = filepath.Join("..", "build", "backend", "easyread-backend")
	}
	cmd := exec.Command(backend, "serve", "--port", "0")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1", "EASYREAD_HOME="+filepath.Join(home, "EasyRead"))
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		panic(err)
	}
	ready := make(chan string, 1)
	go func() {
		s := bufio.NewScanner(pipe)
		re := regexp.MustCompile(`http://127\.0\.0\.1:\d+`)
		for s.Scan() {
			if u := re.FindString(s.Text()); u != "" {
				select {
				case ready <- u:
				default:
				}
			}
		}
	}()
	var address string
	select {
	case address = <-ready:
	case <-time.After(45 * time.Second):
		cmd.Process.Kill()
		panic("Backend startup timed out")
	}
	app := &App{command: cmd, url: address}
	target, _ := url.Parse(address)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(r *http.Response) error {
		if strings.Contains(r.Header.Get("Content-Type"), "text/html") {
			b, e := io.ReadAll(r.Body)
			if e != nil {
				return e
			}
			r.Body.Close()
			bridge := `<script src="/wails/ipc.js"></script><script src="/wails/runtime.js"></script><script>
if(window===window.top && ['/','/index.html'].includes(location.pathname))location.replace('/web/tabs.html');
window.easyreadDesktop={isElectron:false,isWails:true,platform:'darwin',pickFolder:()=>window.go.main.App.PickFolder(),saveExport:p=>window.go.main.App.Save(p.filename,p.data),exportDirectory:async()=>'',updateState:async()=>({supported:false}),onUpdateState:()=>()=>{},onBeforeQuit:cb=>{window.wailsSave=cb}};
document.addEventListener('click',async e=>{const a=e.target.closest('a[download]');if(!a)return;e.preventDefault();e.stopImmediatePropagation();try{const b=await(await fetch(a.href)).blob();const r=new FileReader();r.onload=()=>window.go.main.App.Save(a.download,String(r.result).split(',')[1]);r.readAsDataURL(b)}catch(err){alert(err.message)}},true);
</script>`
			html := strings.Replace(string(b), "<head>", "<head>"+bridge, 1)
			r.Body = io.NopCloser(strings.NewReader(html))
			r.ContentLength = int64(len(html))
			r.Header.Del("Content-Length")
		}
		return nil
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	})
	err = wails.Run(&options.App{Title: "EasyRead Wails", Width: 1440, Height: 960, AssetServer: &assetserver.Options{Handler: handler}, OnStartup: app.startup, Mac: &mac.Options{OnUrlOpen: func(raw string) {
		u, e := url.Parse(raw)
		if e == nil && u.Scheme == "easyread" {
			b, _ := json.Marshal("/open?" + u.RawQuery)
			wr.WindowExecJS(app.ctx, "window.easyreadTabs?.open("+string(b)+")")
		}
	}}, OnBeforeClose: func(context.Context) bool {
		if app.closing.Load() {
			return false
		}
		wr.WindowExecJS(app.ctx, `Promise.resolve(window.easyreadTabs?.saveAll()).then(r=>{if(!r||r.every(x=>x!==false))window.go.main.App.QuitSaved()}).catch(e=>alert(e.message))`)
		return true
	}, OnShutdown: func(context.Context) { cmd.Process.Signal(os.Interrupt); go cmd.Wait() }, Bind: []interface{}{app}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
