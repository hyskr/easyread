package pdf

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/hyskr/easyread/backend/store"
)

// Link contains the original annotation label, not an inferred translation.
type Link struct {
	URL    string `json:"url"`
	Quote  string `json:"quote"`
	Prefix string `json:"prefix"`
	Suffix string `json:"suffix"`
}

func safeURL(s string) string {
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return ""
		}
	}
	u, e := url.Parse(s)
	if e != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Hostname() == "" {
			return ""
		}
	case "mailto":
		if u.Opaque == "" && u.Path == "" {
			return ""
		}
	default:
		return ""
	}
	return strings.NewReplacer("(", "%28", ")", "%29", "[", "%5B", "]", "%5D").Replace(s)
}
func parseLinks(data []byte) map[string][]Link {
	result := map[string][]Link{}
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	page := "1"
	var current *Link
	var text strings.Builder
	depth := 0
	for {
		tok, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "page" {
				for _, a := range t.Attr {
					if a.Name.Local == "number" {
						page = a.Value
					}
				}
			}
			if t.Name.Local == "a" && current == nil {
				for _, a := range t.Attr {
					if a.Name.Local == "href" {
						if u := safeURL(a.Value); u != "" {
							current = &Link{URL: u}
							text.Reset()
							depth = 1
						}
					}
				}
			} else if current != nil {
				depth++
			}
		case xml.CharData:
			if current != nil {
				text.Write(t)
			}
		case xml.EndElement:
			if current != nil {
				depth--
				if depth == 0 {
					current.Quote = strings.TrimSpace(text.String())
					if current.Quote != "" {
						result[page] = append(result[page], *current)
					}
					current = nil
				}
			}
		}
	}
	return result
}

// EnsureLinks uses Poppler's optional pdftohtml annotation reader. Failure to
// read an annotation never prevents preparing or opening the PDF.
func EnsureLinks(ctx context.Context, ws *store.Workspace) (map[string][]Link, error) {
	source, e := ws.SourcePDF()
	if e != nil {
		return nil, e
	}
	stat, e := os.Stat(source)
	if e != nil {
		return nil, e
	}
	identity := fmt.Sprintf("poppler-links-v1:%s:%d:%d", source, stat.Size(), stat.ModTime().UnixNano())
	path := filepath.Join(ws.Root, "extract", "pdf-links-go.json")
	var saved struct {
		Source string            `json:"source"`
		Pages  map[string][]Link `json:"pages"`
	}
	if b, e := os.ReadFile(path); e == nil && json.Unmarshal(b, &saved) == nil && saved.Source == identity {
		return saved.Pages, nil
	}
	if _, e = executable("pdftohtml"); e != nil {
		return map[string][]Link{}, nil
	}
	b, e := run(ctx, "pdfinfo", source)
	if e != nil {
		return nil, e
	}
	m := countRE.FindStringSubmatch(string(b))
	if len(m) != 2 {
		return map[string][]Link{}, nil
	}
	n, _ := strconv.Atoi(m[1])
	if n < 1 || n > 100000 {
		return nil, errors.New("invalid PDF page count")
	}
	all := map[string][]Link{}
	for p := 1; p <= n; p++ {
		data, e := run(ctx, "pdftohtml", "-f", strconv.Itoa(p), "-l", strconv.Itoa(p), "-xml", "-stdout", "-i", "-hidden", "-zoom", "1", source)
		if e != nil {
			return nil, e
		}
		for key, links := range parseLinks(data) {
			all[key] = append(all[key], links...)
		}
	}
	saved.Source = identity
	saved.Pages = all
	return all, writeJSON(path, saved)
}

var protectedText = regexp.MustCompile("(?s)```.*?```|`[^`]*`|\\$\\$.*?\\$\\$|\\$[^$]*\\$|\\[[^\\]\\n]*\\]\\([^\\n]*?\\)")

func linkText(text string, links []Link) string {
	for _, link := range links {
		if link.Quote == "" || safeURL(link.URL) == "" {
			continue
		}
		ranges := protectedText.FindAllStringIndex(text, -1)
		start := 0
		matches := []int{}
		for {
			n := strings.Index(text[start:], link.Quote)
			if n < 0 {
				break
			}
			n += start
			protected := false
			for _, r := range ranges {
				if n < r[1] && n+len(link.Quote) > r[0] {
					protected = true
					break
				}
			}
			if !protected {
				matches = append(matches, n)
			}
			start = n + len(link.Quote)
			if start >= len(text) {
				break
			}
		}
		if len(matches) != 1 {
			continue
		}
		n := matches[0]
		label := strings.NewReplacer("[", "\\[", "]", "\\]").Replace(link.Quote)
		text = text[:n] + "[" + label + "](" + link.URL + ")" + text[n+len(link.Quote):]
	}
	return text
}
func BackfillLinks(ctx context.Context, ws *store.Workspace) error {
	pages, e := EnsureLinks(ctx, ws)
	if e != nil {
		return e
	}
	if len(pages) == 0 {
		return nil
	}
	return ws.Update("paper", func(p map[string]any) error {
		for _, b := range blocks(p) {
			links := pages[strconv.Itoa(int(number(b["page"])))]
			for _, key := range []string{"en", "zh", "caption_en", "caption_zh"} {
				if s, ok := b[key].(string); ok {
					b[key] = linkText(s, links)
				}
			}
			if items, ok := b["items"].([]any); ok {
				for _, v := range items {
					if it, ok := v.(map[string]any); ok {
						for _, key := range []string{"en", "zh"} {
							if s, ok := it[key].(string); ok {
								it[key] = linkText(s, links)
							}
						}
					}
				}
			}
		}
		return nil
	})
}
