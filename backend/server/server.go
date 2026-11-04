// Package server exposes the stable EasyRead HTTP API from a native Go backend.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hyskr/easyread/backend/chat"
	"github.com/hyskr/easyread/backend/config"
	"github.com/hyskr/easyread/backend/engines"
	"github.com/hyskr/easyread/backend/exporter"
	"github.com/hyskr/easyread/backend/importer"
	"github.com/hyskr/easyread/backend/jobs"
	"github.com/hyskr/easyread/backend/pdf"
	"github.com/hyskr/easyread/backend/store"
)

type Object = map[string]any

const MaxUpload = 200 * 1024 * 1024

var Version = "1.4.6"

type App struct {
	Config         *config.Manager
	Library        *store.Library
	Jobs           *jobs.Manager
	Bulk           *importer.Manager
	Assets         fs.FS
	Token          string
	WebRoot        string
	Shutdown       func()
	UpdateURL      string
	updateMu       sync.Mutex
	mu             sync.Mutex
	status         string
	active         int
	pages          int
	keep           bool
	presenceState  presenceState
	preparing      map[string]bool
	cleanup        func()
	background     context.Context
	stopBackground context.CancelFunc
	prepareWG      sync.WaitGroup
}

func New(home string, assets fs.FS) (*App, error) {
	cfg, e := config.New(home)
	if e != nil {
		return nil, e
	}
	root, e := cfg.LibraryDir()
	if e != nil {
		return nil, e
	}
	lib, e := store.NewLibrary(root)
	if e != nil {
		return nil, e
	}
	secret := make([]byte, 24)
	if _, e = rand.Read(secret); e != nil {
		return nil, e
	}
	a := &App{Config: cfg, Library: lib, Assets: assets, Token: hex.EncodeToString(secret), status: "idle", preparing: map[string]bool{}}
	a.background, a.stopBackground = context.WithCancel(context.Background())
	a.Jobs = jobs.New(lib, cfg)
	a.Bulk = importer.New(lib, cfg)
	a.Bulk.OnImported = func(ws *store.Workspace) error { _, err := a.Jobs.Prepare(ws); return err }
	return a, nil
}
func (a *App) Close() {
	a.mu.Lock()
	a.status = "closed"
	a.mu.Unlock()
	if a.stopBackground != nil {
		a.stopBackground()
	}
	a.prepareWG.Wait()
	a.PDFClose()
	a.Bulk.Close()
	a.Jobs.Close()
	if a.cleanup != nil {
		a.cleanup()
	}
}
func (a *App) Status() string { a.mu.Lock(); defer a.mu.Unlock(); return a.status }
func (a *App) Busy() bool {
	a.mu.Lock()
	active := a.active > 0 || len(a.preparing) > 0
	a.mu.Unlock()
	return active || a.Jobs.Busy() || a.Bulk.Busy() || a.PDFBusy()
}
func object(v any) Object {
	x, _ := v.(map[string]any)
	if x == nil {
		return Object{}
	}
	return x
}
func text(v any) string { s, _ := v.(string); return s }
func integer(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}
func array(v any) []any {
	x, _ := v.([]any)
	if x == nil {
		return []any{}
	}
	return x
}
func response(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
func fail(w http.ResponseWriter, status int, e error) {
	response(w, status, Object{"error": e.Error()})
}
func body(w http.ResponseWriter, r *http.Request) (Object, error) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxUpload)
	defer r.Body.Close()
	v := Object{}
	d := json.NewDecoder(r.Body)
	e := d.Decode(&v)
	if e == io.EOF {
		return v, nil
	}
	if e != nil {
		return nil, e
	}
	if v == nil {
		return nil, fmt.Errorf("request body must be an object")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("request must contain one JSON object")
	}
	return v, nil
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		fail(w, 403, fmt.Errorf("untrusted host"))
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" && r.Method != "POST" {
		w.Header().Set("Allow", "GET, HEAD, POST")
		fail(w, 405, fmt.Errorf("method not allowed"))
		return
	}
	if r.Method == "POST" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Token")), []byte(a.Token)) != 1 {
		fail(w, 403, fmt.Errorf("bad token"))
		return
	}
	if r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+r.Host && r.Header.Get("Origin") != "https://"+r.Host {
		fail(w, 403, fmt.Errorf("bad origin"))
		return
	}
	defer func() {
		if p := recover(); p != nil {
			fail(w, 500, fmt.Errorf("internal server error"))
		}
	}()
	protected := r.Method == "POST" && !map[string]bool{"/api/library/move": true, "/api/library/inspect": true, "/api/library/pick-folder": true, "/api/library/reveal": true, "/api/shutdown": true, "/api/presence/keep": true}[r.URL.Path]
	readProtected := r.Method != "POST" && (strings.HasPrefix(r.URL.Path, "/read/") || strings.HasPrefix(r.URL.Path, "/p/") || (strings.HasPrefix(r.URL.Path, "/api/p/") && !strings.HasSuffix(r.URL.Path, "/versions")))
	a.mu.Lock()
	if readProtected && a.status == "moving" {
		a.mu.Unlock()
		fail(w, 409, fmt.Errorf("library is moving"))
		return
	}
	if readProtected && a.status == "idle" {
		protected = true
	}
	if protected && a.status != "idle" {
		a.mu.Unlock()
		fail(w, 409, fmt.Errorf("library location changed; restart EasyRead before editing"))
		return
	}
	if protected {
		a.active++
	}
	a.mu.Unlock()
	if protected {
		defer func() { a.mu.Lock(); a.active--; a.mu.Unlock() }()
	}
	var e error
	if r.Method == "POST" {
		e = a.post(w, r)
	} else {
		e = a.get(w, r)
	}
	if e != nil {
		status := 400
		var modelError *chat.ModelError
		if errors.As(e, &modelError) {
			status = http.StatusInternalServerError
		}
		if errors.Is(e, os.ErrNotExist) {
			status = 404
		}
		var size *http.MaxBytesError
		if errors.As(e, &size) {
			status = 413
		}
		fail(w, status, e)
	}
}
func (a *App) get(w http.ResponseWriter, r *http.Request) error {
	p := r.URL.Path
	q := r.URL.Query()
	switch p {
	case "/", "/index.html":
		return a.page(w, r, "library.html")
	case "/babeldoc", "/web/babeldoc.html":
		http.Redirect(w, r, "/", 302)
		return nil
	case "/web/tabs.html":
		return a.page(w, r, "tabs.html")
	case "/api/version":
		response(w, 200, Object{"api": 1, "version": Version, "backend": "go"})
		return nil
	case "/api/health":
		response(w, 200, Object{"ok": true, "backend": "go", "pdf": pdf.Dependencies()})
		return nil
	case "/api/library":
		items, e := a.Library.List()
		if e != nil {
			return e
		}
		cfg, e := a.Config.Load()
		if e != nil {
			return e
		}
		trash, _ := store.TrashItems(a.Library.Root)
		response(w, 200, Object{"items": items, "token": a.Token, "jobs": a.Jobs.Status(""), "library_status": a.Status(), "other_device": nil, "engine": cfg["engine"], "engine_label": engines.EngineLabel(cfg), "first_run": a.Config.FirstRun(), "version": Version, "trash": len(trash)})
		return nil
	case "/api/config":
		cfg, e := a.Config.Load()
		if e != nil {
			return e
		}
		response(w, 200, Object{"config": a.Config.Public(cfg), "presets": presets(), "groups": presetGroups()})
		return nil
	case "/api/prefs":
		v, e := a.Config.Prefs()
		if e == nil {
			response(w, 200, v)
		}
		return e
	case "/api/engines":
		cfg, e := a.Config.Load()
		if e != nil {
			return e
		}
		response(w, 200, Object{"found": engines.DetectMap(cfg), "ready": engines.Ready(cfg), "engine": cfg["engine"], "models": engines.CLIModels(cfg)})
		return nil
	case "/api/chat/models":
		cfg, e := a.Config.Load()
		if e == nil {
			response(w, 200, engines.ChatModels(cfg))
		}
		return e
	case "/api/jobs":
		response(w, 200, Object{"jobs": a.Jobs.Status(q.Get("pid"))})
		return nil
	case "/api/log":
		response(w, 200, Object{"text": tail(filepath.Join(a.Config.Home, "easyread.log"), 200), "path": filepath.Join(a.Config.Home, "easyread.log")})
		return nil
	case "/api/trash":
		v, e := store.TrashItems(a.Library.Root)
		if e == nil {
			response(w, 200, Object{"items": v})
		}
		return e
	case "/api/library/location":
		v, e := a.location()
		if e == nil {
			response(w, 200, v)
		}
		return e
	case "/api/zotero/detect":
		response(w, 200, Object{"candidates": importer.Candidates()})
		return nil
	case "/api/migrate/status":
		response(w, 200, a.Bulk.Status())
		return nil
	case "/api/presence":
		return a.presence(w, r)
	case "/api/update":
		v, e := a.update(r.Context(), q.Get("force") == "1")
		if e == nil {
			response(w, 200, v)
		}
		return e
	case "/open":
		id := q.Get("id")
		sha := strings.ToLower(q.Get("sha256"))
		if regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(sha) {
			id = sha[:12]
		}
		ws, e := a.Library.Workspace(id)
		target := "/"
		if e == nil && ws != nil {
			target = "/read/" + ws.ID()
			if b := q.Get("block"); regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(b) {
				target += "#" + url.PathEscape(b)
			}
		}
		http.Redirect(w, r, target, 302)
		return nil
	}
	if strings.HasPrefix(p, "/read/") {
		return a.page(w, r, "reader.html")
	}
	if strings.HasPrefix(p, "/web/") {
		return a.asset(w, r, strings.TrimPrefix(p, "/web/"))
	}
	if strings.HasPrefix(p, "/api/p/") {
		parts := strings.Split(strings.TrimPrefix(p, "/api/p/"), "/")
		if len(parts) < 2 {
			return os.ErrNotExist
		}
		ws, e := a.Library.Workspace(parts[0])
		if e != nil {
			return e
		}
		if ws == nil {
			return os.ErrNotExist
		}
		cfg, e := a.Config.Load()
		if e != nil {
			return e
		}
		switch parts[1] {
		case "citations":
			graph, err := a.Library.CitationGraph(ws.ID())
			if err != nil {
				return err
			}
			response(w, 200, graph)
			return nil
		case "state":
			if a.Status() == "idle" {
				item, e := ws.Load("item")
				if e != nil {
					return e
				}
				patch := Object{"last_opened": store.Now()}
				if item["status"] == nil || item["status"] == "unread" {
					patch["status"] = "reading"
				}
				if _, e = ws.PatchItem(patch); e != nil {
					return e
				}
			}
			v := Object{"versions": ws.Versions(), "token": a.Token, "id": ws.ID(), "library_status": a.Status(), "engine": cfg["engine"], "figure_images_enabled": object(cfg["figure_images"])["enabled"] == true}
			for _, n := range []string{"paper", "discussion", "reader", "layout", "item", "job"} {
				v[n], e = ws.Load(n)
				if e != nil {
					return e
				}
			}
			response(w, 200, v)
			a.prepareLater(ws)
			return nil
		case "versions":
			v := Object{"library_status": a.Status(), "figure_images_enabled": object(cfg["figure_images"])["enabled"] == true}
			for k, x := range ws.Versions() {
				v[k] = x
			}
			response(w, 200, v)
			return nil
		case "part":
			if len(parts) != 3 || !map[string]bool{"paper": true, "discussion": true, "reader": true, "layout": true, "job": true}[parts[2]] {
				return os.ErrNotExist
			}
			v, e := ws.Load(parts[2])
			if e == nil {
				response(w, 200, Object{"data": v, "version": ws.Versions()[parts[2]]})
			}
			return e
		case "chat":
			threads, e := chat.Threads(ws)
			if e != nil {
				return e
			}
			v := engines.ChatModels(cfg)
			v["threads"] = threads
			v["limits"] = nil
			response(w, 200, v)
			return nil
		case "log":
			response(w, 200, Object{"text": tail(filepath.Join(ws.Root, "job.log"), 300)})
			return nil
		case "export":
			if a.Status() != "idle" {
				return fmt.Errorf("restart EasyRead before exporting from the changed library")
			}
			root, e := a.exportAssets()
			if e != nil {
				return e
			}
			prefs, _ := a.Config.Prefs()
			b, name, e := exporter.HTMLWithLanguage(ws, root, q.Get("mobile") == "1", text(object(prefs["ui"])["lang"]), text(cfg["target"]))
			if e != nil {
				return e
			}
			w.Header().Set("Content-Disposition", `attachment; filename="export.html"; filename*=UTF-8''`+url.PathEscape(name))
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, e = w.Write(b)
			return e
		case "babeldoc":
			return a.pdfExportGet(w, r, ws, parts[2:])
		}
	}
	if strings.HasPrefix(p, "/p/") {
		parts := strings.SplitN(strings.TrimPrefix(p, "/p/"), "/", 2)
		if len(parts) != 2 {
			return os.ErrNotExist
		}
		ws, e := a.Library.Workspace(parts[0])
		if e != nil {
			return e
		}
		if ws == nil {
			return os.ErrNotExist
		}
		rel := parts[1]
		file := ""
		if rel == "source.pdf" {
			file, e = ws.SourcePDF()
		} else if strings.HasPrefix(rel, "extract/") && regexp.MustCompile(`^extract/page-[0-9]{3,6}\.chars\.json$`).MatchString(rel) {
			if q.Get("selection") == "2" {
				n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rel, "extract/page-"), ".chars.json"))
				file, e = pdf.SelectableText(r.Context(), ws, n)
			} else {
				file, e = safeFile(ws.Root, rel)
			}
		} else if strings.HasPrefix(rel, "pages/") || strings.HasPrefix(rel, "figures/") || regexp.MustCompile(`^chat-media/[a-f0-9]{32}\.png$`).MatchString(rel) {
			if strings.HasPrefix(rel, "pages/") && q.Has("w") && a.Status() == "idle" {
				width, _ := strconv.Atoi(q.Get("w"))
				file, e = pdf.PageVariant(r.Context(), ws, rel, width)
			} else {
				file, e = safeFile(ws.Root, rel)
			}
		} else {
			return os.ErrNotExist
		}
		if e != nil {
			return e
		}
		http.ServeFile(w, r, file)
		return nil
	}
	return os.ErrNotExist
}
func (a *App) post(w http.ResponseWriter, r *http.Request) error {
	p := r.URL.Path
	q := r.URL.Query()
	if strings.HasPrefix(p, "/api/p/") && (strings.HasSuffix(p, "/citation-import") || strings.HasSuffix(p, "/citation-import-pdf")) {
		return a.importCitation(w, r)
	}
	if p == "/api/import" {
		r.Body = http.MaxBytesReader(w, r.Body, MaxUpload)
		b, e := io.ReadAll(r.Body)
		if e != nil {
			return e
		}
		ws, fresh, e := a.Library.ImportPDF(b, q.Get("name"), nil)
		if e != nil {
			return e
		}
		v := Object{}
		for k, x := range q {
			v[k] = x[0]
		}
		v["read"] = q.Get("read") == "1"
		v["translate"] = q.Get("translate") != "0"
		return a.importResult(w, ws, fresh, v)
	}
	b, e := body(w, r)
	if e != nil {
		return e
	}
	var v any
	switch p {
	case "/api/config":
		delete(b, "library_dir")
		delete(b, "translation_extra")
		if e = validateConfig(b); e != nil {
			return e
		}
		cfg, err := a.Config.Save(b)
		e = err
		v = Object{"config": a.Config.Public(cfg)}
	case "/api/prefs":
		v, e = a.Config.SavePrefs(b)
	case "/api/config/test":
		cfg, err := a.Config.Load()
		if err != nil {
			return err
		}
		if b["engine"] != nil {
			cfg["engine"] = b["engine"]
		}
		v = engines.Test(r.Context(), cfg)
	case "/api/models/list":
		cfg, err := a.Config.Load()
		if err != nil {
			return err
		}
		if text(b["api_key"]) == "" || strings.HasPrefix(text(b["api_key"]), "••••") {
			b["api_key"] = config.Resolve(object(cfg["openai"]), text(b["preset"]), text(b["base_url"]))
		}
		models, err := engines.Models(r.Context(), b)
		if err != nil {
			v = Object{"ok": false, "message": err.Error()}
		} else {
			v = Object{"ok": true, "models": models}
		}
	case "/api/chat/models":
		patch := Object{"chat": Object{}}
		for _, k := range []string{"models", "default"} {
			if x, ok := b[k]; ok {
				object(patch["chat"])[k] = x
			}
		}
		if keys, ok := b["keys"].(map[string]any); ok {
			cfg, err := a.Config.Load()
			if err != nil {
				return err
			}
			saved := config.SavedKeys(object(cfg["openai"]))
			for k, val := range keys {
				if s := text(val); s != "" && !strings.HasPrefix(s, "••••") {
					saved[k] = s
				}
			}
			patch["openai"] = Object{"keys": saved}
		}
		cfg, err := a.Config.Save(patch)
		e = err
		v = engines.ChatModels(cfg)
	case "/api/import-url", "/api/import-arxiv":
		data, name, meta, err := importer.Fetch(r.Context(), text(b["ref"]))
		if err != nil {
			return err
		}
		ws, fresh, err := a.Library.ImportPDF(data, name, meta)
		if err != nil {
			return err
		}
		return a.importResult(w, ws, fresh, b)
	case "/api/trash":
		switch text(b["action"]) {
		case "restore":
			id, err := store.Restore(a.Library.Root, text(b["name"]))
			e = err
			v = Object{"id": id}
		case "purge":
			n, err := store.Purge(a.Library.Root, text(b["name"]))
			e = err
			v = Object{"deleted": n}
		case "empty":
			n, err := store.EmptyTrash(a.Library.Root)
			e = err
			v = Object{"deleted": n}
		default:
			return fmt.Errorf("action must be restore, purge or empty")
		}
	case "/api/jobs/stop":
		var ids []string
		if raw, ok := b["ids"]; ok {
			arr, ok := raw.([]any)
			if !ok {
				return fmt.Errorf("ids must be a string array")
			}
			ids = []string{}
			for _, x := range arr {
				s, ok := x.(string)
				if !ok {
					return fmt.Errorf("ids must be a string array")
				}
				ids = append(ids, s)
			}
		}
		n, err := a.Jobs.Stop(ids)
		e = err
		v = Object{"stopped": n}
	case "/api/migrate/scan":
		v, e = a.Bulk.Scan(b)
	case "/api/migrate/start":
		v, e = a.Bulk.Start(b)
	case "/api/migrate/stop":
		v = a.Bulk.Stop()
	case "/api/library/inspect":
		v, e = inspectLibrary(text(b["path"]))
	case "/api/library/move":
		v, e = a.moveLibrary(b)
	case "/api/library/cleanup":
		v, e = a.cleanupLibrary(b)
	case "/api/library/pick-folder":
		v, e = pickFolder(r.Context(), text(b["title"]))
	case "/api/library/reveal":
		e = reveal(a.Library.Root)
		v = Object{"ok": e == nil}
	case "/api/presence/keep":
		a.mu.Lock()
		a.keep = true
		a.mu.Unlock()
		v = Object{"ok": true}
	case "/api/shutdown":
		if a.Busy() {
			return fmt.Errorf("library operations are still running")
		}
		if a.Shutdown == nil {
			return fmt.Errorf("server shutdown unavailable")
		}
		response(w, 200, Object{"ok": true})
		go a.Shutdown()
		return nil
	case "/api/update":
		_, e = a.Config.Save(Object{"check_updates": b["enabled"] == true})
		if e == nil {
			v, e = a.update(r.Context(), false)
		}
	default:
		if !strings.HasPrefix(p, "/api/p/") {
			return os.ErrNotExist
		}
		parts := strings.Split(strings.TrimPrefix(p, "/api/p/"), "/")
		if len(parts) < 2 {
			return os.ErrNotExist
		}
		ws, err := a.Library.Workspace(parts[0])
		if err != nil {
			return err
		}
		if ws == nil {
			return os.ErrNotExist
		}
		cfg, err := a.Config.Load()
		if err != nil {
			return err
		}
		switch parts[1] {
		case "ops":
			raw, ok := b["ops"].([]any)
			if !ok {
				return fmt.Errorf("ops must be an array")
			}
			ops := []map[string]any{}
			for _, x := range raw {
				m, ok := x.(map[string]any)
				if !ok {
					return fmt.Errorf("operation must be an object")
				}
				ops = append(ops, m)
			}
			client := text(b["client"])
			if len(client) > 40 {
				client = client[:40]
			}
			result, err := ws.ApplyReaderOps(ops, client)
			e = err
			if err == nil {
				result["versions"] = ws.Versions()
			}
			v = result
		case "item":
			v, e = ws.PatchItem(b)
		case "translate":
			v, e = a.Jobs.Enqueue(ws, b)
		case "figure-images":
			v, e = a.Jobs.FigureImages(ws, b)
		case "prepare":
			v, e = a.Jobs.Prepare(ws)
		case "cancel":
			a.Jobs.Cancel(ws.ID())
			v = Object{"ok": true}
		case "answer", "retranslate":
			v, e = a.Jobs.Small(parts[1], ws.ID(), b)
		case "delete":
			if a.PDFWorkspaceBusy(ws.Root) {
				return fmt.Errorf("wait for PDF export before deleting this paper")
			}
			a.Jobs.Cancel(ws.ID())
			name, err := a.Library.Trash(ws.ID())
			e = err
			v = Object{"trash": name}
		case "reveal":
			e = reveal(ws.Root)
			v = Object{"ok": e == nil}
		case "chat":
			if len(parts) > 2 {
				v, e = chat.Mutate(ws, parts[2], b)
			} else {
				return chat.Handle(w, r, ws, cfg, b)
			}
		case "notehelp":
			return chat.NoteHelp(w, r, ws, cfg, b)
		case "translate-selection":
			v, e = chat.TranslateSelection(r.Context(), ws, cfg, b)
		case "chat-export":
			v, e = exporter.Markdown(ws, b)
		case "figure-crop":
			job, err := ws.Load("job")
			if err != nil {
				return err
			}
			if job["state"] == "queued" || job["state"] == "running" {
				return fmt.Errorf("wait for translation before cropping")
			}
			v, e = pdf.ManualCrop(r.Context(), ws, b)
		case "discussion_del":
			count := 0
			e = ws.Update("discussion", func(d Object) error {
				entries := []any{}
				for _, x := range array(d["entries"]) {
					if text(object(x)["id"]) == text(b["id"]) {
						count++
					} else {
						entries = append(entries, x)
					}
				}
				d["entries"] = entries
				return nil
			})
			v = Object{"deleted": count > 0}
		case "babeldoc":
			return a.pdfExportPost(w, r, ws, parts[2:], b)
		default:
			return os.ErrNotExist
		}
	}
	if e == nil {
		response(w, 200, v)
	}
	return e
}
func (a *App) importResult(w http.ResponseWriter, ws *store.Workspace, fresh bool, b Object) error {
	paper, e := ws.Load("paper")
	if e != nil {
		return e
	}
	job, e := ws.Load("job")
	if e != nil {
		return e
	}
	active := job["state"] == "queued" || job["state"] == "running"
	queued := fresh || (len(array(object(paper["meta"])["pages"])) == 0 && !active)
	if queued {
		if b["translate"] == false {
			_, e = a.Jobs.Prepare(ws)
		} else {
			_, e = a.Jobs.Enqueue(ws, b)
		}
		if e != nil {
			return e
		}
	}
	response(w, 200, Object{"id": ws.ID(), "new": fresh, "queued": queued})
	return nil
}
func (a *App) prepareLater(ws *store.Workspace) {
	a.mu.Lock()
	if a.status != "idle" || a.preparing[ws.Root] {
		a.mu.Unlock()
		return
	}
	a.preparing[ws.Root] = true
	a.prepareWG.Add(1)
	a.mu.Unlock()
	go func() {
		defer func() { a.mu.Lock(); delete(a.preparing, ws.Root); a.mu.Unlock(); a.prepareWG.Done() }()
		job, e := ws.Load("job")
		if e != nil || job["state"] == "running" || job["state"] == "queued" {
			return
		}
		ctx, cancel := context.WithTimeout(a.background, 10*time.Minute)
		defer cancel()
		paper, err := ws.Load("paper")
		if err != nil {
			return
		}
		if len(array(object(paper["meta"])["pages"])) == 0 && text(object(paper["meta"])["source_pdf_path"]) != "" {
			if _, err = pdf.Prepare(ctx, ws); err != nil {
				return
			}
		}
		_ = pdf.RefreshLayout(ctx, ws)
	}()
}
func safeFile(root, rel string) (string, error) {
	if strings.Contains(rel, "\\") || strings.HasPrefix(rel, "/") || path.Clean(rel) != rel || rel == "." || strings.HasPrefix(rel, "../") {
		return "", os.ErrNotExist
	}
	base, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", e
	}
	file, e := filepath.EvalSymlinks(filepath.Join(base, filepath.FromSlash(rel)))
	if e != nil {
		return "", e
	}
	r, e := filepath.Rel(base, file)
	if e != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", os.ErrNotExist
	}
	st, e := os.Stat(file)
	if e != nil {
		return "", e
	}
	if !st.Mode().IsRegular() {
		return "", os.ErrNotExist
	}
	return file, nil
}
func (a *App) asset(w http.ResponseWriter, r *http.Request, rel string) error {
	if !fs.ValidPath(rel) || strings.Contains(rel, "\\") {
		return os.ErrNotExist
	}
	data, e := fs.ReadFile(a.Assets, rel)
	if e != nil {
		return e
	}
	ctype := mime.TypeByExtension(path.Ext(rel))
	if ctype == "" {
		ctype = http.DetectContentType(data)
	}
	w.Header().Set("Content-Type", ctype)
	if strings.HasPrefix(rel, "vendor/") {
		w.Header().Set("Cache-Control", "max-age=86400")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	if r.Method != "HEAD" {
		_, e = w.Write(data)
	}
	return e
}
func (a *App) page(w http.ResponseWriter, r *http.Request, name string) error {
	data, e := fs.ReadFile(a.Assets, name)
	if e != nil {
		return e
	}
	s := string(data)
	prefs, _ := a.Config.Prefs()
	lang := text(object(prefs["ui"])["lang"])
	choice := lang
	if choice == "" {
		choice = "auto"
	}
	if env := os.Getenv("EASYREAD_LANG"); env != "" {
		lang = env
		choice = env
	}
	if lang == "" || lang == "auto" {
		tag := os.Getenv("EASYREAD_SYSTEM_LANG")
		if tag == "" {
			tag = os.Getenv("LANG")
		}
		lang = "en"
		if strings.HasPrefix(strings.ToLower(tag), "zh") {
			lang = "zh"
			if strings.Contains(strings.ToLower(tag), "hant") || strings.Contains(strings.ToLower(tag), "tw") || strings.Contains(strings.ToLower(tag), "hk") {
				lang = "zh-Hant"
			}
		}
	}
	if lang != "zh" && lang != "zh-Hant" && lang != "en" {
		lang = "en"
	}
	tag := lang
	if lang == "zh" {
		tag = "zh-CN"
	}
	cfg, e := a.Config.Load()
	if e != nil {
		return e
	}
	target := text(cfg["target"])
	if target == "" {
		target = "zh"
	}
	s = strings.Replace(s, `<html lang="zh-CN">`, fmt.Sprintf(`<html lang="%s" data-lang-choice="%s" data-target="%s">`, html.EscapeString(tag), html.EscapeString(choice), html.EscapeString(target)), 1)
	if lang != "zh" {
		if b, err := fs.ReadFile(a.Assets, "i18n/"+lang+".json"); err == nil {
			s = strings.Replace(s, "</title>", "</title>\n<script id=\"pr-i18n\" type=\"application/json\">"+strings.ReplaceAll(string(b), "<", "\\u003c")+"</script>", 1)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "HEAD" {
		_, e = io.WriteString(w, s)
	}
	return e
}
func (a *App) exportAssets() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.WebRoot != "" {
		return a.WebRoot, nil
	}
	dir, e := os.MkdirTemp("", "easyread-web-")
	if e != nil {
		return "", e
	}
	e = fs.WalkDir(a.Assets, ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		b, e := fs.ReadFile(a.Assets, p)
		if e != nil {
			return e
		}
		return os.WriteFile(target, b, 0600)
	})
	if e != nil {
		os.RemoveAll(dir)
		return "", e
	}
	a.WebRoot = dir
	a.cleanup = func() { os.RemoveAll(dir) }
	return dir, nil
}
func tail(p string, n int) string {
	b, e := os.ReadFile(p)
	if e != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
