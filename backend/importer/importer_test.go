package importer

import (
	"context"
	"database/sql"
	"github.com/hyskr/easyread/backend/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFetchCitation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a.pdf" {
			w.Write([]byte("%PDF-1.4 test"))
			return
		}
		w.Write([]byte(`<meta content="Don't guess" name="citation_title"><meta name="citation_author" content="Smith, Jane"><meta name="citation_pdf_url" content="/a.pdf">`))
	}))
	defer s.Close()
	b, n, m, e := Fetch(context.Background(), s.URL+"/paper")
	if e != nil || len(b) == 0 || n != "a.pdf" || m["title_en"] != "Don't guess" || m["authors"] != "Jane Smith" {
		t.Fatal(n, m, e)
	}
	if _, _, e = get(context.Background(), s.URL, 2); e == nil {
		t.Fatal("size limit ignored")
	}
}
func TestFolderDeduplication(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "nested"), 0700)
	os.MkdirAll(filepath.Join(root, ".hidden"), 0700)
	os.WriteFile(filepath.Join(root, "nested", "a.pdf"), []byte("%PDF-1.4 test"), 0600)
	os.WriteFile(filepath.Join(root, ".hidden", "a.pdf"), []byte("%PDF hidden"), 0600)
	lib, _ := store.NewLibrary(t.TempDir())
	m := New(lib, nil)
	body := M{"source": "folder", "path": root}
	scan, e := m.Scan(body)
	if e != nil || scan["total"] != 1 {
		t.Fatal(scan, e)
	}
	for i := 0; i < 2; i++ {
		if _, e = m.Start(body); e != nil {
			t.Fatal(e)
		}
		deadline := time.Now().Add(3 * time.Second)
		for m.Busy() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if m.Status()["state"] != "done" {
			t.Fatal(m.Status())
		}
	}
	all, _ := lib.All()
	if len(all) != 1 {
		t.Fatal("duplicates")
	}
	item, _ := all[0].Load("item")
	if len(arr(item["tags"])) != 1 {
		t.Fatal(item)
	}
}
func TestZoteroReadOnly(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "zotero.sqlite")
	db, e := sql.Open("sqlite", p)
	if e != nil {
		t.Fatal(e)
	}
	queries := []string{
		`CREATE TABLE items(itemID INTEGER,key TEXT,dateAdded TEXT,itemTypeID INTEGER)`,
		`CREATE TABLE itemTypes(itemTypeID INTEGER,typeName TEXT)`,
		`CREATE TABLE fields(fieldID INTEGER,fieldName TEXT)`,
		`CREATE TABLE itemData(itemID INTEGER,fieldID INTEGER,valueID INTEGER)`,
		`CREATE TABLE itemDataValues(valueID INTEGER,value TEXT)`,
		`CREATE TABLE itemAttachments(itemID INTEGER,parentItemID INTEGER,linkMode INTEGER,path TEXT,contentType TEXT)`,
		`CREATE TABLE deletedItems(itemID INTEGER)`,
		`CREATE TABLE collections(collectionID INTEGER,collectionName TEXT,parentCollectionID INTEGER,libraryID INTEGER)`,
		`CREATE TABLE collectionItems(collectionID INTEGER,itemID INTEGER)`,
		`INSERT INTO itemTypes VALUES(1,'journalArticle'),(2,'attachment')`,
		`INSERT INTO items VALUES(1,'PAPER','2025-01-02 03:04:05',1),(2,'ATT','2025-01-02 03:04:06',2),(3,'TRASH','2025-01-02 03:04:07',1)`,
		`INSERT INTO deletedItems VALUES(3)`,
		`INSERT INTO fields VALUES(1,'title'),(2,'date')`,
		`INSERT INTO itemDataValues VALUES(1,'My paper'),(2,'2025-01-00 January 2025')`,
		`INSERT INTO itemData VALUES(1,1,1),(1,2,2)`,
		`INSERT INTO itemAttachments VALUES(2,1,0,'storage:a.pdf','application/pdf')`,
		`INSERT INTO collections VALUES(1,'Research',NULL,1),(2,'Sub',1,1)`,
		`INSERT INTO collectionItems VALUES(2,1)`}
	for _, q := range queries {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(q, e)
		}
	}
	db.Close()
	os.MkdirAll(filepath.Join(root, "storage", "ATT"), 0700)
	os.WriteFile(filepath.Join(root, "storage", "ATT", "a.pdf"), []byte("%PDF-1.4 test"), 0600)
	before, _ := os.ReadFile(p)
	s, e := zotero(root, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Entries) != 1 || s.Entries[0].Meta["title_en"] != "My paper" || s.Entries[0].Meta["date"] != "2025-01" || s.Entries[0].PDF == "" || s.Entries[0].Cats[0] != "Research/Sub" {
		t.Fatalf("%+v", s)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("database modified")
	}
}

func TestLinkedImportDoesNotPrepare(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.pdf"), []byte("%PDF-1.4 linked"), 0600)
	lib, _ := store.NewLibrary(t.TempDir())
	m := New(lib, nil)
	m.OnImported = func(*store.Workspace) error { t.Error("linked import prepared eagerly"); return nil }
	if _, e := m.Start(M{"source": "folder", "path": root, "link": true}); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for m.Busy() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.Status()["state"] != "done" {
		t.Fatal(m.Status())
	}
}

func TestConcurrentStartClose(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.pdf"), []byte("%PDF-1.4 close"), 0600)
	lib, _ := store.NewLibrary(t.TempDir())
	for i := 0; i < 20; i++ {
		m := New(lib, nil)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = m.Start(M{"source": "folder", "path": root}) }()
		go func() { defer wg.Done(); m.Close() }()
		wg.Wait()
		m.Close()
		if m.Busy() {
			t.Fatal("close left worker running")
		}
		if _, e := m.Start(M{"source": "folder", "path": root}); e == nil {
			t.Fatal("start after close accepted")
		}
	}
}
