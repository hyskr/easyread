package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ConvertPDFToLink verifies both copies before replacing storage with a reference.
func (l *Library) ConvertPDFToLink(id, source string, removeCopy bool) (*Workspace, error) {
	w, e := l.Workspace(id)
	if e != nil {
		return nil, e
	}
	if regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`).MatchString(source) {
		return nil, fmt.Errorf("only local PDF files supported")
	}
	path, e := filepath.Abs(expand(source))
	if e != nil {
		return nil, e
	}
	path, e = filepath.EvalSymlinks(path)
	if e != nil {
		return nil, e
	}
	rel, e := filepath.Rel(l.Root, path)
	if e != nil {
		return nil, e
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("linked PDF must be outside library")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	if !strings.HasPrefix(string(b), "%PDF") {
		return nil, fmt.Errorf("not a PDF file")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(b))
	unlock, e := Lock(w.Root)
	if e != nil {
		return nil, e
	}
	defer unlock()
	p, e := w.Load("paper")
	if e != nil {
		return nil, e
	}
	meta := obj(p["meta"])
	if digest[:12] != id || meta["source_sha256"] != digest {
		return nil, fmt.Errorf("PDF content differs; cannot convert to link")
	}
	copyPath := filepath.Join(w.Root, "source.pdf")
	st, e := os.Lstat(copyPath)
	if e == nil {
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("stored PDF must be a regular file")
		}
		stored, e := os.ReadFile(copyPath)
		if e != nil {
			return nil, e
		}
		if fmt.Sprintf("%x", sha256.Sum256(stored)) != digest {
			return nil, fmt.Errorf("PDF content differs; cannot convert to link")
		}
		if !removeCopy {
			return nil, fmt.Errorf("conversion requires explicit permission to remove stored copy")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	} else if !truth(meta["source_pdf_path"]) {
		return nil, fmt.Errorf("no stored PDF to convert")
	}
	meta["source_pdf_path"] = path
	p["meta"] = meta
	if e = WriteJSON(filepath.Join(w.Root, "paper.json"), p); e != nil {
		return nil, e
	}
	if e = os.Remove(copyPath); e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	return w, nil
}

var nonLetters = regexp.MustCompile(`[^a-z]`)

func letters(s string) string { return nonLetters.ReplaceAllString(strings.ToLower(s), "") }

// RepairDone corrects older versions' incorrectly completed blank translation pages.
func (w *Workspace) RepairDone() ([]int, error) {
	bad := []int{}
	e := w.Update("paper", func(p Object) error {
		tr := obj(p["translation"])
		if truth(tr["gaps_checked"]) {
			return nil
		}
		blocks := array(p["blocks"])
		on := map[int]bool{}
		refStart := -1
		for _, v := range blocks {
			b := obj(v)
			on[number(b["page"])] = true
			if b["type"] == "references" {
				n := number(b["page"])
				if refStart < 0 || n < refStart {
					refStart = n
				}
			}
		}
		encoded, _ := json.Marshal(blocks)
		have := letters(string(encoded))
		done := []any{}
		for _, v := range array(tr["done_pages"]) {
			n := number(v)
			missing := false
			if !on[n] && (refStart < 0 || n < refStart) {
				b, e := os.ReadFile(filepath.Join(w.Root, "extract", fmt.Sprintf("page-%03d.txt", n)))
				if e == nil {
					page := letters(string(b))
					if len(page) >= 80 {
						missing = true
						for _, i := range []int{len(page) / 4, len(page) / 2, len(page) * 3 / 4} {
							end := i + 30
							if end > len(page) {
								end = len(page)
							}
							if strings.Contains(have, page[i:end]) {
								missing = false
								break
							}
						}
					}
				}
			}
			if missing {
				bad = append(bad, n)
			} else {
				done = append(done, v)
			}
		}
		if len(bad) > 0 {
			tr["done_pages"] = done
		}
		tr["gaps_checked"] = 1
		p["translation"] = tr
		return nil
	})
	return bad, e
}
