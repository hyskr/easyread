package importer

import (
	"database/sql"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type entry struct {
	Key, PDF, Added, Missing string
	Meta                     M
	Cats, Tags               []string
}
type snapshot struct {
	Entries     []entry
	Collections []string
	Backup      bool
	Path        string
}

func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	return p
}
func folder(where string) (snapshot, error) {
	root, e := filepath.Abs(expand(where))
	snap := snapshot{Path: root}
	if e != nil {
		return snap, e
	}
	info, e := os.Stat(root)
	if e != nil || !info.IsDir() {
		return snap, errors.New("folder not found")
	}
	seen := map[string]bool{}
	e = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
			return nil
		}
		if len(snap.Entries) >= 5000 {
			return errors.New("folder contains over 5000 PDFs; import smaller folders")
		}
		rel, _ := filepath.Rel(root, p)
		cat := filepath.ToSlash(filepath.Join(filepath.Base(root), filepath.Dir(rel)))
		for i, parts := 0, strings.Split(cat, "/"); i < len(parts); i++ {
			v := strings.Join(parts[:i+1], "/")
			if !seen[v] {
				seen[v] = true
				snap.Collections = append(snap.Collections, v)
			}
		}
		snap.Entries = append(snap.Entries, entry{Key: filepath.ToSlash(rel), PDF: p, Meta: M{}, Cats: []string{cat}})
		return nil
	})
	return snap, e
}
func readRows(tx *sql.Tx, q string) ([]M, error) {
	rows, e := tx.Query(q)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	cols, e := rows.Columns()
	if e != nil {
		return nil, e
	}
	out := []M{}
	for rows.Next() {
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range values {
			dest[i] = &values[i]
		}
		if e = rows.Scan(dest...); e != nil {
			return nil, e
		}
		m := M{}
		for i, k := range cols {
			v := values[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			m[k] = v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func key(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func zotero(where, base string) (snapshot, error) {
	root, e := filepath.Abs(expand(where))
	snap := snapshot{Path: root}
	if e != nil {
		return snap, e
	}
	if strings.HasSuffix(strings.ToLower(root), ".sqlite") {
		root = filepath.Dir(root)
		snap.Path = root
	}
	var db *sql.DB
	var tx *sql.Tx
	for _, suffix := range []string{"", ".bak"} {
		p := filepath.Join(root, "zotero.sqlite"+suffix)
		if _, er := os.Stat(p); er != nil {
			continue
		}
		u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro"}
		db, e = sql.Open("sqlite", u.String())
		if e != nil {
			continue
		}
		db.SetMaxOpenConns(1)
		tx, e = db.Begin()
		if e == nil {
			_, e = readRows(tx, "SELECT count(*) FROM items")
		}
		if e == nil {
			snap.Backup = suffix != ""
			break
		}
		if tx != nil {
			tx.Rollback()
		}
		db.Close()
		tx = nil
	}
	if tx == nil || e != nil {
		return snap, errors.New("cannot read Zotero database; close Zotero and retry")
	}
	defer db.Close()
	defer tx.Rollback()
	tables, er := readRows(tx, "SELECT name FROM sqlite_master WHERE type IN ('table','view')")
	if er != nil {
		return snap, er
	}
	has := map[string]bool{}
	for _, r := range tables {
		has[str(r["name"])] = true
	}
	optional := func(table, q string) []M {
		if !has[table] {
			return nil
		}
		rs, _ := readRows(tx, q)
		return rs
	}
	deleted := map[string]bool{}
	for _, r := range optional("deletedItems", "SELECT itemID FROM deletedItems") {
		deleted[key(r["itemID"])] = true
	}
	deadCols := map[string]bool{}
	for _, r := range optional("deletedCollections", "SELECT collectionID FROM deletedCollections") {
		deadCols[key(r["collectionID"])] = true
	}
	groups := map[string]string{}
	for _, r := range optional("groups", "SELECT libraryID,name FROM groups") {
		groups[key(r["libraryID"])] = str(r["name"])
	}
	cols := map[string]M{}
	for _, r := range optional("collections", "SELECT collectionID,collectionName,parentCollectionID,libraryID FROM collections") {
		cols[key(r["collectionID"])] = r
	}
	paths := map[string]string{}
	var colPath func(string, map[string]bool) string
	colPath = func(cid string, visited map[string]bool) string {
		if deadCols[cid] || visited[cid] {
			return ""
		}
		if p, ok := paths[cid]; ok {
			return p
		}
		c, ok := cols[cid]
		if !ok {
			return ""
		}
		visited[cid] = true
		head := ""
		parent := key(c["parentCollectionID"])
		if _, ok := cols[parent]; ok {
			head = colPath(parent, visited)
			if head == "" {
				return ""
			}
		} else {
			head = strings.ReplaceAll(groups[key(c["libraryID"])], "/", "／")
		}
		name := strings.ReplaceAll(strings.TrimSpace(str(c["collectionName"])), "/", "／")
		if name == "" {
			name = "?"
		}
		p := name
		if head != "" {
			p = head + "/" + name
		}
		paths[cid] = p
		return p
	}
	for cid := range cols {
		if p := colPath(cid, map[string]bool{}); p != "" {
			snap.Collections = append(snap.Collections, p)
		}
	}
	sort.Strings(snap.Collections)
	inCol := map[string][]string{}
	for _, r := range optional("collectionItems", "SELECT collectionID,itemID FROM collectionItems") {
		if p := paths[key(r["collectionID"])]; p != "" {
			i := key(r["itemID"])
			inCol[i] = append(inCol[i], p)
		}
	}
	tags := map[string][]string{}
	for _, r := range optional("itemTags", "SELECT it.itemID,t.name,it.type FROM itemTags it JOIN tags t USING(tagID)") {
		if key(r["type"]) == "0" {
			i := key(r["itemID"])
			tags[i] = append(tags[i], str(r["name"]))
		}
	}
	fieldsTable := "fields"
	if has["fieldsCombined"] {
		fieldsTable = "fieldsCombined"
	}
	values := map[string]M{}
	rows, er := readRows(tx, "SELECT d.itemID,f.fieldName,v.value FROM itemData d JOIN "+fieldsTable+" f USING(fieldID) JOIN itemDataValues v USING(valueID)")
	if er != nil {
		return snap, er
	}
	for _, r := range rows {
		i := key(r["itemID"])
		if values[i] == nil {
			values[i] = M{}
		}
		values[i][str(r["fieldName"])] = r["value"]
	}
	authors, others := map[string][]string{}, map[string][]string{}
	for _, r := range optional("itemCreators", "SELECT ic.itemID,c.firstName,c.lastName,c.fieldMode,ct.creatorType FROM itemCreators ic JOIN creators c USING(creatorID) JOIN creatorTypes ct USING(creatorTypeID) ORDER BY ic.itemID,ic.orderIndex") {
		i := key(r["itemID"])
		n := str(r["lastName"])
		if key(r["fieldMode"]) != "1" {
			n = strings.TrimSpace(str(r["firstName"]) + " " + n)
		}
		others[i] = append(others[i], n)
		if r["creatorType"] == "author" {
			authors[i] = append(authors[i], n)
		}
	}
	pdfs := map[string]M{}
	lone := []M{}
	rows, er = readRows(tx, "SELECT ia.itemID,ia.parentItemID,ia.linkMode,ia.path,i.key,i.dateAdded FROM itemAttachments ia JOIN items i USING(itemID) WHERE ia.contentType='application/pdf' ORDER BY i.dateAdded")
	if er != nil {
		return snap, er
	}
	for _, r := range rows {
		if deleted[key(r["itemID"])] {
			continue
		}
		if r["parentItemID"] == nil {
			lone = append(lone, r)
		} else {
			i := key(r["parentItemID"])
			if pdfs[i] == nil {
				pdfs[i] = r
			}
		}
	}
	makeEntry := func(r M, typ string, att M) entry {
		i := key(r["itemID"])
		f := values[i]
		a := authors[i]
		if len(a) == 0 {
			a = others[i]
		}
		date := str(f["date"])
		year := regexp.MustCompile(`(?:18|19|20)\d{2}`).FindString(date)
		if m := regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`).FindStringSubmatch(date); len(m) > 0 {
			date = m[1]
			if m[2] != "00" {
				date += "-" + m[2]
				if m[3] != "00" {
					date += "-" + m[3]
				}
			}
			if m[1] == "0000" {
				date = ""
				year = ""
			}
		}
		venue := ""
		for _, k := range []string{"publicationTitle", "proceedingsTitle", "conferenceName", "bookTitle", "repository", "university", "institution", "publisher", "websiteTitle"} {
			if venue == "" {
				venue = str(f[k])
			}
		}
		meta := M{"title_en": f["title"], "authors": strings.Join(a, ", "), "date": date, "year": year, "doi": regexp.MustCompile(`(?i)^(https?://(?:dx\.)?doi\.org/|doi:\s*)`).ReplaceAllString(str(f["DOI"]), ""), "url": f["url"], "abstract_en": f["abstractNote"], "venue": venue, "zotero_type": typ}
		if m := regexp.MustCompile(`(?i)(?:arxiv[:/ ]\s*|abs/|pdf/)(\d{4}\.\d{4,5})`).FindStringSubmatch(str(f["archiveID"]) + " " + str(f["url"]) + " " + str(f["extra"]) + " " + str(f["number"])); len(m) > 0 {
			meta["arxiv"] = "arXiv:" + m[1]
		}
		for k, v := range meta {
			if v == nil || v == "" {
				delete(meta, k)
			}
		}
		en := entry{Key: str(r["key"]), Meta: meta, Cats: inCol[i], Tags: tags[i], Added: strings.ReplaceAll(str(r["dateAdded"]), " ", "T") + "+00:00", Missing: "没有 PDF 附件"}
		if att != nil {
			p := str(att["path"])
			switch {
			case strings.HasPrefix(p, "storage:"):
				p = filepath.Join(root, "storage", str(att["key"]), strings.TrimPrefix(p, "storage:"))
			case strings.HasPrefix(p, "attachments:"):
				if base != "" {
					p = filepath.Join(expand(base), strings.TrimPrefix(p, "attachments:"))
				} else {
					p = ""
				}
			case key(att["linkMode"]) != "2":
				p = ""
			}
			if st, er := os.Stat(p); er == nil && !st.IsDir() {
				en.PDF = p
				en.Missing = ""
			} else {
				en.Missing = first(p, str(att["path"]), "PDF file missing")
			}
		}
		return en
	}
	rows, er = readRows(tx, "SELECT i.itemID,i.key,i.dateAdded,t.typeName FROM items i JOIN itemTypes t USING(itemTypeID) ORDER BY i.dateAdded")
	if er != nil {
		return snap, er
	}
	for _, r := range rows {
		typ := str(r["typeName"])
		if deleted[key(r["itemID"])] || typ == "attachment" || typ == "note" || typ == "annotation" {
			continue
		}
		snap.Entries = append(snap.Entries, makeEntry(r, typ, pdfs[key(r["itemID"])]))
	}
	for _, r := range lone {
		en := makeEntry(r, "attachment", r)
		if en.PDF != "" && str(en.Meta["title_en"]) == "" {
			en.Meta["title_en"] = strings.TrimSuffix(filepath.Base(en.PDF), filepath.Ext(en.PDF))
		}
		snap.Entries = append(snap.Entries, en)
	}
	return snap, nil
}
func Candidates() []M {
	h, _ := os.UserHomeDir()
	roots := []string{filepath.Join(h, ".zotero", "zotero"), filepath.Join(h, "Library", "Application Support", "Zotero", "Profiles"), filepath.Join(os.Getenv("APPDATA"), "Zotero", "Zotero", "Profiles")}
	pairs := []M{}
	pref := func(s, k string) string {
		m := regexp.MustCompile(`user_pref\("` + regexp.QuoteMeta(k) + `",\s*"((?:[^"\\]|\\.)*)"\);`).FindStringSubmatch(s)
		if len(m) == 0 {
			return ""
		}
		return strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(m[1])
	}
	for _, r := range roots {
		ps, _ := filepath.Glob(filepath.Join(r, "*", "prefs.js"))
		for _, p := range ps {
			b, e := os.ReadFile(p)
			if e == nil {
				pairs = append(pairs, M{"path": pref(string(b), "extensions.zotero.dataDir"), "base": pref(string(b), "extensions.zotero.baseAttachmentPath")})
			}
		}
	}
	pairs = append(pairs, M{"path": filepath.Join(h, "Zotero"), "base": ""})
	out := []M{}
	seen := map[string]bool{}
	for _, p := range pairs {
		d := str(p["path"])
		if d != "" && !seen[d] {
			if _, e := os.Stat(filepath.Join(d, "zotero.sqlite")); e == nil {
				seen[d] = true
				out = append(out, p)
			}
		}
	}
	return out
}
