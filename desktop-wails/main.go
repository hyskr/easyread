package main

import (
	"bufio"
	"context"

	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx           context.Context
	command       *exec.Cmd
	url           string
	closing       atomic.Bool
	updating      atomic.Bool
	linkMu        sync.Mutex
	pageReady     bool
	pendingLink   string
	exportMu      sync.Mutex
	exports       map[string]string
	exportSerial  uint64
	exportDir     string
	installMu     sync.Mutex
	installDir    string
	installSource string
	installTarget string
}

func (a *App) PickFolder() (string, error) {
	return wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "选择文献库"})
}
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	app := &App{command: cmd, url: address, exports: make(map[string]string)}
	state := loadWindow()
	target, _ := url.Parse(address)
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		originalDirector(r)
		r.Host = target.Host
		if r.Header.Get("Origin") != "" {
			r.Header.Set("Origin", address)
		}
	}
	proxy.ModifyResponse = func(r *http.Response) error {
		if strings.Contains(r.Header.Get("Content-Type"), "text/html") {
			b, e := io.ReadAll(r.Body)
			if e != nil {
				return e
			}
			r.Body.Close()
			bridge := `<script>
window.addEventListener("keydown",e=>{if(e.key==="Escape"&&!e.isComposing)e.preventDefault()});
const pending=new Map();let serial=0;
function native(method,...args){return new Promise((resolve,reject)=>{const id=++serial;pending.set(id,{resolve,reject});window.top.postMessage({easyreadNative:true,id,method,args},'*')})}
window.addEventListener('message',e=>{const d=e.data;if(d?.easyreadReply){const p=pending.get(d.id);if(p){pending.delete(d.id);d.error?p.reject(Error(d.error)):p.resolve(d.value)}}if(d?.easyreadLibrary&&window.easyreadTabs)window.easyreadTabs.select('library');if(d?.easyreadCloseTab&&window.easyreadTabs){const tab=document.querySelector('.paper-tab.active .tab-title');if(tab&&tab.id!=='tab-library')window.easyreadTabs.close(tab.id.slice(4))}if(d?.easyreadOpen&&window.easyreadTabs)window.easyreadTabs.open(d.easyreadOpen);if(d?.easyreadSave&&window.easyreadTabs)window.easyreadTabs.saveAll().then(r=>{if(r.every(v=>v!==false))return native('QuitSaved');throw Error('笔记尚未保存成功，已取消退出')}).catch(error=>alert(error.message))});
window.addEventListener('load',()=>{if(window.easyreadTabs)native('PageReady')});
window.go={main:{App:{PickFolder:()=>native('PickFolder'),Save:(...args)=>native('Save',...args)}}};
window.easyreadDesktop={isElectron:false,isWails:true,deepLinkScheme:'easyread-wails',platform:'darwin',pickFolder:()=>window.go.main.App.PickFolder(),saveExport:p=>native('Export',p),exportDirectory:async()=>'',updateState:async()=>({supported:false}),onUpdateState:()=>()=>{},onBeforeQuit:cb=>{window.wailsSave=cb}};
const originalClick=HTMLAnchorElement.prototype.click;HTMLAnchorElement.prototype.click=function(){if(this.download){exportFile(this);return}return originalClick.call(this)};async function exportFile(a){try{const b=await(await fetch(a.href)).blob();const r=new FileReader();r.onload=()=>window.go.main.App.Save(a.download,String(r.result).split(',')[1]);r.readAsDataURL(b)}catch(err){alert(err.message)}};document.addEventListener('click',async e=>{const a=e.target.closest('a[download]');if(!a)return;e.preventDefault();e.stopImmediatePropagation();try{const b=await(await fetch(a.href)).blob();const r=new FileReader();r.onload=()=>window.go.main.App.Save(a.download,String(r.result).split(',')[1]);r.readAsDataURL(b)}catch(err){alert(err.message)}},true);
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

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	defer server.Close()
	pageURL := "http://" + listener.Addr().String() + "/web/tabs.html"
	pageJSON, _ := json.Marshal(pageURL)
	host := `<!doctype html><html><head><style>html,body,iframe{margin:0;border:0;width:100%;height:100%;overflow:hidden}</style></head><body><iframe id="app"></iframe><script>
 const app=document.querySelector('iframe');app.src=` + string(pageJSON) + `;
 window.addEventListener('message',async e=>{const d=e.data;if(!d?.easyreadNative||e.origin!==new URL(app.src).origin)return;
 if(!['PickFolder','Save','Export','QuitSaved','PageReady'].includes(d.method))return;
 try{const value=await window.go.main.App[d.method](...d.args);e.source.postMessage({easyreadReply:true,id:d.id,value},e.origin)}catch(error){e.source.postMessage({easyreadReply:true,id:d.id,error:error.message},e.origin)}});
 </script></body></html>`
	nativeHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, host)
	})
	err = wails.Run(&options.App{Title: "EasyRead Wails", Width: state.Width, Height: state.Height, MinWidth: 700, MinHeight: 500, AssetServer: &assetserver.Options{Handler: nativeHandler}, OnStartup: app.startup, Menu: app.menus(), Mac: &mac.Options{OnUrlOpen: app.OpenLink}, OnDomReady: func(ctx context.Context) {
		if state.Maximized {
			wr.WindowMaximise(ctx)
		}
		go app.CheckUpdate(false)
	}, OnBeforeClose: func(context.Context) bool {
		if app.closing.Load() {
			return false
		}
		wr.WindowExecJS(app.ctx, `document.querySelector("iframe").contentWindow.postMessage({easyreadSave:true},"*")`)
		return true
	}, OnShutdown: func(context.Context) {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		}
	}, Bind: []interface{}{app}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
