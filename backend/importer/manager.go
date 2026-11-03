package importer

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/hyskr/easyread/backend/config"
	"github.com/hyskr/easyread/backend/store"
	"path/filepath"
	"strings"
	"sync"
)

type Manager struct {
	lib        *store.Library
	cfg        *config.Manager
	mu         sync.Mutex
	status     M
	cancel     context.CancelFunc
	generation uint64
	closed     bool
	wg         sync.WaitGroup
	OnImported func(*store.Workspace) error
}

func New(lib *store.Library, cfg *config.Manager) *Manager {
	return &Manager{lib: lib, cfg: cfg, status: M{"state": "idle"}}
}
func copyMap(m M) M          { b, _ := json.Marshal(m); var n M; json.Unmarshal(b, &n); return n }
func (m *Manager) Status() M { m.mu.Lock(); defer m.mu.Unlock(); return copyMap(m.status) }
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status["state"] == "running"
}
func (m *Manager) Candidates() []M { return Candidates() }
func read(body M) (snapshot, error) {
	source := first(str(body["source"]), "zotero")
	switch source {
	case "folder":
		return folder(str(body["path"]))
	case "zotero":
		return zotero(str(body["path"]), str(body["base"]))
	}
	return snapshot{}, errors.New("unknown import source")
}
func entryTitle(e entry) string {
	if e.PDF != "" {
		return first(str(e.Meta["title_en"]), filepath.Base(e.PDF), e.Key)
	}
	return first(str(e.Meta["title_en"]), e.Key)
}
func (m *Manager) Scan(body M) (M, error) {
	s, e := read(body)
	if e != nil {
		return nil, e
	}
	missing := []any{}
	noPDF, fetchable := 0, 0
	tags := map[string]bool{}
	for _, en := range s.Entries {
		for _, t := range en.Tags {
			tags[t] = true
		}
		if en.PDF == "" {
			noPDF++
			if str(en.Meta["doi"]) != "" || str(en.Meta["arxiv"]) != "" {
				fetchable++
			}
			if len(missing) < 50 {
				missing = append(missing, M{"title": entryTitle(en), "why": en.Missing})
			}
		}
	}
	return M{"source": first(str(body["source"]), "zotero"), "path": s.Path, "from_backup": s.Backup, "total": len(s.Entries), "with_pdf": len(s.Entries) - noPDF, "no_pdf": noPDF, "fetchable": fetchable, "collections": len(s.Collections), "tags": len(tags), "missing": missing}, nil
}
func (m *Manager) Start(body M) (M, error) {
	if body["link"] == true && body["fetch"] == true {
		return nil, errors.New("linked imports support local PDFs only")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("import manager is closed")
	}
	if m.status["state"] == "running" {
		m.mu.Unlock()
		return nil, errors.New("an import is already running")
	}
	s, e := read(body)
	if e != nil {
		m.mu.Unlock()
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.generation++
	generation := m.generation
	m.status = M{"state": "running", "source": first(str(body["source"]), "zotero"), "link": body["link"] == true, "done": 0, "total": len(s.Entries), "imported": 0, "existing": 0, "skipped": []any{}, "failed": []any{}, "message": "正在迁移"}
	result := copyMap(m.status)
	m.wg.Add(1)
	m.mu.Unlock()
	go m.run(ctx, s, body, generation)
	return result, nil
}
func (m *Manager) Stop() M {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	r := copyMap(m.status)
	m.mu.Unlock()
	return r
}
func (m *Manager) set(k string, v any) { m.mu.Lock(); m.status[k] = v; m.mu.Unlock() }
func (m *Manager) note(k string, e entry, why string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := arr(m.status[k])
	if len(a) < 200 {
		m.status[k] = append(a, M{"title": entryTitle(e), "why": why})
	}
}
func category(s string) string {
	parts := []string{}
	for _, p := range strings.Split(s, "/") {
		p = strings.TrimSpace(p)
		r := []rune(p)
		if len(r) > 40 {
			p = string(r[:40])
		}
		if p != "" {
			parts = append(parts, p)
		}
	}
	p := strings.Join(parts, "/")
	r := []rune(p)
	if len(r) > 120 {
		p = string(r[:120])
	}
	return p
}
func unique(existing any, want []string) []any {
	a := arr(existing)
	seen := map[string]bool{}
	for _, v := range a {
		seen[str(v)] = true
	}
	for _, s := range want {
		if s != "" && !seen[s] {
			seen[s] = true
			a = append(a, s)
		}
	}
	return a
}
func (m *Manager) run(ctx context.Context, s snapshot, body M, generation uint64) {
	defer m.wg.Done()
	defer func() {
		m.mu.Lock()
		if m.generation == generation && m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		m.mu.Unlock()
	}()
	cats := []string{}
	for _, c := range s.Collections {
		cats = append(cats, category(c))
	}
	for n, en := range s.Entries {
		if ctx.Err() != nil {
			break
		}
		m.set("done", n)
		m.set("message", entryTitle(en))
		var ws *store.Workspace
		var fresh bool
		var e error
		if en.PDF != "" {
			ws, fresh, e = m.lib.ImportPath(en.PDF, en.Meta, body["link"] == true)
		} else if body["fetch"] == true && first(str(en.Meta["arxiv"]), str(en.Meta["doi"])) != "" {
			var raw []byte
			var filename string
			var meta M
			raw, filename, meta, e = Fetch(ctx, first(str(en.Meta["arxiv"]), str(en.Meta["doi"])))
			if e == nil {
				for k, v := range en.Meta {
					meta[k] = v
				}
				ws, fresh, e = m.lib.ImportPDF(raw, filename, meta)
			}
		} else {
			m.note("skipped", en, en.Missing)
			m.set("done", n+1)
			continue
		}
		if e != nil {
			m.note("failed", en, e.Error())
			m.set("done", n+1)
			continue
		}
		want := []string{}
		for _, c := range en.Cats {
			want = append(want, category(c))
		}
		if body["tags"] != false {
			for _, t := range en.Tags {
				want = append(want, category("Zotero 标签/"+strings.ReplaceAll(t, "/", "／")))
			}
		}
		cats = append(cats, want...)
		e = ws.Update("item", func(item M) error {
			item["tags"] = unique(item["tags"], want)
			if fresh && en.Added != "" {
				item["added"] = en.Added
			}
			return nil
		})
		if e != nil {
			m.note("failed", en, e.Error())
			continue
		}
		paper, _ := ws.Load("paper")
		job, _ := ws.Load("job")
		active := job["state"] == "queued" || job["state"] == "running"
		if m.OnImported != nil && body["link"] != true && len(arr(obj(paper["meta"])["pages"])) == 0 && !active {
			if e = m.OnImported(ws); e != nil {
				m.note("failed", en, "Imported, but page preparation failed: "+e.Error())
			}
		}
		k := "existing"
		if fresh {
			k = "imported"
		}
		m.mu.Lock()
		count, _ := m.status[k].(int)
		m.status[k] = count + 1
		m.status["done"] = n + 1
		m.mu.Unlock()
	}
	if m.cfg != nil {
		prefs, e := m.cfg.Prefs()
		if e == nil {
			_, e = m.cfg.SavePrefs(M{"library": M{"cats": unique(obj(prefs["library"])["cats"], cats)}})
		}
		if e != nil {
			m.set("state", "error")
			m.set("message", e.Error())
			return
		}
	}
	if ctx.Err() != nil {
		m.set("state", "stopped")
		m.set("message", "已停止")
	} else {
		m.set("state", "done")
		m.set("done", len(s.Entries))
		m.set("message", "迁移完成")
	}
}

// Close cancels active imports and waits until their writes are complete.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}
