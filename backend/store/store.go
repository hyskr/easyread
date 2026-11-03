// Package store implements EasyRead's interoperable, crash-safe JSON library.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

type Object = map[string]any

const Schema = 2

func Now() string { return time.Now().Format("2006-01-02T15:04:05.000Z07:00") }
func TextHash(s string) string {
	h := uint32(2166136261)
	for _, r := range utf16.Encode([]rune(s)) {
		h ^= uint32(r)
		h *= 16777619
	}
	return fmt.Sprintf("%08x", h)
}
func ReadJSON(path string) (Object, error) {
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var v Object
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("cannot read data file %s; original file preserved: invalid UTF-8", path)
	}
	if e = json.Unmarshal(b, &v); e != nil || v == nil {
		return nil, fmt.Errorf("cannot read data file %s; original file preserved: invalid JSON object", path)
	}
	return v, nil
}
func WriteJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", " ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	f, e := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	for i := 0; i < 20; i++ {
		e = os.Rename(tmp, path)
		if e == nil {
			if d, er := os.Open(filepath.Dir(path)); er == nil {
				_ = d.Sync()
				_ = d.Close()
			}
			return nil
		}
		if !errors.Is(e, os.ErrPermission) {
			return e
		}
		time.Sleep(time.Duration(i+1) * 50 * time.Millisecond)
	}
	return e
}

func EmptyReader() Object {
	return Object{"schema": Schema, "rev": 0, "edits": Object{}, "notes": Object{}, "paper_note": Object{}, "progress": Object{}}
}
func EmptyDiscussion() Object { return Object{"schema": Schema, "entries": []any{}} }
func obj(v any) Object {
	m, _ := v.(map[string]any)
	if m == nil {
		return Object{}
	}
	return m
}
func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func number(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	}
	return 0
}
func array(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	if a, ok := v.([]int); ok {
		b := []any{}
		for _, x := range a {
			b = append(b, x)
		}
		return b
	}
	return nil
}
func truth(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	case int:
		return x != 0
	}
	return v != nil
}
func get(m Object, k string, d any) any {
	if v, ok := m[k]; ok {
		return v
	}
	return d
}
func clone(m Object) Object {
	n := Object{}
	for k, v := range m {
		n[k] = v
	}
	return n
}

type Workspace struct{ Root string }

func NewWorkspace(root string) *Workspace {
	p, e := filepath.Abs(root)
	if e == nil {
		root = p
	}
	return &Workspace{Root: root}
}
func (w *Workspace) ID() string { return filepath.Base(w.Root) }

var partName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (w *Workspace) path(name string) (string, error) {
	if !partName.MatchString(name) {
		return "", fmt.Errorf("invalid workspace part")
	}
	return filepath.Join(w.Root, name+".json"), nil
}
func (w *Workspace) Load(name string) (Object, error) {
	p, e := w.path(name)
	if e != nil {
		return nil, e
	}
	v, e := ReadJSON(p)
	if e != nil || v != nil {
		return v, e
	}
	switch name {
	case "reader":
		return EmptyReader(), nil
	case "discussion":
		return EmptyDiscussion(), nil
	case "chat":
		return Object{"messages": []any{}}, nil
	case "layout", "item", "job":
		return Object{}, nil
	}
	return nil, nil
}
func (w *Workspace) Write(name string, v Object) error {
	p, e := w.path(name)
	if e != nil {
		return e
	}
	unlock, e := Lock(w.Root)
	if e != nil {
		return e
	}
	defer unlock()
	if _, e = w.Load(name); e != nil {
		return e
	}
	return WriteJSON(p, v)
}
func (w *Workspace) Update(name string, fn func(Object) error) error {
	p, e := w.path(name)
	if e != nil {
		return e
	}
	unlock, e := Lock(w.Root)
	if e != nil {
		return e
	}
	defer unlock()
	v, e := w.Load(name)
	if e != nil {
		return e
	}
	if v == nil {
		v = Object{}
	}
	if e = fn(v); e != nil {
		return e
	}
	return WriteJSON(p, v)
}
func (w *Workspace) Versions() map[string]string {
	v := map[string]string{}
	for _, n := range []string{"paper", "discussion", "reader", "layout", "job"} {
		st, e := os.Stat(filepath.Join(w.Root, n+".json"))
		v[n] = "0"
		if e == nil {
			v[n] = fmt.Sprintf("%x-%x", st.ModTime().UnixNano(), st.Size())
		}
	}
	return v
}
func (w *Workspace) SourcePDF() (string, error) {
	p, e := w.Load("paper")
	if e != nil {
		return "", e
	}
	linked := str(obj(p["meta"])["source_pdf_path"])
	if linked == "" {
		return filepath.Join(w.Root, "source.pdf"), nil
	}
	if !filepath.IsAbs(linked) {
		return "", fmt.Errorf("linked PDF must be absolute: %s", linked)
	}
	f, e := os.Open(linked)
	if e != nil {
		return "", fmt.Errorf("linked PDF missing or unreadable: %s", linked)
	}
	defer f.Close()
	b := make([]byte, 4)
	_, e = io.ReadFull(f, b)
	if e != nil || string(b) != "%PDF" {
		return "", fmt.Errorf("linked PDF missing or unreadable: %s", linked)
	}
	return linked, nil
}
func (w *Workspace) PatchItem(fields Object) (Object, error) {
	var result Object
	e := w.Update("item", func(item Object) error {
		for _, k := range []string{"tags", "status", "starred", "rating", "last_opened", "meta_override", "archived"} {
			if v, ok := fields[k]; ok {
				item[k] = v
			}
		}
		item["updated"] = Now()
		result = item
		return nil
	})
	return result, e
}
func ApplyOps(reader Object, ops []Object) ([]string, error) {
	applied := []string{}
	edits := obj(reader["edits"])
	notes := obj(reader["notes"])
	progress := obj(reader["progress"])
	reader["edits"] = edits
	reader["notes"] = notes
	reader["progress"] = progress
	for _, op := range ops {
		at := str(op["at"])
		if at == "" {
			at = Now()
		}
		switch str(op["op"]) {
		case "edit":
			if _, ok := op["block"]; !ok {
				return nil, fmt.Errorf("edit requires block")
			}
			block := str(op["block"])
			cur := obj(edits[block])
			if len(cur) > 0 && at < str(cur["at"]) {
				continue
			}
			if op["zh"] == nil {
				if len(cur) > 0 {
					edits[block] = Object{"reverted": true, "prev": cur["zh"], "at": at}
				}
			} else {
				entry := Object{"zh": op["zh"], "base": get(op, "base", ""), "at": at}
				if truth(cur["zh"]) && !reflect.DeepEqual(cur["zh"], op["zh"]) {
					entry["prev"] = cur["zh"]
				}
				edits[block] = entry
			}
			applied = append(applied, "edit:"+block)
		case "note":
			note := clone(obj(op["note"]))
			if note["id"] == nil {
				return nil, fmt.Errorf("note requires id")
			}
			id := str(note["id"])
			cur := obj(notes[id])
			if len(cur) > 0 && str(note["updated"]) < str(cur["updated"]) {
				continue
			}
			notes[id] = note
			applied = append(applied, "note:"+id)
		case "note_del":
			if op["id"] == nil {
				return nil, fmt.Errorf("note_del requires id")
			}
			id := str(op["id"])
			cur := obj(notes[id])
			if len(cur) == 0 || at >= str(cur["updated"]) {
				cur = clone(cur)
				cur["id"] = get(cur, "id", id)
				cur["deleted"] = true
				cur["updated"] = at
				notes[id] = cur
				applied = append(applied, "note_del:"+id)
			}
		case "paper_note":
			if at >= str(obj(reader["paper_note"])["at"]) {
				reader["paper_note"] = Object{"body": get(op, "body", ""), "at": at}
				applied = append(applied, "paper_note")
			}
		case "progress":
			if at >= str(progress["at"]) {
				progress["block"] = op["block"]
				progress["at"] = at
				delete(progress, "off")
				for _, k := range []string{"ratio", "off"} {
					if op[k] != nil {
						progress[k] = op[k]
					}
				}
			}
			applied = append(applied, "progress")
		}
	}
	if len(applied) > 0 {
		reader["rev"] = number(reader["rev"]) + 1
	}
	return applied, nil
}
func (w *Workspace) ApplyReaderOps(ops []Object, client string) (Object, error) {
	unlock, e := Lock(w.Root)
	if e != nil {
		return nil, e
	}
	defer unlock()
	r, e := w.Load("reader")
	if e != nil {
		return nil, e
	}
	applied, e := ApplyOps(r, ops)
	if e != nil {
		return nil, e
	}
	if len(applied) > 0 {
		hist := filepath.Join(w.Root, "history")
		if e = os.MkdirAll(hist, 0755); e != nil {
			return nil, e
		}
		f, err := os.OpenFile(filepath.Join(hist, "reader.log.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		enc := json.NewEncoder(f)
		enc.SetEscapeHTML(false)
		for _, op := range ops {
			entry := Object{"t": Now(), "client": client}
			for k, v := range op {
				entry[k] = v
			}
			if e = enc.Encode(entry); e != nil {
				break
			}
		}
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
		snaps, _ := filepath.Glob(filepath.Join(hist, "reader-*.json"))
		recent := false
		for _, p := range snaps {
			if st, err := os.Stat(p); err == nil && time.Since(st.ModTime()) < 600*time.Second {
				recent = true
			}
		}
		if !recent {
			b, err := os.ReadFile(filepath.Join(w.Root, "reader.json"))
			if err == nil {
				if e = os.WriteFile(filepath.Join(hist, "reader-"+time.Now().Format("20060102-150405")+".json"), b, 0600); e != nil {
					return nil, e
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		if e = WriteJSON(filepath.Join(w.Root, "reader.json"), r); e != nil {
			return nil, e
		}
	}
	return Object{"rev": r["rev"], "applied": applied}, nil
}
func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	return p
}
