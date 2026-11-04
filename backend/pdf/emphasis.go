package pdf

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"github.com/hyskr/easyread/backend/store"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type emphasisRun struct{ Text, Style string }

func parseEmphasis(data []byte) []emphasisRun {
	decoder := xml.NewDecoder(bytes.NewReader(cleanPDFXML(data)))
	decoder.Strict = false
	bold, italic := 0, 0
	runs := []emphasisRun{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		switch item := token.(type) {
		case xml.StartElement:
			if item.Name.Local == "b" {
				bold++
			}
			if item.Name.Local == "i" {
				italic++
			}
		case xml.EndElement:
			if item.Name.Local == "b" && bold > 0 {
				bold--
			}
			if item.Name.Local == "i" && italic > 0 {
				italic--
			}
		case xml.CharData:
			style := ""
			if bold > 0 {
				style += "**"
			}
			if italic > 0 {
				style += "*"
			}
			text := strings.TrimSpace(string(item))
			if style != "" && text != "" {
				letters := 0
				for _, r := range text {
					if unicode.IsLetter(r) {
						letters++
					}
				}
				if letters >= 2 {
					runs = append(runs, emphasisRun{text, style})
				}
			}
		}
	}
	return runs
}
func insertEmphasis(text string, runs []emphasisRun) string {
	type edit struct {
		start, end int
		mark       string
	}
	edits := []edit{}
	for _, run := range runs {
		words := strings.Fields(run.Text)
		for i := range words {
			words[i] = regexp.QuoteMeta(words[i])
		}
		if len(words) == 0 {
			continue
		}
		pattern := regexp.MustCompile(strings.Join(words, `\s+`))
		matches := pattern.FindAllStringIndex(text, -1)
		if len(matches) != 1 {
			continue
		}
		m := matches[0]
		overlap := false
		for _, other := range edits {
			if m[0] < other.end && m[1] > other.start {
				overlap = true
				break
			}
		}
		if !overlap {
			edits = append(edits, edit{m[0], m[1], run.Style})
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, e := range edits {
		text = text[:e.start] + e.mark + text[e.start:e.end] + e.mark + text[e.end:]
	}
	return text
}

// PromptText decorates model input while keeping plain extraction files for positioning.
func PromptText(ws *store.Workspace, page int) string {
	plain, _ := os.ReadFile(filepath.Join(ws.Root, "extract", fmt.Sprintf("page-%03d.txt", page)))
	text := string(plain)
	source, err := ws.SourcePDF()
	if err != nil {
		return text
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := run(ctx, "pdftohtml", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-xml", "-stdout", "-i", "-hidden", "-zoom", "1", source)
	if err != nil {
		return text
	}
	text = insertEmphasis(text, parseEmphasis(data))
	return insertAnnotationLinks(text, parseAnnotationLinks(data, true)[strconv.Itoa(page)], true)
}
