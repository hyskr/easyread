package store

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Library struct{ Root string }

func NewLibrary(root string) (*Library, error) {
	p, e := filepath.Abs(expand(root))
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(p, 0755); e != nil {
		return nil, e
	}
	p, e = filepath.EvalSymlinks(p)
	if e != nil {
		return nil, e
	}
	return &Library{Root: p}, nil
}

var idRE = regexp.MustCompile(`^[A-Za-z0-9_-]{4,64}$`)

func (l *Library) Workspace(id string) (*Workspace, error) {
	if !idRE.MatchString(id) {
		return nil, os.ErrNotExist
	}
	p := filepath.Join(l.Root, id)
	if _, e := os.Stat(filepath.Join(p, "paper.json")); e != nil {
		return nil, e
	}
	resolved, e := filepath.EvalSymlinks(p)
	if e != nil {
		return nil, e
	}
	rel, e := filepath.Rel(l.Root, resolved)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("workspace escapes library")
	}
	return NewWorkspace(p), nil
}
func (l *Library) All() ([]*Workspace, error) {
	entries, e := os.ReadDir(l.Root)
	if e != nil {
		return nil, e
	}
	out := []*Workspace{}
	for _, en := range entries {
		if en.IsDir() && !strings.HasPrefix(en.Name(), ".") {
			w, e := l.Workspace(en.Name())
			if e == nil {
				out = append(out, w)
			}
		}
	}
	return out, nil
}
func (l *Library) ImportPDF(data []byte, filename string, meta Object) (*Workspace, bool, error) {
	if !strings.HasPrefix(string(data), "%PDF") {
		return nil, false, fmt.Errorf("not a PDF file")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	return l.create(digest, filename, meta, data, "")
}
func (l *Library) ImportPath(path string, meta Object, link bool) (*Workspace, bool, error) {
	if regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`).MatchString(path) {
		return nil, false, fmt.Errorf("only local PDF files supported")
	}
	p, e := filepath.Abs(expand(path))
	if e != nil {
		return nil, false, e
	}
	p, e = filepath.EvalSymlinks(p)
	if e != nil {
		return nil, false, e
	}
	if link {
		rel, e := filepath.Rel(l.Root, p)
		if e != nil {
			return nil, false, e
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, false, fmt.Errorf("linked PDF must be outside library")
		}
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, false, e
	}
	if !strings.HasPrefix(string(b), "%PDF") {
		return nil, false, fmt.Errorf("not a PDF file")
	}
	if !link {
		return l.ImportPDF(b, filepath.Base(p), meta)
	}
	return l.create(fmt.Sprintf("%x", sha256.Sum256(b)), filepath.Base(p), meta, nil, p)
}
func (l *Library) create(digest, filename string, meta Object, data []byte, linked string) (*Workspace, bool, error) {
	unlock, e := Lock(l.Root)
	if e != nil {
		return nil, false, e
	}
	defer unlock()
	id := digest[:12]
	if ws, e := l.Workspace(id); e == nil {
		return ws, false, nil
	}
	root := filepath.Join(l.Root, id)
	if e = os.MkdirAll(root, 0755); e != nil {
		return nil, false, e
	}
	w := NewWorkspace(root)
	if linked == "" {
		if e = os.WriteFile(filepath.Join(root, "source.pdf"), data, 0600); e != nil {
			return nil, false, e
		}
	}
	m := Object{"title_zh": "", "title_en": "", "authors": "", "source": filename}
	for k, v := range meta {
		m[k] = v
	}
	m["source_sha256"] = digest
	m["pdf"] = "source.pdf"
	delete(m, "source_pdf_path")
	if linked != "" {
		m["source_pdf_path"] = linked
	}
	if !truth(m["title_en"]) {
		m["title_en"] = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}
	files := map[string]Object{"paper": {"schema": Schema, "meta": m, "translation": Object{"scope": "未开始", "done_pages": []any{}, "note": ""}, "glossary": []any{}, "references": []any{}, "blocks": []any{}}, "discussion": EmptyDiscussion(), "reader": EmptyReader(), "item": {"added": Now(), "tags": []any{}, "status": "unread", "starred": false}} // Publish paper last so partially initialized workspaces are never listed.
	for _, name := range []string{"reader", "discussion", "item", "paper"} {
		if e = WriteJSON(filepath.Join(root, name+".json"), files[name]); e != nil {
			return nil, false, e
		}
	}
	return w, true, nil
}
func (l *Library) List() ([]Object, error) {
	ws, e := l.All()
	if e != nil {
		return nil, e
	}
	items := []Object{}
	for _, w := range ws {
		item, e := l.Summary(w)
		if e != nil {
			item = Object{"id": w.ID(), "title_en": w.ID(), "tags": []any{}, "status": "unread", "load_error": e.Error()}
		}
		items = append(items, item)
	}
	return items, nil
}
func (l *Library) Summary(w *Workspace) (Object, error) {
	parts := map[string]Object{}
	for _, name := range []string{"paper", "item", "reader", "discussion", "job"} {
		v, e := w.Load(name)
		if e != nil {
			return nil, e
		}
		parts[name] = v
	}
	p := parts["paper"]
	item := parts["item"]
	r := parts["reader"]
	d := parts["discussion"]
	j := parts["job"]
	meta := clone(obj(p["meta"]))
	for k, v := range obj(item["meta_override"]) {
		if truth(v) || k == "doi" {
			meta[k] = v
		}
	}
	tr := obj(p["translation"])
	if len(array(tr["done_pages"])) > 0 && !truth(tr["gaps_checked"]) {
		if _, err := w.RepairDone(); err != nil {
			return nil, err
		}
		refreshed, err := w.Load("paper")
		if err != nil {
			return nil, err
		}
		tr = obj(refreshed["translation"])
	}
	out := Object{"id": w.ID()}
	for _, k := range []string{"title_zh", "title_en", "short_zh", "target", "authors", "affiliation", "date", "venue", "arxiv", "doi"} {
		out[k] = get(meta, k, "")
	}
	year := meta["year"]
	if !truth(year) {
		year = regexp.MustCompile(`(?:19|20)\d{2}`).FindString(str(meta["date"]))
	}
	out["year"] = year
	out["url"] = sourceLink(meta)
	out["pages"] = get(meta, "page_count", 0)
	out["done_pages"] = len(array(tr["done_pages"]))
	out["en_pages"] = len(array(tr["en_pages"]))
	out["todo"] = todo(number(meta["page_count"]), tr)
	abstract := ""
	for _, b := range array(p["blocks"]) {
		m := obj(b)
		if m["role"] == "abstract" {
			abstract = str(m["zh"])
			if abstract == "" {
				abstract = str(m["en"])
			}
			break
		}
	}
	if abstract == "" {
		abstract = str(meta["abstract_en"])
	}
	out["abstract"] = abstract
	out["meta_override"] = obj(item["meta_override"])
	out["tags"] = get(item, "tags", []any{})
	out["status"] = get(item, "status", "unread")
	out["starred"] = truth(item["starred"])
	out["added"] = get(item, "added", "")
	out["last_opened"] = get(item, "last_opened", "")
	out["progress"] = get(obj(r["progress"]), "ratio", 0)
	replied := map[string]bool{}
	entries := array(d["entries"])
	for _, v := range entries {
		if id := str(obj(v)["reply_to"]); id != "" {
			replied[id] = true
		}
	}
	notes, highlights, questions := 0, 0, 0
	for _, v := range obj(r["notes"]) {
		n := obj(v)
		if truth(n["deleted"]) {
			continue
		}
		if n["kind"] == "highlight" {
			highlights++
		} else {
			notes++
		}
		if n["kind"] == "question" && !replied[str(n["id"])] {
			questions++
		}
	}
	out["notes"] = notes
	out["highlights"] = highlights
	out["open_questions"] = questions
	out["discussions"] = len(entries)
	out["has_paper_note"] = truth(obj(r["paper_note"])["body"])
	out["job"] = nil
	if len(j) > 0 {
		summary := Object{}
		for _, k := range []string{"type", "state", "message", "done", "total", "updated", "error", "failed", "scope", "read", "model", "target", "pages", "cap_check", "page_cap", "usage", "usage_total"} {
			summary[k] = j[k]
		}
		out["job"] = summary
	}
	out["thumb"] = ""
	for _, name := range []string{"page-001.webp", "page-001.png", "page-001.jpg"} {
		if _, e := os.Stat(filepath.Join(w.Root, "pages", name)); e == nil {
			out["thumb"] = "/p/" + w.ID() + "/pages/" + name
			break
		}
	}
	return out, nil
}
func todo(total int, tr Object) string {
	done := map[int]bool{}
	for _, v := range array(tr["done_pages"]) {
		done[number(v)] = true
	}
	for _, v := range array(tr["en_pages"]) {
		delete(done, number(v))
	}
	ranges := []string{}
	for n := 1; n <= total; n++ {
		if done[n] {
			continue
		}
		a := n
		for n+1 <= total && !done[n+1] {
			n++
		}
		if a == n {
			ranges = append(ranges, fmt.Sprint(a))
		} else {
			ranges = append(ranges, fmt.Sprintf("%d-%d", a, n))
		}
	}
	return strings.Join(ranges, ",")
}

var arxivRE = regexp.MustCompile(`\d{4}\.\d{4,5}(?:v\d+)?`)

func sourceLink(m Object) string {
	if truth(m["url"]) {
		return str(m["url"])
	}
	source := str(m["arxiv"])
	if source == "" {
		source = str(m["source"])
	}
	id := arxivRE.FindString(source)
	if id != "" && (truth(m["arxiv"]) || source == id+".pdf") {
		id = regexp.MustCompile(`v\d+$`).ReplaceAllString(id, "")
		return "https://arxiv.org/abs/" + id
	}
	if truth(m["doi"]) {
		return "https://doi.org/" + str(m["doi"])
	}
	return ""
}
func (l *Library) Trash(id string) (string, error) {
	unlock, e := Lock(l.Root)
	if e != nil {
		return "", e
	}
	defer unlock()
	w, e := l.Workspace(id)
	if e != nil {
		return "", e
	}
	dir := filepath.Join(l.Root, ".trash")
	if e = os.MkdirAll(dir, 0755); e != nil {
		return "", e
	}
	stamp := time.Now()
	dest := filepath.Join(dir, id+"-"+stamp.Format("20060102150405"))
	for {
		if _, e = os.Lstat(dest); os.IsNotExist(e) {
			break
		}
		stamp = stamp.Add(time.Second)
		dest = filepath.Join(dir, id+"-"+stamp.Format("20060102150405"))
	}
	e = os.Rename(w.Root, dest)
	return dest, e
}

var trashRE = regexp.MustCompile(`^([0-9a-f]{12})-(\d{14})$`)

func trashEntry(root, name string) (string, error) {
	if !trashRE.MatchString(name) {
		return "", fmt.Errorf("invalid trash item")
	}
	p := filepath.Join(root, ".trash", name)
	st, e := os.Lstat(p)
	if e != nil {
		return "", e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("invalid trash item")
	}
	return p, nil
}
func TrashItems(root string) ([]Object, error) {
	entries, e := os.ReadDir(filepath.Join(root, ".trash"))
	out := []Object{}
	if os.IsNotExist(e) {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		matches := trashRE.FindStringSubmatch(entry.Name())
		if matches == nil || !entry.IsDir() {
			continue
		}
		stamp, e := time.Parse("20060102150405", matches[2])
		if e != nil {
			continue
		}
		p, e := ReadJSON(filepath.Join(root, ".trash", entry.Name(), "paper.json"))
		meta := obj(p["meta"])
		if e != nil {
			meta = Object{"title_en": matches[1]}
		}
		out = append(out, Object{"name": entry.Name(), "id": matches[1], "title_en": get(meta, "title_en", ""), "title_zh": get(meta, "title_zh", ""), "authors": get(meta, "authors", ""), "deleted": stamp.Format("2006-01-02T15:04:05")})
	}
	sort.Slice(out, func(i, j int) bool { return str(out[i]["deleted"]) > str(out[j]["deleted"]) })
	return out, nil
}
func Restore(root, name string) (string, error) {
	unlock, e := Lock(root)
	if e != nil {
		return "", e
	}
	defer unlock()
	src, e := trashEntry(root, name)
	if e != nil {
		return "", e
	}
	id := trashRE.FindStringSubmatch(name)[1]
	dest := filepath.Join(root, id)
	if _, e = os.Lstat(dest); !os.IsNotExist(e) {
		return "", fmt.Errorf("paper already exists in library")
	}
	if e = os.Rename(src, dest); e != nil {
		return "", e
	}
	return id, nil
}
func Purge(root, name string) (int, error) {
	unlock, e := Lock(root)
	if e != nil {
		return 0, e
	}
	defer unlock()
	p, e := trashEntry(root, name)
	if e != nil {
		return 0, e
	}
	if e = os.RemoveAll(p); e != nil {
		return 0, e
	}
	return 1, nil
}
func EmptyTrash(root string) (int, error) {
	entries, e := TrashItems(root)
	if e != nil {
		return 0, e
	}
	count := 0
	for _, en := range entries {
		n, e := Purge(root, str(en["name"]))
		count += n
		if e != nil {
			return count, e
		}
	}
	return count, nil
}
