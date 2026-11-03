package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/hyskr/easyread/backend/config"
	"github.com/hyskr/easyread/backend/store"
	"github.com/hyskr/easyread/backend/translate"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

func readAnyInput(p string) (any, error) {
	if p == "" {
		return nil, fmt.Errorf("--from FILE is required")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	var v any
	if e = json.Unmarshal(b, &v); e != nil {
		return nil, e
	}
	return v, nil
}
func printStatus(ws *store.Workspace) error {
	parts := map[string]M{}
	for _, n := range []string{"paper", "reader", "discussion", "job"} {
		v, e := ws.Load(n)
		if e != nil {
			return e
		}
		parts[n] = v
	}
	paper, r, disc, job := parts["paper"], parts["reader"], parts["discussion"], parts["job"]
	meta := translate.Obj(paper["meta"])
	title := translate.Str(meta["title_zh"])
	if title == "" {
		title = translate.Str(meta["title_en"])
	}
	tr := translate.Obj(paper["translation"])
	fmt.Printf("# %s  (%s)\n目录：%s\n译文范围：%v；已完成页 %v\n", title, ws.ID(), ws.Root, tr["scope"], tr["done_pages"])
	if len(job) > 0 {
		fmt.Printf("后台任务：%s %s %s\n", translate.Str(job["state"]), translate.Str(job["message"]), translate.Str(job["error"]))
	}
	blocks := map[string]M{}
	for _, v := range translate.List(paper["blocks"]) {
		b := translate.Obj(v)
		blocks[translate.Str(b["id"])] = b
	}
	edits := translate.Obj(r["edits"])
	keys := []string{}
	for k, v := range edits {
		if translate.Obj(v)["zh"] != nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	fmt.Printf("\n## 用户改过的译文（%d）\n", len(keys))
	for _, key := range keys {
		edit := translate.Obj(edits[key])
		fields := strings.SplitN(key, "#", 2)
		b := blocks[fields[0]]
		field := ""
		if len(fields) > 1 {
			field = fields[1]
		}
		agent := translate.Str(b["zh"])
		switch field {
		case "caption":
			agent = translate.Str(b["caption_zh"])
		case "image":
			agent = translate.Str(b["image_zh"])
		default:
			if n, e := strconv.Atoi(field); e == nil {
				items := translate.List(b["items"])
				if n >= 0 && n < len(items) {
					agent = translate.Str(translate.Obj(items[n])["zh"])
				} else {
					agent = ""
				}
			}
		}
		stale := ""
		if base := translate.Str(edit["base"]); base != "" && base != store.TextHash(agent) {
			stale = " [译者稿在用户修改后又变过]"
		}
		fmt.Printf("- %s%s\n  用户：%s\n  译者：%s\n", key, stale, translate.Str(edit["zh"]), agent)
	}
	notes := []M{}
	for _, v := range translate.Obj(r["notes"]) {
		n := translate.Obj(v)
		if n["deleted"] != true {
			notes = append(notes, n)
		}
	}
	sort.SliceStable(notes, func(i, j int) bool { return translate.Str(notes[i]["created"]) < translate.Str(notes[j]["created"]) })
	replied := map[string]bool{}
	for _, v := range translate.List(disc["entries"]) {
		replied[translate.Str(translate.Obj(v)["reply_to"])] = true
	}
	fmt.Printf("\n## 用户笔记与划线（%d）\n", len(notes))
	for _, n := range notes {
		flag := ""
		if n["kind"] == "question" {
			flag = " [待回答]"
			if replied[translate.Str(n["id"])] {
				flag = " [已回复]"
			}
		}
		quote := ""
		if q := translate.Str(n["quote"]); q != "" {
			quote = "「" + q + "」"
		}
		fmt.Printf("- %s · %s%s · 块 %s %s\n  %s\n", translate.Str(n["id"]), translate.Str(n["kind"]), flag, translate.Str(n["anchor"]), quote, strings.TrimSpace(translate.Str(n["body"])))
	}
	if body := translate.Str(translate.Obj(r["paper_note"])["body"]); body != "" {
		fmt.Printf("\n## 论文笔记\n%s\n", body)
	}
	fmt.Printf("\n## 讨论条目：%d\n", len(translate.List(disc["entries"])))
	return nil
}
func discuss(ws *store.Workspace, source, del string) error {
	if del != "" {
		count := 0
		e := ws.Update("discussion", func(d M) error {
			kept := []any{}
			for _, v := range translate.List(d["entries"]) {
				if translate.Str(translate.Obj(v)["id"]) == del {
					count++
				} else {
					kept = append(kept, v)
				}
			}
			d["entries"] = kept
			return nil
		})
		if e != nil {
			return e
		}
		fmt.Printf("删除 %d 条\n", count)
		return nil
	}
	raw, e := readAnyInput(source)
	if e != nil {
		return e
	}
	items := []any{}
	switch v := raw.(type) {
	case []any:
		items = v
	case map[string]any:
		if entries, ok := v["entries"].([]any); ok {
			items = entries
		} else {
			items = []any{v}
		}
	default:
		return fmt.Errorf("discussion input must be an object or array")
	}
	p, e := ws.Load("paper")
	if e != nil {
		return e
	}
	r, e := ws.Load("reader")
	if e != nil {
		return e
	}
	blocks := map[string]bool{}
	for _, v := range translate.List(p["blocks"]) {
		blocks[translate.Str(translate.Obj(v)["id"])] = true
	}
	for _, v := range items {
		it, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("discussion entry must be an object")
		}
		kind := translate.Str(it["kind"])
		if kind == "" {
			kind = "explain"
		}
		if !map[string]bool{"explain": true, "qa": true, "insight": true, "reply": true, "check": true}[kind] {
			return fmt.Errorf("invalid discussion kind")
		}
		if id := translate.Str(it["anchor"]); id != "" && !blocks[id] {
			return fmt.Errorf("anchor block does not exist: %s", id)
		}
		if id := translate.Str(it["reply_to"]); id != "" && translate.Obj(r["notes"])[id] == nil {
			return fmt.Errorf("user note does not exist: %s", id)
		}
		if strings.TrimSpace(translate.Str(it["body"])) == "" {
			return fmt.Errorf("body must not be empty")
		}
	}
	added, updated := 0, 0
	e = ws.Update("discussion", func(d M) error {
		entries := translate.List(d["entries"])
		byID := map[string]M{}
		for _, v := range entries {
			it := translate.Obj(v)
			byID[translate.Str(it["id"])] = it
		}
		stamp := store.Now()
		for i, v := range items {
			it := translate.Obj(v)
			if it["kind"] == nil {
				it["kind"] = "explain"
			}
			id := translate.Str(it["id"])
			if current, ok := byID[id]; ok && id != "" {
				for k, x := range it {
					current[k] = x
				}
				current["updated"] = stamp
				updated++
			} else {
				if id == "" {
					hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s%d", stamp, i))))
					id = fmt.Sprintf("d%03d-%s", len(entries)+1, hash[:5])
					it["id"] = id
				}
				it["at"] = stamp
				entries = append(entries, it)
				byID[id] = it
				added++
			}
		}
		d["entries"] = entries
		return nil
	})
	if e != nil {
		return e
	}
	fmt.Printf("讨论：新增 %d 条，更新 %d 条。页面几秒内自动出现。\n", added, updated)
	return nil
}

var cliHTTP = &http.Client{Timeout: 1500 * time.Millisecond, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}

func runningServer(cfg *config.Manager) string {
	if os.Getenv("EASYREAD_LIBRARY") != "" || os.Getenv("COREAD_LIBRARY") != "" {
		return ""
	}
	v, e := store.ReadJSON(filepath.Join(cfg.Home, ".server.json"))
	if e != nil {
		return ""
	}
	raw := translate.Str(v["url"])
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return ""
	}
	if host := u.Hostname(); host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return ""
	}
	resp, e := cliHTTP.Get(raw + "/api/config")
	if e != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	var reply M
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&reply); e != nil {
		return ""
	}
	if _, ok := reply["config"].(map[string]any); !ok {
		return ""
	}
	return raw
}
func serverPost(root, path string, data M) error {
	resp, e := cliHTTP.Get(root + "/api/library")
	if e != nil {
		return e
	}
	var library M
	e = json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&library)
	resp.Body.Close()
	if e != nil {
		return e
	}
	token := translate.Str(library["token"])
	if token == "" {
		return fmt.Errorf("server did not provide authentication token")
	}
	payload, e := json.Marshal(data)
	if e != nil {
		return e
	}
	req, e := http.NewRequest("POST", root+path, bytes.NewReader(payload))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Token", token)
	resp, e = cliHTTP.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("server HTTP %d: %s", resp.StatusCode, b)
	}
	return nil
}
func keepServer(root string) error { return serverPost(root, "/api/presence/keep", M{}) }
func queueTranslation(root, id string, data M) error {
	return serverPost(root, "/api/p/"+id+"/translate", data)
}
func migrateFolder(lib *store.Library, source string) (*store.Workspace, error) {
	src, e := filepath.Abs(source)
	if e != nil {
		return nil, e
	}
	st, e := os.Lstat(src)
	if e != nil {
		return nil, e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("migration source must be a regular directory")
	}
	rel, e := filepath.Rel(src, lib.Root)
	if e != nil {
		return nil, e
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return nil, fmt.Errorf("source folder must not contain destination library")
	}
	pdfInfo, e := os.Lstat(filepath.Join(src, "source.pdf"))
	if e != nil {
		return nil, e
	}
	if !pdfInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("source PDF must be a regular file")
	}
	data, e := os.ReadFile(filepath.Join(src, "source.pdf"))
	if e != nil {
		return nil, e
	}
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		return nil, fmt.Errorf("not a PDF file")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	id := digest[:12]
	unlock, e := store.Lock(lib.Root)
	if e != nil {
		return nil, e
	}
	defer unlock()
	dest := filepath.Join(lib.Root, id)
	if _, e = os.Lstat(dest); e == nil {
		return lib.Workspace(id)
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	staging, e := os.MkdirTemp(lib.Root, ".migrate-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(staging)
	before, e := migrationSnapshot(src)
	if e != nil {
		return nil, e
	}
	if e = copyTree(src, staging); e != nil {
		return nil, e
	}
	after, e := migrationSnapshot(src)
	if e != nil {
		return nil, e
	}
	if !reflect.DeepEqual(before, after) {
		return nil, fmt.Errorf("source changed during migration")
	}
	w := store.NewWorkspace(staging)
	if p, e := w.Load("paper"); e != nil || p == nil {
		if e != nil {
			return nil, e
		}
		return nil, fmt.Errorf("source folder is missing paper.json")
	}
	if !fileExists(filepath.Join(staging, "item.json")) {
		if e = w.Write("item", M{"added": store.Now(), "tags": []any{}, "status": "reading", "starred": false}); e != nil {
			return nil, e
		}
	}
	if e = w.Update("reader", func(r M) error {
		if r["paper_note"] == nil {
			r["paper_note"] = M{}
		}
		return nil
	}); e != nil {
		return nil, e
	}
	if e = os.Rename(staging, dest); e != nil {
		return nil, e
	}
	return store.NewWorkspace(dest), nil
}
func fileExists(p string) bool { _, e := os.Lstat(p); return e == nil }

func migrationSnapshot(root string) (map[string]string, error) {
	out := map[string]string{}
	e := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, p)
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
		if d.IsDir() {
			out[rel] = "directory"
			return nil
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("refusing special file %s", p)
		}
		data, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		out[rel] = fmt.Sprintf("%d/%d/%x", st.Size(), st.ModTime().UnixNano(), sha256.Sum256(data))
		return nil
	})
	return out, e
}
