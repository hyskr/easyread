package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hyskr/easyread/backend/store"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const migrationMarker = ".easyread-migration.json"
const stagingPrefix = ".easyread-copy-"

func fullPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		p = filepath.Join(h, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("enter a full folder path")
	}
	p = filepath.Clean(p)
	if resolved, e := filepath.EvalSymlinks(p); e == nil {
		return resolved, nil
	} else if !os.IsNotExist(e) {
		return "", e
	}
	parent := filepath.Dir(p)
	if parent == p {
		return "", fmt.Errorf("path does not exist: %s", p)
	}
	resolved, e := fullPath(parent)
	if e != nil {
		return "", e
	}
	return filepath.Join(resolved, filepath.Base(p)), nil
}
func exists(p string) bool { _, e := os.Lstat(p); return e == nil }
func within(root, p string) bool {
	rel, e := filepath.Rel(root, p)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func noLink(p string) error {
	st, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symbolic links are not supported: %s", p)
	}
	return nil
}
func targetLibrary(p string) (string, error) {
	root, e := fullPath(p)
	if e != nil {
		return "", e
	}
	if strings.EqualFold(filepath.Base(root), "EasyRead") || exists(filepath.Join(root, migrationMarker)) {
		return root, nil
	}
	if st, e := os.Stat(root); e == nil {
		if !st.IsDir() {
			return root, nil
		}
		entries, e := os.ReadDir(root)
		if e != nil {
			return "", e
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), ".") && exists(filepath.Join(root, entry.Name(), "item.json")) {
				return root, nil
			}
		}
	}
	return filepath.Join(root, "EasyRead"), nil
}
func incomplete(root string) bool {
	p := filepath.Join(root, migrationMarker)
	if !exists(p) {
		return false
	}
	if noLink(p) != nil {
		return true
	}
	v, e := store.ReadJSON(p)
	if e != nil {
		return true
	}
	if v["state"] == "ready" {
		return false
	}
	host, _ := os.Hostname()
	return text(v["host"]) == "" || text(v["host"]) == host
}
func paperDirs(root string) ([]string, error) {
	entries, e := os.ReadDir(root)
	if os.IsNotExist(e) {
		return []string{}, nil
	}
	if e != nil {
		return nil, e
	}
	out := []string{}
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), ".") {
			continue
		}
		p := filepath.Join(root, en.Name())
		if e = noLink(p); e != nil {
			return nil, e
		}
		if en.IsDir() && exists(filepath.Join(p, "item.json")) {
			out = append(out, p)
		}
	}
	return out, nil
}

type fileStamp struct {
	Size     int64
	Modified int64
	Hash     string
}

func fileDigest(p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func manifest(root string) (map[string]fileStamp, error) {
	result := map[string]fileStamp{}
	e := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not supported: %s", p)
		}
		if st.IsDir() {
			return nil
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("special file cannot be copied: %s", p)
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		h, e := fileDigest(p)
		if e != nil {
			return e
		}
		result[rel] = fileStamp{st.Size(), st.ModTime().UnixNano(), h}
		return nil
	})
	return result, e
}
func randomSuffix() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return strconv.Itoa(os.Getpid())
	}
	return hex.EncodeToString(b)
}
func inspectExact(root string) (Object, error) {
	if incomplete(root) {
		return Object{"path": root, "exists": true, "writable": false, "papers": 0, "bytes": int64(0), "incomplete": "Target library migration is incomplete; clean up before using it. The original library is preserved."}, nil
	}
	if st, e := os.Stat(root); e == nil && !st.IsDir() {
		return Object{"path": root, "exists": true, "writable": false, "papers": 0, "bytes": int64(0)}, nil
	}
	papers, e := paperDirs(root)
	if e != nil {
		return nil, e
	}
	size := int64(0)
	paths := append([]string{}, papers...)
	if exists(filepath.Join(root, ".trash")) {
		paths = append(paths, filepath.Join(root, ".trash"))
	}
	for _, p := range paths {
		m, e := manifest(p)
		if e != nil {
			return nil, e
		}
		for _, f := range m {
			size += f.Size
		}
	}
	parent := root
	for !exists(parent) {
		next := filepath.Dir(parent)
		if next == parent {
			return nil, fmt.Errorf("path does not exist: %s", root)
		}
		parent = next
	}
	probe := filepath.Join(parent, ".easyread-probe-"+randomSuffix())
	f, e := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	writable := e == nil
	if e == nil {
		f.Close()
		os.Remove(probe)
	}
	return Object{"path": root, "exists": exists(root), "writable": writable, "papers": len(papers), "bytes": size}, nil
}
func inspectLibrary(path string) (Object, error) {
	root, e := targetLibrary(path)
	if e != nil {
		return nil, e
	}
	return inspectExact(root)
}
func cloudCandidates() []Object {
	out := []Object{}
	seen := map[string]bool{}
	add := func(id, label, path string) {
		if path == "" {
			return
		}
		p, e := fullPath(path)
		if e != nil {
			return
		}
		st, e := os.Stat(p)
		if e != nil || !st.IsDir() || seen[strings.ToLower(p)] {
			return
		}
		seen[strings.ToLower(p)] = true
		out = append(out, Object{"id": id, "label": label, "root_path": p, "path": filepath.Join(p, "EasyRead")})
	}
	for _, key := range []string{"OneDrive", "OneDriveConsumer", "OneDriveCommercial"} {
		add("onedrive", "OneDrive", os.Getenv(key))
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		paths, _ := filepath.Glob(filepath.Join(home, "Library", "CloudStorage", "OneDrive*"))
		for _, p := range paths {
			add("onedrive", "OneDrive", p)
		}
	}
	if runtime.GOOS == "windows" {
		for _, n := range []string{"iCloudDrive", "iCloud Drive"} {
			add("icloud", "iCloud Drive", filepath.Join(home, n))
		}
	}
	infos := []string{filepath.Join(home, ".dropbox", "info.json")}
	if runtime.GOOS == "windows" {
		infos = nil
		for _, key := range []string{"APPDATA", "LOCALAPPDATA"} {
			if p := os.Getenv(key); p != "" {
				infos = append(infos, filepath.Join(p, "Dropbox", "info.json"))
			}
		}
	}
	for _, p := range infos {
		v, e := store.ReadJSON(p)
		if e == nil {
			for _, account := range []string{"personal", "business"} {
				add("dropbox", "Dropbox", text(object(v[account])["path"]))
			}
		}
	}
	return out
}
func (a *App) location() (Object, error) {
	info, e := inspectExact(a.Library.Root)
	if e != nil {
		return nil, e
	}
	candidates := []Object{}
	for _, candidate := range cloudCandidates() {
		v, e := inspectLibrary(text(candidate["path"]))
		if e != nil {
			continue
		}
		for k, x := range v {
			candidate[k] = x
		}
		candidates = append(candidates, candidate)
	}
	status := a.Status()
	info["path"] = a.Library.Root
	info["temp"] = os.Getenv("EASYREAD_LIBRARY") != "" || os.Getenv("COREAD_LIBRARY") != ""
	info["candidates"] = candidates
	info["library_status"] = status
	info["moving"] = status == "moving"
	info["restart_required"] = status == "restart_required"
	return info, nil
}
func (a *App) moveLibrary(body Object) (Object, error) {
	a.mu.Lock()
	if a.status != "idle" {
		a.mu.Unlock()
		return nil, fmt.Errorf("library is moving or requires restart")
	}
	if os.Getenv("EASYREAD_LIBRARY") != "" || os.Getenv("COREAD_LIBRARY") != "" {
		a.mu.Unlock()
		return nil, fmt.Errorf("temporary library location cannot be changed")
	}
	if a.active > 0 || len(a.preparing) > 0 {
		a.mu.Unlock()
		return nil, fmt.Errorf("library is still saving or preparing pages")
	}
	if a.pages > 1 {
		a.mu.Unlock()
		return nil, fmt.Errorf("close other reader pages before moving library")
	}
	if (a.Jobs != nil && a.Jobs.Busy()) || (a.Bulk != nil && a.Bulk.Busy()) || a.PDFBusy() {
		a.mu.Unlock()
		return nil, fmt.Errorf("wait for translation or import tasks to finish")
	}
	ws, e := a.Library.All()
	if e != nil {
		a.mu.Unlock()
		return nil, e
	}
	for _, w := range ws {
		j, e := w.Load("job")
		if e != nil {
			a.mu.Unlock()
			return nil, e
		}
		if j["state"] == "queued" || j["state"] == "running" {
			a.mu.Unlock()
			return nil, fmt.Errorf("wait for queued or running tasks to finish")
		}
	}
	a.status = "moving"
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.status == "moving" {
			a.status = "idle"
		}
		a.mu.Unlock()
	}()
	result, e := a.migrateLibrary(text(body["path"]), text(body["mode"]))
	if e != nil {
		return nil, e
	}
	a.mu.Lock()
	a.status = "restart_required"
	a.mu.Unlock()
	return result, nil
}
func copyVerified(src, dst string) (map[string]fileStamp, error) {
	before, e := manifest(src)
	if e != nil {
		return nil, e
	}
	st, e := os.Stat(src)
	if e != nil {
		return nil, e
	}
	if st.IsDir() {
		if e = os.Mkdir(dst, 0700); e != nil {
			return nil, e
		}
	}
	keys := []string{}
	for k := range before {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, rel := range keys {
		source, target := src, dst
		if rel != "." {
			source = filepath.Join(src, rel)
			target = filepath.Join(dst, rel)
		}
		resolved, e := filepath.EvalSymlinks(source)
		if e != nil || !within(src, resolved) {
			return nil, fmt.Errorf("source changed during copy")
		}
		if e = noLink(source); e != nil {
			return nil, e
		}
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return nil, e
		}
		in, e := os.Open(source)
		if e != nil {
			return nil, e
		}
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			in.Close()
			return nil, e
		}
		_, e = io.Copy(out, in)
		in.Close()
		if e == nil {
			e = out.Sync()
		}
		ce := out.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
	}
	after, e := manifest(src)
	if e != nil {
		return nil, e
	}
	copied, e := manifest(dst)
	if e != nil {
		return nil, e
	}
	if !reflect.DeepEqual(before, after) || len(copied) != len(before) {
		return nil, fmt.Errorf("copy verification failed; library location unchanged")
	}
	for k, v := range before {
		if v.Size != copied[k].Size || v.Hash != copied[k].Hash {
			return nil, fmt.Errorf("copy verification failed; library location unchanged")
		}
	}
	return before, nil
}
func migrationSHA(p string) (string, error) {
	v, e := store.ReadJSON(filepath.Join(p, "paper.json"))
	if e != nil {
		return "", e
	}
	meta := object(v["meta"])
	if text(meta["source_pdf_path"]) != "" {
		s := text(meta["source_sha256"])
		b, e := hex.DecodeString(s)
		if e == nil && len(b) == 32 {
			return s, nil
		}
		return "", nil
	}
	p = filepath.Join(p, "source.pdf")
	if !exists(p) {
		return "", nil
	}
	if e = noLink(p); e != nil {
		return "", e
	}
	return fileDigest(p)
}

type migrationEntry struct {
	source, relative string
	snapshot         map[string]fileStamp
}
type createdEntry struct {
	path string
	info os.FileInfo
}

func freeSpace(root string) int64 {
	if runtime.GOOS == "windows" {
		return -1
	}
	out, e := exec.Command("df", "-Pk", root).Output()
	if e != nil {
		return -1
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return -1
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return -1
	}
	n, e := strconv.ParseInt(fields[3], 10, 64)
	if e != nil {
		return -1
	}
	return n * 1024
}
func (a *App) migrateLibrary(requested, mode string) (result Object, err error) {
	src, e := fullPath(a.Library.Root)
	if e != nil {
		return nil, e
	}
	dst, e := targetLibrary(requested)
	if e != nil {
		return nil, e
	}
	request, e := fullPath(requested)
	if e != nil {
		return nil, e
	}
	if mode != "copy" && mode != "use" && mode != "merge" {
		return nil, fmt.Errorf("choose copy, use or merge")
	}
	for _, p := range []string{request, dst} {
		if within(src, p) || within(p, src) {
			return nil, fmt.Errorf("old and new libraries must not overlap")
		}
	}
	state, e := inspectExact(dst)
	if e != nil {
		return nil, e
	}
	if state["incomplete"] != nil {
		return nil, fmt.Errorf("%s", state["incomplete"])
	}
	if state["writable"] != true {
		return nil, fmt.Errorf("target folder is not writable")
	}
	if mode == "copy" && integer(state["papers"]) > 0 {
		return nil, fmt.Errorf("target contains papers; choose use or merge")
	}
	if mode != "copy" && integer(state["papers"]) == 0 {
		return nil, fmt.Errorf("target has no papers; choose copy")
	}
	result = Object{"ok": true, "path": dst, "old_path": src, "copied": 0, "skipped": []any{}, "restart_required": true, "message": "Library location changed. Restart EasyRead to use it."}
	if mode == "use" {
		if _, e = a.Config.Save(Object{"library_dir": dst}); e != nil {
			return nil, e
		}
		p := filepath.Join(dst, migrationMarker)
		if v, e := store.ReadJSON(p); e == nil && v["state"] == "ready" {
			os.Remove(p)
		}
		return result, nil
	}
	papers, e := paperDirs(src)
	if e != nil {
		return nil, e
	}
	digests := map[string]bool{}
	if mode == "merge" {
		targets, e := paperDirs(dst)
		if e != nil {
			return nil, e
		}
		for _, p := range targets {
			sha, e := migrationSHA(p)
			if e != nil {
				return nil, e
			}
			if sha != "" {
				digests[sha] = true
			}
		}
	}
	entries := []migrationEntry{}
	skipped := []any{}
	for _, p := range papers {
		rel := filepath.Base(p)
		if exists(filepath.Join(dst, rel)) {
			if mode == "copy" {
				return nil, fmt.Errorf("target already contains %s; will not overwrite", rel)
			}
			skipped = append(skipped, Object{"id": rel, "reason": "Target already contains this folder; its content is preserved."})
			continue
		}
		sha := ""
		if mode == "merge" {
			sha, e = migrationSHA(p)
			if e != nil {
				return nil, e
			}
		}
		if sha != "" && digests[sha] {
			skipped = append(skipped, Object{"id": rel, "reason": "Target already contains the same PDF."})
			continue
		}
		digests[sha] = true
		entries = append(entries, migrationEntry{source: p, relative: rel})
	}
	trash := filepath.Join(src, ".trash")
	if exists(trash) {
		if e = noLink(trash); e != nil {
			return nil, e
		}
		if !exists(filepath.Join(dst, ".trash")) {
			entries = append(entries, migrationEntry{source: trash, relative: ".trash"})
		} else {
			if e = noLink(filepath.Join(dst, ".trash")); e != nil {
				return nil, e
			}
			children, e := os.ReadDir(trash)
			if e != nil {
				return nil, e
			}
			for _, child := range children {
				rel := filepath.Join(".trash", child.Name())
				if exists(filepath.Join(dst, rel)) {
					if mode == "copy" {
						return nil, fmt.Errorf("target already contains %s", rel)
					}
					skipped = append(skipped, Object{"id": rel, "reason": "Target already contains this folder."})
					continue
				}
				entries = append(entries, migrationEntry{source: filepath.Join(trash, child.Name()), relative: rel})
			}
		}
	}
	result["skipped"] = skipped
	if e = os.MkdirAll(dst, 0755); e != nil {
		return nil, e
	}
	needed := int64(0)
	for i := range entries {
		m, e := manifest(entries[i].source)
		if e != nil {
			return nil, e
		}
		for _, v := range m {
			needed += v.Size
		}
	}
	if free := freeSpace(dst); free >= 0 && free < needed {
		return nil, fmt.Errorf("insufficient free space for library copy")
	}
	staging := filepath.Join(dst, stagingPrefix+randomSuffix())
	if e = os.Mkdir(staging, 0700); e != nil {
		return nil, e
	}
	marker := filepath.Join(dst, migrationMarker)
	host, _ := os.Hostname()
	names := []any{}
	for _, en := range entries {
		names = append(names, en.relative)
	}
	transaction := Object{"host": host, "source": src, "target": dst, "staging": filepath.Base(staging), "entries": names, "state": "publishing"}
	created := []createdEntry{}
	owned, ready, committed := false, false, false
	defer func() {
		if err != nil && !committed {
			rollbackOK := true
			if ready {
				transaction["state"] = "rollback"
				if e := store.WriteJSON(marker, transaction); e != nil {
					rollbackOK = false
					created = nil
				}
			}
			for i := len(created) - 1; i >= 0; i-- {
				entry := created[i]
				st, e := os.Lstat(entry.path)
				if e != nil || st.Mode()&os.ModeSymlink != 0 || !os.SameFile(entry.info, st) {
					rollbackOK = false
					continue
				}
				if e = os.RemoveAll(entry.path); e != nil {
					rollbackOK = false
				}
			}
			if owned && rollbackOK {
				os.Remove(marker)
			}
		}
		warn := func(e error) {
			if committed && e != nil {
				warnings, _ := result["warnings"].([]string)
				warnings = append(warnings, e.Error())
				result["warnings"] = warnings
				result["message"] = text(result["message"]) + "\n" + e.Error()
			}
		}
		if committed {
			warn(os.Remove(marker))
		}
		warn(os.RemoveAll(staging))
	}()
	for i := range entries {
		entries[i].snapshot, e = copyVerified(entries[i].source, filepath.Join(staging, strconv.Itoa(i)))
		if e != nil {
			return nil, e
		}
	}
	for _, en := range entries {
		current, e := manifest(en.source)
		if e != nil {
			return nil, e
		}
		if !reflect.DeepEqual(current, en.snapshot) {
			return nil, fmt.Errorf("source changed during copy")
		}
	}
	f, e := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	owned = true
	e = json.NewEncoder(f).Encode(transaction)
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
	copied := 0
	for i, en := range entries {
		target := filepath.Join(dst, en.relative)
		parent, e := filepath.EvalSymlinks(filepath.Dir(target))
		if e != nil || !within(dst, parent) {
			return nil, fmt.Errorf("target changed during copy")
		}
		if exists(target) {
			return nil, fmt.Errorf("target already contains %s; will not overwrite", en.relative)
		}
		staged := filepath.Join(staging, strconv.Itoa(i))
		st, e := os.Stat(staged)
		if e != nil {
			return nil, e
		}
		if st.IsDir() {
			if e = os.Mkdir(target, 0700); e != nil {
				return nil, e
			}
			info, _ := os.Lstat(target)
			created = append(created, createdEntry{target, info})
			children, e := os.ReadDir(staged)
			if e != nil {
				return nil, e
			}
			for _, child := range children {
				if e = os.Rename(filepath.Join(staged, child.Name()), filepath.Join(target, child.Name())); e != nil {
					return nil, e
				}
			}
		} else {
			out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return nil, e
			}
			info, _ := out.Stat()
			created = append(created, createdEntry{target, info})
			in, e := os.Open(staged)
			if e != nil {
				out.Close()
				return nil, e
			}
			_, e = io.Copy(out, in)
			in.Close()
			ce := out.Close()
			if e != nil {
				return nil, e
			}
			if ce != nil {
				return nil, ce
			}
		}
		if en.relative != ".trash" && !strings.HasPrefix(en.relative, ".trash"+string(filepath.Separator)) {
			copied++
		}
	}
	result["copied"] = copied
	transaction["state"] = "ready"
	if e = store.WriteJSON(marker, transaction); e != nil {
		return nil, e
	}
	ready = true
	if _, e = a.Config.Save(Object{"library_dir": dst}); e != nil {
		return nil, e
	}
	committed = true
	return result, nil
}
func (a *App) cleanupLibrary(body Object) (Object, error) {
	root, e := fullPath(text(body["path"]))
	if e != nil {
		return nil, e
	}
	current, e := fullPath(a.Library.Root)
	if e != nil {
		return nil, e
	}
	if root == current {
		return nil, fmt.Errorf("cannot clean current library")
	}
	p := filepath.Join(root, migrationMarker)
	if e = noLink(p); e != nil {
		return nil, e
	}
	v, e := store.ReadJSON(p)
	if e != nil {
		return nil, e
	}
	if v["state"] == "ready" {
		e = os.Remove(p)
		return Object{"ok": e == nil, "removed": 0}, e
	}
	host, _ := os.Hostname()
	if text(v["host"]) != "" && text(v["host"]) != host {
		return nil, fmt.Errorf("migration belongs to another computer")
	}
	names := []string{}
	for _, v := range array(v["entries"]) {
		names = append(names, text(v))
	}
	names = append(names, text(v["staging"]))
	seen := map[string]bool{}
	removed := 0
	for _, name := range names {
		if name == "" || name == "." || filepath.IsAbs(name) || seen[name] {
			continue
		}
		seen[name] = true
		target := filepath.Join(root, name)
		if target == root || !within(root, target) {
			continue
		}
		parent, e := filepath.EvalSymlinks(filepath.Dir(target))
		if e != nil || !within(root, parent) {
			continue
		}
		if !exists(target) {
			continue
		}
		if e = noLink(target); e != nil {
			return nil, e
		}
		if e = os.RemoveAll(target); e != nil {
			return nil, e
		}
		removed++
	}
	if e = os.Remove(p); e != nil {
		return nil, e
	}
	return Object{"ok": true, "removed": removed}, nil
}
func reveal(path string) error {
	if !exists(path) {
		return os.ErrNotExist
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if e := cmd.Start(); e != nil {
		return e
	}
	go cmd.Wait()
	return nil
}
func validateConfig(body Object) error {
	for _, key := range []string{"auto_translate", "translate_figure_text", "check_updates"} {
		if v, ok := body[key]; ok {
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("%s must be a boolean", key)
			}
		}
	}
	if v, ok := body["translation_instructions"]; ok {
		value, ok := v.(string)
		if !ok || len([]rune(value)) > 4000 {
			return fmt.Errorf("translation instructions must be text of at most 4000 characters")
		}
		body["translation_instructions"] = strings.TrimSpace(value)
	}
	if v, ok := body["figure_images"]; ok {
		options, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("figure_images must be an object")
		}
		if v, ok := options["enabled"]; ok {
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("figure_images.enabled must be a boolean")
			}
		}
		if v, ok := options["provider"]; ok && v != "codex" && v != "api" {
			return fmt.Errorf("figure_images.provider must be codex or api")
		}
		for _, key := range []string{"base_url", "api_key", "model"} {
			if v, ok := options[key]; ok {
				value, ok := v.(string)
				if !ok {
					return fmt.Errorf("figure_images.%s must be a string", key)
				}
				options[key] = strings.TrimSpace(value)
			}
		}
		if raw := text(options["base_url"]); raw != "" {
			u, e := url.Parse(raw)
			if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
				return fmt.Errorf("figure image endpoint must be an HTTP or HTTPS URL without credentials")
			}
		}
		if v, ok := options["timeout"]; ok {
			n := integer(v)
			if n <= 0 {
				return fmt.Errorf("figure_images.timeout must be a positive integer")
			}
			switch x := v.(type) {
			case float64:
				if float64(n) != x {
					return fmt.Errorf("figure_images.timeout must be an integer")
				}
			case int:
			default:
				return fmt.Errorf("figure_images.timeout must be an integer")
			}
		}
	}
	if _, ok := body["library_dir"]; ok {
		return errors.New("use the library location migration workflow to change library_dir")
	}
	for key, limit := range map[string]int{"port": 65535, "batch_pages": 100, "paper_concurrency": 4, "page_cap": 100000, "concurrency": 8} {
		if v, ok := body[key]; ok {
			n := integer(v)
			switch v.(type) {
			case float64:
				if float64(n) != v.(float64) {
					return fmt.Errorf("%s must be an integer", key)
				}
			case int:
			default:
				return fmt.Errorf("%s must be an integer", key)
			}
			min := 0
			if key == "port" || key == "batch_pages" || key == "paper_concurrency" {
				min = 1
			}
			if n < min || n > limit {
				return fmt.Errorf("invalid %s", key)
			}
		}
	}
	if v, ok := body["translation_prompt"]; ok && v != nil {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" || len([]rune(s)) > 12000 {
			return fmt.Errorf("translation prompt must contain 1–12000 characters")
		}
	}
	if v, ok := body["target"]; ok {
		valid := map[string]bool{"zh": true, "zh-Hant": true, "en": true, "ja": true, "ko": true, "es": true, "fr": true, "de": true, "it": true}
		if !valid[text(v)] {
			return fmt.Errorf("unsupported target language")
		}
	}
	return nil
}
