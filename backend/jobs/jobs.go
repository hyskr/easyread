// Package jobs provides independent durable paper and small-task worker queues.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/hyskr/easyread/backend/config"
	"github.com/hyskr/easyread/backend/engines"
	"github.com/hyskr/easyread/backend/pdf"
	"github.com/hyskr/easyread/backend/store"
	tr "github.com/hyskr/easyread/backend/translate"
)

type Object = map[string]any
type activeRun struct{ cancel context.CancelFunc }

type Manager struct {
	lib             *store.Library
	cfg             *config.Manager
	mu              sync.Mutex
	bulk            []string
	small           []string
	recent          []Object
	cancels         map[string]*activeRun
	runBulk         func(context.Context, *store.Workspace, Object) error
	writeStatus     func(*store.Workspace, Object) error
	preparePDF      func(context.Context, *store.Workspace) ([]map[string]any, error)
	wake, smallWake chan struct{}
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	closed          bool
}

func stamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func copyMap(m Object) Object {
	out := Object{}
	for k, v := range m {
		out[k] = v
	}
	return out
}
func New(lib *store.Library, cfg *config.Manager) *Manager {
	m := newManager(lib, cfg)
	c, _ := cfg.Load()
	m.startWorkers(workerCount(c))
	return m
}

// newManager separates durable recovery from worker startup, allowing controlled
// tests of lifecycle races without package-global hooks or real model calls.
func newManager(lib *store.Library, cfg *config.Manager) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{lib: lib, cfg: cfg, cancels: map[string]*activeRun{}, wake: make(chan struct{}, 1), smallWake: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
	m.runBulk = m.run
	m.preparePDF = pdf.Prepare
	m.resume()
	return m
}
func workerCount(cfg Object) int {
	n := 1
	switch v := cfg["paper_concurrency"].(type) {
	case int:
		n = v
	case float64:
		if v >= 1 && v <= 4 && v == float64(int(v)) {
			n = int(v)
		} else if v > 4 && v == float64(int(v)) {
			n = 4
		}
	}
	if n < 1 {
		return 1
	}
	if n > 4 {
		return 4
	}
	return n
}
func (m *Manager) startWorkers(workers int) {
	for i := 0; i < workers; i++ {
		m.wg.Add(1)
		go m.loop(false)
	}
	m.wg.Add(1)
	go m.loop(true)
}

func signal(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}
func (m *Manager) write(ws *store.Workspace, fields Object) error {
	if m.writeStatus != nil {
		return m.writeStatus(ws, fields)
	}
	return ws.Update("job", func(j Object) error {
		for k, v := range fields {
			j[k] = v
		}
		j["updated"] = stamp()
		return nil
	})
}
func (m *Manager) resume() {
	all, e := m.lib.All()
	if e == nil {
		for _, ws := range all {
			j, e := ws.Load("job")
			if e != nil {
				continue
			}
			s := tr.Str(j["state"])
			if s != "queued" && s != "running" {
				continue
			}
			if s == "running" && j["type"] == "figure-images" {
				_ = m.write(ws, Object{"state": "cancelled", "message": "Image generation interrupted by restart; saved images retained. Retry manually to avoid duplicate charges."})
				continue
			}
			fields := Object{"state": "queued", "message": "Resuming after restart"}
			if j["pages"] != nil {
				remaining := []int{}
				for _, n := range pageInts(j["pages"]) {
					if !has(pageInts(j["completed_pages"]), n) {
						remaining = append(remaining, n)
					}
				}
				fields["pages"] = remaining
			}
			if m.write(ws, fields) == nil {
				m.bulk = append(m.bulk, ws.ID())
			}
		}
	}
	saved, e := store.ReadJSON(filepath.Join(m.lib.Root, "small-jobs.json"))
	if e == nil {
		for _, x := range tr.List(saved["jobs"]) {
			j := tr.Obj(x)
			if j["state"] == "running" {
				j["state"] = "error"
				j["message"] = "Interrupted by restart; retry manually"
			}
			if j["state"] == "queued" {
				m.small = append(m.small, tr.Str(j["id"]))
			}
			m.recent = append(m.recent, j)
		}
	}
	if len(m.bulk) > 0 {
		signal(m.wake)
	}
	if len(m.small) > 0 {
		signal(m.smallWake)
	}
}
func (m *Manager) Enqueue(ws *store.Workspace, body Object) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("job manager closed")
	}
	last, e := ws.Load("job")
	if e != nil {
		return nil, e
	}
	if last["state"] == "queued" || last["state"] == "running" {
		return nil, errors.New("paper already has a queued or running task")
	}
	pending := last["state"] == "confirm"
	retry := tr.Bool(body["failed"])
	scope := tr.Str(body["scope"])
	read := tr.Bool(body["read"])
	model := tr.Str(body["model"])
	target := tr.Str(body["target"])
	extra := body["translation_extra"]
	if extra == nil {
		extra = ""
		if pending || retry {
			if last["translation_extra"] != nil {
				extra = last["translation_extra"]
			}
		}
	}
	extraText, e := tr.ValidateExtra(extra)
	if e != nil {
		return nil, e
	}
	explicit := body["pages"] != nil
	var pages []int
	capCheck := false
	if explicit {
		pages, e = tr.ParsePages(body["pages"])
		if e != nil {
			return nil, e
		}
	}
	if pending {
		read = tr.Bool(last["read"])
		model = tr.Str(last["model"])
		target = tr.Str(last["target"])
		if scope == "" {
			scope = tr.Str(last["scope"])
		}
		if !explicit && last["pages"] != nil {
			pages, e = tr.ParsePages(last["pages"])
			if e != nil {
				return nil, e
			}
			capCheck = tr.Bool(last["cap_check"])
			if tr.Str(body["scope"]) != "" && scope != tr.Str(last["scope"]) {
				selected, e := tr.ScopePages(ws, scope)
				if e != nil {
					return nil, e
				}
				pages = intersection(pages, selected)
				capCheck = false
			}
		}
	}
	if retry {
		pages = []int{}
		for k := range tr.Obj(last["failed"]) {
			n, e := strconv.Atoi(k)
			if e == nil && n > 0 {
				pages = append(pages, n)
			}
		}
		sort.Ints(pages)
		if len(pages) == 0 {
			pages = nil
		}
		read = tr.Bool(last["read"])
		model = tr.Str(last["model"])
		target = tr.Str(last["target"])
	} else if tr.Bool(body["en"]) {
		read = false
		if !explicit && !pending {
			paper, e := ws.Load("paper")
			if e != nil {
				return nil, e
			}
			pages = pageInts(tr.Obj(paper["translation"])["en_pages"])
			capCheck = true
		}
	}
	if scope == "" {
		scope = "all"
	}
	cfg, e := m.cfg.Load()
	if e != nil {
		return nil, e
	}
	paper, e := ws.Load("paper")
	if e != nil {
		return nil, e
	}
	if target == "" {
		target = tr.Target(tr.Obj(paper["meta"]), cfg)
	} else {
		target = tr.ValidTarget(target)
	}
	doTranslate := body["translate"] != false
	kind := "translate"
	if read {
		kind = "read"
	}
	if !doTranslate {
		kind = "prepare"
	}
	var planned any
	if pages != nil {
		planned = pages
	}
	j := Object{"type": kind, "state": "queued", "message": "Queued", "pages": planned, "scope": scope, "translate": doTranslate, "read": read, "model": model, "confirmed": body["confirmed"] == true, "target": target, "cap_check": capCheck, "translation_extra": extraText, "done": 0, "total": 0, "error": "", "failed": Object{}, "usage": Object{}, "completed_pages": []int{}}
	e = ws.Update("job", func(old Object) error {
		for k, v := range j {
			old[k] = v
		}
		delete(old, "page_cap")
		old["updated"] = stamp()
		return nil
	})
	if e != nil {
		return nil, e
	}
	m.bulk = append(m.bulk, ws.ID())
	signal(m.wake)
	return Object{"ok": true}, nil
}
func pageInts(v any) []int {
	if a, ok := v.([]int); ok {
		return append([]int{}, a...)
	}
	a := []int{}
	for _, x := range tr.List(v) {
		a = append(a, tr.Num(x))
	}
	return a
}
func has(a []int, n int) bool {
	for _, v := range a {
		if v == n {
			return true
		}
	}
	return false
}
func intersection(a, b []int) []int {
	out := []int{}
	for _, n := range a {
		if has(b, n) {
			out = append(out, n)
		}
	}
	return out
}
func (m *Manager) Prepare(ws *store.Workspace) (Object, error) {
	return m.Enqueue(ws, Object{"translate": false})
}
func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if run := m.cancels[id]; run != nil {
		run.cancel()
	}
	ws, e := m.lib.Workspace(id)
	if e != nil || ws == nil {
		return
	}
	j, e := ws.Load("job")
	if e != nil {
		return
	}
	if j["state"] == "queued" {
		_ = m.write(ws, Object{"state": "cancelled", "message": "Cancelled"})
	} else if j["state"] == "confirm" {
		_ = m.write(ws, Object{"state": "done", "message": "Imported without translation"})
	}
}
func (m *Manager) Stop(ids []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	all, e := m.lib.All()
	if e != nil {
		return 0, e
	}
	n := 0
	for _, ws := range all {
		if ids != nil {
			found := false
			for _, id := range ids {
				if id == ws.ID() {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		j, e := ws.Load("job")
		if e != nil {
			return n, e
		}
		s := tr.Str(j["state"])
		if (s != "queued" && s != "running" && s != "confirm") || !tr.Bool(j["translate"]) {
			continue
		}
		n++
		if s == "running" {
			if run := m.cancels[ws.ID()]; run != nil {
				run.cancel()
			}
			continue
		}
		state := "cancelled"
		fields := Object{}
		if s == "confirm" {
			state = "done"
		} else if j["type"] != "figure-images" {
			p, e := ws.Load("paper")
			if e != nil {
				return n, e
			}
			if len(tr.List(tr.Obj(p["meta"])["pages"])) == 0 {
				state = "queued"
				fields["type"] = "prepare"
				fields["translate"] = false
				fields["read"] = false
			}
		}
		fields["state"] = state
		fields["message"] = "Model work stopped"
		if e = m.write(ws, fields); e != nil {
			return n, e
		}
	}
	return n, nil
}
func (m *Manager) loop(small bool) {
	defer m.wg.Done()
	wake := m.wake
	if small {
		wake = m.smallWake
	}
	for {
		m.mu.Lock()
		q := &m.bulk
		if small {
			q = &m.small
		}
		id := ""
		if len(*q) > 0 {
			id = (*q)[0]
			*q = (*q)[1:]
			if len(*q) > 0 {
				signal(wake)
			}
		}
		m.mu.Unlock()
		if m.ctx.Err() != nil {
			return
		}
		if id != "" {
			if small {
				m.processSmall(id)
			} else {
				m.process(id)
			}
			continue
		}
		select {
		case <-m.ctx.Done():
			return
		case <-wake:
		}
	}
}
func (m *Manager) process(id string) {
	m.mu.Lock()
	ws, e := m.lib.Workspace(id)
	if e != nil || ws == nil {
		m.mu.Unlock()
		return
	}
	j, e := ws.Load("job")
	if e != nil || j["state"] != "queued" {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	run := &activeRun{cancel: cancel}
	m.cancels[id] = run
	e = m.write(ws, Object{"state": "running", "message": "Starting"})
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		if m.cancels[id] == run {
			delete(m.cancels, id)
		}
		m.mu.Unlock()
	}()
	if e != nil {
		tr.Journal(ws, "Cannot mark task running: "+e.Error())
		return
	}
	defer func() {
		if r := recover(); r != nil {
			m.finishRun(ws, run, Object{"state": "error", "error": fmt.Sprint(r), "message": "Task failed"})
		}
	}()
	e = m.runBulk(ctx, ws, j)
	if e != nil {
		state := "error"
		message := "Task failed"
		if ctx.Err() != nil {
			state = "cancelled"
			message = "Cancelled; completed pages retained"
			if m.ctx.Err() != nil && j["type"] != "figure-images" {
				state = "queued"
				message = "Paused for server shutdown"
			}
		}
		m.finishRun(ws, run, Object{"state": state, "error": e.Error(), "message": message})
		tr.Journal(ws, e.Error())
	}
}

// An old worker may unwind after a new task has already claimed this paper.
// Its cleanup and error handlers must never cancel or overwrite that new run.
func (m *Manager) finishRun(ws *store.Workspace, run *activeRun, fields Object) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancels[ws.ID()] != run {
		return
	}
	job, err := ws.Load("job")
	if err != nil || job["state"] != "running" {
		return
	}
	if err := m.write(ws, fields); err != nil {
		tr.Journal(ws, "Cannot save final task status: "+err.Error())
	}
}

func (m *Manager) run(ctx context.Context, ws *store.Workspace, j Object) error {
	cfg, e := m.cfg.Load()
	if e != nil {
		return e
	}
	if j["type"] == "figure-images" {
		return m.runFigures(ctx, ws, j, cfg)
	}
	if model := tr.Str(j["model"]); model != "" {
		if selected, _, err := engines.EngineConfig(cfg, model); err == nil {
			cfg = selected
		}
	}
	cfg = copyMap(cfg)
	cfg["translation_extra"] = j["translation_extra"]
	cfg["target"] = j["target"]
	p, e := ws.Load("paper")
	if e != nil {
		return e
	}
	if len(tr.List(tr.Obj(p["meta"])["pages"])) == 0 {
		_ = m.write(ws, Object{"message": "Rendering PDF and extracting text"})
		pages, e := m.preparePDF(ctx, ws)
		if e != nil {
			return e
		}
		if e = ws.Update("paper", func(p Object) error {
			meta := tr.Obj(p["meta"])
			meta["pages"] = pages
			meta["page_count"] = len(pages)
			p["meta"] = meta
			return nil
		}); e != nil {
			return e
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !tr.Bool(j["translate"]) || cfg["engine"] == "none" {
		return m.write(ws, Object{"state": "done", "message": "Imported"})
	}
	p, e = ws.Load("paper")
	if e != nil {
		return e
	}
	read := tr.Bool(j["read"])
	var pages []int
	planned := j["pages"] != nil
	if planned {
		pages, e = tr.ParsePages(j["pages"])
	} else {
		pages, e = tr.ScopePages(ws, tr.Str(j["scope"]))
	}
	if e != nil {
		return e
	}
	total := tr.Num(tr.Obj(p["meta"])["page_count"])
	for _, n := range pages {
		if n > total {
			return fmt.Errorf("page %d exceeds document length %d", n, total)
		}
	}
	if !planned || tr.Bool(j["cap_check"]) {
		tl := tr.Obj(p["translation"])
		done, en := pageInts(tl["done_pages"]), pageInts(tl["en_pages"])
		keep := []int{}
		for _, n := range pages {
			if !has(done, n) || (!read && has(en, n)) {
				keep = append(keep, n)
			}
		}
		pages = keep
	}
	if len(pages) == 0 {
		return m.write(ws, Object{"state": "done", "message": "Selected pages already complete", "done": 0, "total": 0})
	}
	cap := 60
	switch v := cfg["page_cap"].(type) {
	case int:
		if v >= 0 {
			cap = v
		}
	case float64:
		if v >= 0 && v == float64(int(v)) {
			cap = int(v)
		}
	}
	scope := tr.Str(j["scope"])
	check := tr.Bool(j["cap_check"]) || (!planned && (scope == "all" || scope == "body" || scope == ""))
	if cap > 0 && len(pages) > cap && check && j["confirmed"] != true {
		m.mu.Lock()
		defer m.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return m.write(ws, Object{"state": "confirm", "total": len(pages), "page_cap": cap, "pages": pages, "cap_check": true, "message": fmt.Sprintf("%d pages exceed your %d-page limit; confirm before starting", len(pages), cap)})
	}
	usage := Object{}
	var writeErr error
	failed, e := tr.Pages(ctx, ws, cfg, pages, read, func(done, total int, msg string, u Object) {
		if u != nil {
			mergeUsage(usage, u)
		}
		if err := m.write(ws, Object{"done": done, "total": total, "message": msg, "usage": copyMap(usage)}); err != nil && writeErr == nil {
			writeErr = err
		}
	})
	_ = ws.Update("job", func(j Object) error {
		all := tr.Obj(j["usage_total"])
		mergeUsage(all, usage)
		j["usage_total"] = all
		return nil
	})
	if e != nil {
		return e
	}
	if writeErr != nil {
		return writeErr
	}
	_ = pdf.RefreshLayout(ctx, ws)
	_ = tr.ReorderFloats(ws)
	if !read && tr.Bool(tr.Obj(cfg["figure_images"])["enabled"]) && cfg["translate_figure_text"] != false && ctx.Err() == nil {
		paper, loadErr := ws.Load("paper")
		if loadErr == nil {
			ids := []string{}
			for _, raw := range tr.List(paper["blocks"]) {
				b := tr.Obj(raw)
				n := tr.Num(b["page"])
				if b["type"] == "figure" && has(pages, n) && failed[strconv.Itoa(n)] == nil && tr.Str(b["src"]) != "" && tr.Str(b["image_zh"]) != "" {
					ids = append(ids, tr.Str(b["id"]))
				}
			}
			if len(ids) > 0 {
				result, imageErr := tr.GenerateFigures(ctx, ws, cfg, ids, false, func(done, total int, msg string) { _ = m.write(ws, Object{"message": msg}) })
				if imageErr != nil {
					tr.Journal(ws, "Optional translated images: "+imageErr.Error())
				} else {
					_ = m.write(ws, Object{"image_failed": result["failed"]})
				}
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	state, msg, first := "done", "Translation complete", ""
	if read {
		msg = "Source text organized"
	}
	if len(failed) > 0 {
		state = "partial"
		msg = fmt.Sprintf("%d pages failed; completed pages retained", len(failed))
		for _, v := range failed {
			first = tr.Str(v)
			break
		}
	}
	return m.write(ws, Object{"state": state, "failed": failed, "error": first, "message": msg, "done": len(pages), "total": len(pages)})
}
func mergeUsage(dst, src Object) {
	for k, v := range src {
		switch x := v.(type) {
		case int:
			dst[k] = tr.Num(dst[k]) + x
		case float64:
			old, _ := dst[k].(float64)
			if old == 0 {
				old = float64(tr.Num(dst[k]))
			}
			dst[k] = old + x
		case map[string]any:
			n := tr.Obj(dst[k])
			mergeUsage(n, x)
			dst[k] = n
		default:
			if dst[k] == nil {
				dst[k] = v
			}
		}
	}
}
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.bulk) > 0 || len(m.small) > 0 || len(m.cancels) > 0 {
		return true
	}
	for _, j := range m.recent {
		if j["state"] == "queued" || j["state"] == "running" {
			return true
		}
	}
	return false
}
func (m *Manager) Close() {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		m.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}
func id() string { b := make([]byte, 12); _, _ = rand.Read(b); return "j" + hex.EncodeToString(b) }
func (m *Manager) saveSmall() error {
	return store.WriteJSON(filepath.Join(m.lib.Root, "small-jobs.json"), Object{"jobs": m.recent})
}
func (m *Manager) Small(kind, pid string, body Object) (Object, error) {
	if kind != "answer" && kind != "retranslate" {
		return nil, errors.New("unknown small task")
	}
	ws, e := m.lib.Workspace(pid)
	if e != nil || ws == nil {
		return nil, errors.New("paper not found")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("job manager closed")
	}
	j := copyMap(body)
	for k, v := range (Object{"id": id(), "kind": kind, "pid": pid, "state": "queued", "message": "Queued", "at": stamp()}) {
		j[k] = v
	}
	m.recent = append([]Object{j}, m.recent...)
	if e = m.saveSmall(); e != nil {
		m.recent = m.recent[1:]
		return nil, e
	}
	m.small = append(m.small, tr.Str(j["id"]))
	signal(m.smallWake)
	return copyMap(j), nil
}
func (m *Manager) Status(pid string) []Object {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Object{}
	for _, j := range m.recent {
		if pid == "" || j["pid"] == pid {
			out = append(out, copyMap(j))
			if len(out) >= 50 {
				break
			}
		}
	}
	return out
}
func (m *Manager) processSmall(id string) {
	m.mu.Lock()
	var j Object
	for _, v := range m.recent {
		if v["id"] == id {
			j = v
			break
		}
	}
	if j == nil {
		m.mu.Unlock()
		return
	}
	j["state"] = "running"
	j["message"] = "Model thinking"
	_ = m.saveSmall()
	work := copyMap(j)
	m.mu.Unlock()
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("small task failed: %v", r)
			}
		}()
		ws, e := m.lib.Workspace(tr.Str(work["pid"]))
		if e != nil || ws == nil {
			err = errors.New("paper removed")
			return
		}
		cfg, e := m.cfg.Load()
		if e != nil {
			err = e
			return
		}
		if work["kind"] == "answer" {
			err = tr.Answer(m.ctx, ws, cfg, tr.Str(work["note"]))
		} else {
			err = tr.Retranslate(m.ctx, ws, cfg, tr.Str(work["key"]), tr.Str(work["hint"]))
		}
	}()
	m.mu.Lock()
	defer m.mu.Unlock()
	j["state"] = "done"
	j["message"] = "Complete"
	if err != nil {
		j["state"] = "error"
		j["message"] = err.Error()
	}
	_ = m.saveSmall()
}

// FigureImages queues explicit image generation without blocking text-only tasks.
func (m *Manager) FigureImages(ws *store.Workspace, body Object) (Object, error) {
	cfg, e := m.cfg.Load()
	if e != nil {
		return nil, e
	}
	if !tr.Bool(tr.Obj(cfg["figure_images"])["enabled"]) {
		return nil, errors.New("enable translated figure generation in settings first")
	}
	ids, e := tr.SelectFigures(ws, body)
	if e != nil {
		return nil, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("job manager closed")
	}
	j, e := ws.Load("job")
	if e != nil {
		return nil, e
	}
	if j["state"] == "queued" || j["state"] == "running" {
		return nil, errors.New("paper already has a queued or running task")
	}
	e = m.write(ws, Object{"type": "figure-images", "state": "queued", "message": "Images queued", "figure_ids": ids, "force": tr.Bool(body["force"]), "translate": true, "read": false, "done": 0, "total": len(ids), "error": "", "failed": Object{}, "image_failed": Object{}, "usage": Object{}})
	if e != nil {
		return nil, e
	}
	m.bulk = append(m.bulk, ws.ID())
	signal(m.wake)
	return Object{"ok": true, "count": len(ids)}, nil
}
func (m *Manager) runFigures(ctx context.Context, ws *store.Workspace, j, cfg Object) error {
	ids := []string{}
	for _, x := range tr.List(j["figure_ids"]) {
		ids = append(ids, tr.Str(x))
	}
	result, e := tr.GenerateFigures(ctx, ws, cfg, ids, tr.Bool(j["force"]), func(done, total int, msg string) {
		_ = m.write(ws, Object{"done": done, "total": total, "message": msg})
	})
	if e != nil {
		return e
	}
	failed := tr.Obj(result["failed"])
	state := "done"
	if len(failed) > 0 {
		state = "partial"
	}
	return m.write(ws, Object{"state": state, "message": fmt.Sprintf("Generated %d images; reused %d; failed %d", tr.Num(result["generated"]), tr.Num(result["skipped"]), len(failed)), "image_failed": failed, "done": len(ids), "total": len(ids)})
}
