package server

import (
	"github.com/hyskr/easyread/backend/config"
	"github.com/hyskr/easyread/backend/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func locationApp(t *testing.T) *App {
	t.Helper()
	cfg, e := config.New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	lib, e := store.NewLibrary(filepath.Join(t.TempDir(), "source"))
	if e != nil {
		t.Fatal(e)
	}
	cfg.Save(Object{"library_dir": lib.Root})
	return &App{Config: cfg, Library: lib, status: "idle", preparing: map[string]bool{}}
}
func locationPaper(t *testing.T, root, id, content string) {
	t.Helper()
	p := filepath.Join(root, id)
	if e := os.MkdirAll(p, 0755); e != nil {
		t.Fatal(e)
	}
	if e := store.WriteJSON(filepath.Join(p, "paper.json"), Object{"meta": Object{"title_en": id}}); e != nil {
		t.Fatal(e)
	}
	store.WriteJSON(filepath.Join(p, "item.json"), Object{})
	store.WriteJSON(filepath.Join(p, "reader.json"), Object{"unknown": true})
	os.WriteFile(filepath.Join(p, "source.pdf"), []byte("%PDF "+content), 0600)
}
func TestLocationCopyPreservesSourceTrashAndUnknown(t *testing.T) {
	a := locationApp(t)
	locationPaper(t, a.Library.Root, "paper001", "one")
	locationPaper(t, filepath.Join(a.Library.Root, ".trash"), "deleted", "gone")
	target := filepath.Join(t.TempDir(), "EasyRead")
	result, e := a.moveLibrary(Object{"path": target, "mode": "copy"})
	if e != nil {
		t.Fatal(e)
	}
	if result["copied"] != 1 || a.Status() != "restart_required" {
		t.Fatal(result)
	}
	for _, root := range []string{target, a.Library.Root} {
		v, e := store.ReadJSON(filepath.Join(root, "paper001", "reader.json"))
		if e != nil || v["unknown"] != true {
			t.Fatal(v, e)
		}
		if !exists(filepath.Join(root, ".trash", "deleted", "source.pdf")) {
			t.Fatal("trash missing")
		}
	}
	cfg, _ := a.Config.Load()
	resolvedTarget, _ := filepath.EvalSymlinks(target)
	if cfg["library_dir"] != resolvedTarget {
		t.Fatal(cfg)
	}
	if exists(filepath.Join(target, migrationMarker)) {
		t.Fatal("marker not cleaned")
	}
}
func TestLocationMergeSkipsSamePDFAndCollision(t *testing.T) {
	a := locationApp(t)
	for _, id := range []string{"paper001", "paper002", "paper003"} {
		locationPaper(t, a.Library.Root, id, id)
	}
	target := filepath.Join(t.TempDir(), "EasyRead")
	locationPaper(t, target, "differentid", "paper001")
	locationPaper(t, target, "paper003", "other")
	v, e := a.moveLibrary(Object{"path": target, "mode": "merge"})
	if e != nil {
		t.Fatal(e)
	}
	if v["copied"] != 1 || len(array(v["skipped"])) != 2 {
		t.Fatal(v)
	}
	b, _ := os.ReadFile(filepath.Join(target, "paper003", "source.pdf"))
	if string(b) != "%PDF other" {
		t.Fatal("existing content overwritten")
	}
}
func TestLocationFailureRollsBackOnCorruptConfig(t *testing.T) {
	a := locationApp(t)
	locationPaper(t, a.Library.Root, "paper001", "one")
	target := filepath.Join(t.TempDir(), "EasyRead")
	os.Mkdir(target, 0755)
	os.WriteFile(filepath.Join(target, "keep.txt"), []byte("keep"), 0600)
	os.WriteFile(filepath.Join(a.Config.Home, "config.json"), []byte("{bad"), 0600)
	if _, e := a.moveLibrary(Object{"path": target, "mode": "copy"}); e == nil {
		t.Fatal("expected save failure")
	}
	if a.Status() != "idle" || exists(filepath.Join(target, "paper001")) {
		t.Fatal("rollback failed")
	}
	b, _ := os.ReadFile(filepath.Join(target, "keep.txt"))
	if string(b) != "keep" {
		t.Fatal("existing file changed")
	}
	if !exists(filepath.Join(a.Library.Root, "paper001", "reader.json")) {
		t.Fatal("source changed")
	}
}
func TestLocationSymlinkRejected(t *testing.T) {
	a := locationApp(t)
	locationPaper(t, a.Library.Root, "paper001", "one")
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("private"), 0600)
	if e := os.Symlink(outside, filepath.Join(a.Library.Root, "paper001", "escape")); e != nil {
		t.Skip(e)
	}
	target := filepath.Join(t.TempDir(), "EasyRead")
	if _, e := a.moveLibrary(Object{"path": target, "mode": "copy"}); e == nil || !strings.Contains(e.Error(), "symbolic") {
		t.Fatal(e)
	}
	if a.Status() != "idle" {
		t.Fatal(a.Status())
	}
}
func TestLocationBusyAndNestedBlocked(t *testing.T) {
	a := locationApp(t)
	locationPaper(t, a.Library.Root, "paper001", "one")
	target := filepath.Join(t.TempDir(), "EasyRead")
	for _, field := range []string{"active", "pages", "job"} {
		switch field {
		case "active":
			a.active = 1
		case "pages":
			a.pages = 2
		case "job":
			store.WriteJSON(filepath.Join(a.Library.Root, "paper001", "job.json"), Object{"state": "queued"})
		}
		if _, e := a.moveLibrary(Object{"path": target, "mode": "copy"}); e == nil {
			t.Fatal("busy move allowed", field)
		}
		a.active = 0
		a.pages = 0
	}
	os.Remove(filepath.Join(a.Library.Root, "paper001", "job.json"))
	if _, e := a.moveLibrary(Object{"path": filepath.Join(a.Library.Root, "nested"), "mode": "copy"}); e == nil {
		t.Fatal("nested move allowed")
	}
}
func TestIncompleteCleanupConfinedToTarget(t *testing.T) {
	a := locationApp(t)
	target := filepath.Join(t.TempDir(), "EasyRead")
	locationPaper(t, target, "existing", "keep")
	locationPaper(t, target, "newpaper", "remove")
	host, _ := os.Hostname()
	store.WriteJSON(filepath.Join(target, migrationMarker), Object{"state": "publishing", "host": host, "entries": []any{"newpaper", "../outside", "."}, "staging": stagingPrefix + "abc"})
	info, e := inspectLibrary(target)
	if e != nil || info["incomplete"] == nil {
		t.Fatal(info, e)
	}
	v, e := a.cleanupLibrary(Object{"path": target})
	if e != nil || v["removed"] != 1 {
		t.Fatal(v, e)
	}
	if !exists(filepath.Join(target, "existing", "source.pdf")) || exists(filepath.Join(target, "newpaper")) {
		t.Fatal("cleanup damaged target")
	}
}
func TestRemoteAndReadyMarkersRemainUsable(t *testing.T) {
	target := filepath.Join(t.TempDir(), "EasyRead")
	locationPaper(t, target, "paper001", "one")
	store.WriteJSON(filepath.Join(target, migrationMarker), Object{"state": "publishing", "host": "some-other-computer"})
	info, e := inspectLibrary(target)
	if e != nil || info["incomplete"] != nil {
		t.Fatal(info, e)
	}
	store.WriteJSON(filepath.Join(target, migrationMarker), Object{"state": "ready"})
	if incomplete(target) {
		t.Fatal("ready library blocked")
	}
}
