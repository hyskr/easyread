// easyread is the native backend and command-line interface.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/hyskr/easyread/backend/config"
	"github.com/hyskr/easyread/backend/exporter"
	"github.com/hyskr/easyread/backend/importer"
	"github.com/hyskr/easyread/backend/jobs"
	"github.com/hyskr/easyread/backend/pdf"
	"github.com/hyskr/easyread/backend/server"
	"github.com/hyskr/easyread/backend/store"
	"github.com/hyskr/easyread/backend/translate"
	web "github.com/hyskr/easyread/web"
)

var version = "1.4.6"

type M = map[string]any

func main() {
	server.Version = version
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func output(v any) error {
	e := json.NewEncoder(os.Stdout)
	e.SetEscapeHTML(false)
	e.SetIndent("", " ")
	return e.Encode(v)
}
func run(args []string) error {
	if len(args) == 0 {
		args = []string{"serve", "--open"}
	}
	if args[0] == "--version" || args[0] == "version" {
		fmt.Println(version)
		return nil
	}
	if args[0] == "help" || args[0] == "--help" {
		fmt.Println("easyread serve [--port 8765] [--open] [--exit-on-close]\neasyread list | import SOURCE [--no-translate] [--link] | translate ID [--pages RANGE]\neasyread status ID | blocks ID --from FILE [--done RANGE] [--replace]\neasyread discuss ID --from FILE | --delete ID\neasyread check ID | locate ID | export ID [--mobile] [--out FILE]\neasyread demo ID --out DIRECTORY | merge ID --from FILE | migrate DIRECTORY")
		return nil
	}
	if args[0] == "serve" {
		return serve(args[1:])
	}
	cfg, e := config.New("")
	if e != nil {
		return e
	}
	root, e := cfg.LibraryDir()
	if e != nil {
		return e
	}
	lib, e := store.NewLibrary(root)
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if args[0] == "list" {
		v, e := lib.List()
		if e != nil {
			return e
		}
		if len(args) > 1 && args[1] == "--json" {
			return output(v)
		}
		for _, item := range v {
			title := translate.Str(item["title_zh"])
			if title == "" {
				title = translate.Str(item["title_en"])
			}
			job := translate.Obj(item["job"])
			fmt.Printf("%s  %s  [%d/%d 页] %s %s\n", item["id"], title, translate.Num(item["done_pages"]), translate.Num(item["pages"]), translate.Str(job["state"]), translate.Str(job["message"]))
		}
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("%s requires a paper ID or source", args[0])
	}
	cmd, arg := args[0], args[1]
	flags := flag.NewFlagSet(cmd, flag.ContinueOnError)
	source := flags.String("from", "", "JSON input")
	out := flags.String("out", "", "output path")
	pages := flags.String("pages", "", "page range")
	done := flags.String("done", "", "completed pages")
	replace := flags.Bool("replace", false, "replace selected pages")
	noTranslate := flags.Bool("no-translate", false, "prepare without model")
	link := flags.Bool("link", false, "link original PDF")
	mobile := flags.Bool("mobile", false, "mobile export")
	del := flags.String("delete", "", "discussion ID")
	if e = flags.Parse(args[2:]); e != nil {
		return e
	}
	if cmd == "import" {
		var ws *store.Workspace
		fresh := false
		if _, er := os.Stat(arg); er == nil {
			ws, fresh, e = lib.ImportPath(arg, nil, *link)
		} else {
			if *link {
				return fmt.Errorf("--link only supports local PDF files")
			}
			data, name, meta, er := importer.Fetch(ctx, arg)
			if er != nil {
				return er
			}
			ws, fresh, e = lib.ImportPDF(data, name, meta)
		}
		if e != nil {
			return e
		}
		if !fresh {
			p, e := ws.Load("paper")
			if e != nil {
				return e
			}
			if len(translate.List(translate.Obj(p["meta"])["pages"])) > 0 {
				fmt.Printf("已在库里：%s\n", ws.ID())
				return nil
			}
		}
		if _, e = pdf.Prepare(ctx, ws); e != nil {
			return e
		}
		fmt.Printf("Imported %s (new=%t)\n", ws.ID(), fresh)
		if !*noTranslate {
			return translateWait(ctx, lib, cfg, ws, M{})
		}
		return nil
	}
	if cmd == "migrate" {
		ws, e := migrateFolder(lib, arg)
		if e != nil {
			return e
		}
		fmt.Printf("已搬入文献库：%s（%s）\n", ws.ID(), ws.Root)
		return nil
	}

	all, e := lib.All()
	if e != nil {
		return e
	}
	var ws *store.Workspace
	for _, w := range all {
		if strings.HasPrefix(w.ID(), arg) {
			if ws != nil {
				return fmt.Errorf("paper ID is ambiguous")
			}
			ws = w
		}
	}
	if ws == nil {
		return fmt.Errorf("paper not found: %s", arg)
	}
	switch cmd {
	case "status":
		return printStatus(ws)

	case "translate":
		b := M{}
		if *pages != "" {
			b["pages"] = *pages
		}
		if u := runningServer(cfg); u != "" {
			if e := queueTranslation(u, ws.ID(), b); e == nil {
				fmt.Println("已交给服务排队翻译，进度在文献库页面上看。")
				return nil
			}
		}
		return translateWait(ctx, lib, cfg, ws, b)
	case "locate":
		if e := pdf.RefreshLayout(ctx, ws); e != nil {
			return e
		}
		layout, e := ws.Load("layout")
		if e != nil {
			return e
		}
		fmt.Printf("定位 %d 个块\n", len(layout))
		return nil
	case "export", "demo":
		dir, e := materializeAssets()
		if e != nil {
			return e
		}
		defer os.RemoveAll(dir)
		data, name, e := exporter.HTML(ws, dir, *mobile)
		if e != nil {
			return e
		}
		target := *out
		if cmd == "demo" {
			if target == "" {
				return fmt.Errorf("demo requires --out DIRECTORY")
			}
			if e = os.MkdirAll(target, 0755); e != nil {
				return e
			}
			target = filepath.Join(target, "index.html")
		} else if target == "" {
			target = filepath.Join(ws.Root, name)
		}
		if e = os.WriteFile(target, data, 0600); e != nil {
			return e
		}
		fmt.Println(target)
		return nil
	case "merge":
		data, e := readAnyInput(*source)
		if e != nil {
			return e
		}
		var raw any = data
		if m, ok := data.(map[string]any); ok {
			raw = m["ops"]
		}
		ops := []M{}
		if raw != nil {
			items, ok := raw.([]any)
			if !ok {
				return fmt.Errorf("reader operations must be an array")
			}
			for _, x := range items {
				m, ok := x.(map[string]any)
				if !ok {
					return fmt.Errorf("reader operation must be an object")
				}
				ops = append(ops, m)
			}
		}
		v, e := ws.ApplyReaderOps(ops, "merge")
		if e != nil {
			return e
		}
		fmt.Printf("并入 %d 个操作\n", len(v["applied"].([]string)))
		return nil

	case "blocks":
		data, e := readInput(*source)
		if e != nil {
			return e
		}
		before, e := ws.Load("paper")
		if e != nil {
			return e
		}
		ps, e := translate.ParsePages(*done)
		if e != nil {
			return e
		}
		replacePages := []int(nil)
		if *replace {
			replacePages = ps
		}
		if e = translate.MergeBlocks(ws, data, ps, replacePages, false); e != nil {
			return e
		}
		after, e := ws.Load("paper")
		if e != nil {
			return e
		}
		old := map[string]bool{}
		for _, b := range translate.List(before["blocks"]) {
			old[translate.Str(translate.Obj(b)["id"])] = true
		}
		added, updated := 0, 0
		for _, b := range translate.List(data["blocks"]) {
			if old[translate.Str(translate.Obj(b)["id"])] {
				updated++
			} else {
				added++
			}
		}
		fmt.Printf("paper.json：新增 %d 块，替换 %d 块；已完成页 %v\n", added, updated, translate.Obj(after["translation"])["done_pages"])
		return nil

	case "discuss":
		return discuss(ws, *source, *del)

	case "check":
		return checkPaper(ws)

	}
	return fmt.Errorf("unknown command %q", cmd)
}
func readInput(p string) (M, error) {
	if p == "" {
		return nil, fmt.Errorf("--from FILE is required")
	}
	data, e := readAnyInput(p)
	if e != nil {
		return nil, e
	}
	v, ok := data.(map[string]any)
	if !ok || v == nil {
		return nil, fmt.Errorf("JSON input must be an object")
	}
	return v, nil
}
func translateWait(ctx context.Context, lib *store.Library, cfg *config.Manager, ws *store.Workspace, b M) error {
	j := jobs.New(lib, cfg)
	defer j.Close()
	if _, e := j.Enqueue(ws, b); e != nil {
		return e
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			j.Cancel(ws.ID())
			return ctx.Err()
		case <-tick.C:
			state, e := ws.Load("job")
			if e != nil {
				return e
			}
			switch state["state"] {
			case "done":
				return output(state)
			case "confirm":
				_ = output(state)
				return fmt.Errorf("page limit requires confirmation in the library UI")
			case "error", "partial", "cancelled":
				_ = output(state)
				return fmt.Errorf("translation ended with state %s", state["state"])
			}
		}
	}
}
func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := flags.Int("port", -1, "loopback port, 0 for random")
	open := flags.Bool("open", false, "open browser")
	auto := flags.Bool("exit-on-close", false, "quit after all pages close")
	home := flags.String("home", "", "data directory")
	webRoot := flags.String("web-root", os.Getenv("EASYREAD_WEB_ROOT"), "live frontend directory")
	if e := flags.Parse(args); e != nil {
		return e
	}
	portGiven := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portGiven = true
		}
	})
	if portGiven && (*port < 0 || *port > 65535) {
		return fmt.Errorf("port must be between 0 and 65535")
	}
	manager, e := config.New(*home)
	if e != nil {
		return e
	}
	if !portGiven {
		if url := runningServer(manager); url != "" {
			fmt.Printf("服务已在运行：%s\n", url)
			if !*auto {
				_ = keepServer(url)
			}
			if *open {
				_ = openURL(url)
			}
			return nil
		}
	}
	var assets fs.FS = web.Files
	if *webRoot != "" {
		assets = os.DirFS(*webRoot)
	}
	a, e := server.New(*home, assets)
	if e != nil {
		return e
	}
	defer a.Close()
	cfg, e := a.Config.Load()
	if e != nil {
		return e
	}
	if *port < 0 {
		*port = translate.Num(cfg["port"])
	}
	if *port < 0 || *port > 65535 {
		return fmt.Errorf("port must be between 0 and 65535")
	}
	listener, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if e != nil && *port != 0 {
		listener, e = net.Listen("tcp", "127.0.0.1:0")
	}
	if e != nil {
		return e
	}
	defer listener.Close()
	url := "http://" + listener.Addr().String()
	httpd := &http.Server{Handler: a, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	a.Shutdown = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpd.Shutdown(ctx)
	}
	info := filepath.Join(a.Config.Home, ".server.json")
	temporary := os.Getenv("EASYREAD_LIBRARY") != "" || os.Getenv("COREAD_LIBRARY") != ""
	if !temporary {
		if e = store.WriteJSON(info, M{"url": url, "pid": os.Getpid(), "started": store.Now()}); e != nil {
			return e
		}
		defer func() {
			v, _ := store.ReadJSON(info)
			if translate.Num(v["pid"]) == os.Getpid() {
				os.Remove(info)
			}
		}()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() { <-ctx.Done(); a.Shutdown() }()
	if *auto {
		go a.WatchPresence(ctx)
	}
	fmt.Printf("EasyRead 已启动：%s  文献库：%s\n", url, a.Library.Root)
	if *open {
		go func() { time.Sleep(200 * time.Millisecond); _ = openURL(url) }()
	}
	e = httpd.Serve(listener)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
func openURL(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if e := cmd.Start(); e != nil {
		return e
	}
	go cmd.Wait()
	return nil
}
func materializeAssets() (string, error) {
	dir, e := os.MkdirTemp("", "easyread-assets-")
	if e != nil {
		return "", e
	}
	e = fs.WalkDir(web.Files, ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		b, e := fs.ReadFile(web.Files, p)
		if e != nil {
			return e
		}
		return os.WriteFile(target, b, 0600)
	})
	if e != nil {
		os.RemoveAll(dir)
	}
	return dir, e
}
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(src, p)
		if e != nil {
			return e
		}
		if d.Name() == "server.json" || d.Name() == ".write.lock" || d.Name() == "打开共读.cmd" || strings.HasSuffix(d.Name(), ".html") {
			if d.IsDir() && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink %s", p)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		before, e := os.Stat(p)
		if e != nil {
			return e
		}
		if !before.Mode().IsRegular() {
			return fmt.Errorf("refusing special file %s", p)
		}
		data, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		after, e := os.Stat(p)
		if e != nil {
			return e
		}
		if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return fmt.Errorf("source changed during migration: %s", p)
		}
		f, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(data)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return e
		}
		return ce
	})
}
