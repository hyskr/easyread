package store

import (
	"crypto/sha256"
	"fmt"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var citationDOI = regexp.MustCompile(`(?i)\b10\.\d{4,9}/[^\s"<>\[\]]+`)
var citationArxiv = regexp.MustCompile(`(?i)(?:arxiv\s*:\s*|arxiv\.org/(?:abs|pdf)/)(\d{4}\.\d{4,5})(?:v\d+)?`)

func citationIDs(s string, arxiv bool) map[string]bool {
	result := map[string]bool{}
	if arxiv {
		for _, m := range citationArxiv.FindAllStringSubmatch(s, -1) {
			result[strings.ToLower(m[1])] = true
		}
		return result
	}
	for _, id := range citationDOI.FindAllString(s, -1) {
		id = strings.TrimRight(strings.ToLower(id), ".,;:")
		for strings.HasSuffix(id, ")") && strings.Count(id, ")") > strings.Count(id, "(") {
			id = strings.TrimRight(strings.TrimSuffix(id, ")"), ".,;:")
		}
		result[id] = true
	}
	return result
}
func citationWords(s string) string {
	return strings.Join(strings.FieldsFunc(cases.Fold().String(norm.NFKC.String(s)), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }), " ")
}
func intersects(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}
func CitationFingerprint(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

type citationPaper struct {
	id, title, titleEN string
	doi, arxiv         map[string]bool
	refs               []any
	links              Object
}

func (l *Library) CitationGraph(pid string) (Object, error) {
	workspaces, err := l.All()
	if err != nil {
		return nil, err
	}
	papers := map[string]citationPaper{}
	incomplete := false
	for _, ws := range workspaces {
		paper, e := ws.Load("paper")
		item, ie := ws.Load("item")
		if e != nil || ie != nil {
			if ws.ID() == pid {
				if e != nil {
					return nil, e
				}
				return nil, ie
			}
			incomplete = true
			continue
		}
		meta := obj(paper["meta"])
		for k, v := range obj(item["meta_override"]) {
			if v != "" && v != nil || k == "doi" {
				meta[k] = v
			}
		}
		title := str(meta["title_zh"])
		if title == "" {
			title = str(meta["title_en"])
		}
		if title == "" {
			title = ws.ID()
		}
		papers[ws.ID()] = citationPaper{ws.ID(), title, str(meta["title_en"]), citationIDs(str(meta["doi"]), false), citationIDs(str(meta["arxiv"]), true), array(paper["references"]), obj(item["citation_links"])}
	}
	current, ok := papers[pid]
	if !ok {
		return nil, fmt.Errorf("paper not found: %s", pid)
	}
	outward := map[string][]any{}
	inward := map[string][]any{}
	for source, paper := range papers {
		outward[source] = []any{}
		for _, raw := range paper.refs {
			ref, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id := fmt.Sprint(ref["id"])
			content := str(ref["text"])
			doi, arxiv := citationIDs(content, false), citationIDs(content, true)
			target := ""
			manual := obj(paper.links[id])
			if str(manual["text"]) == content {
				target = str(manual["target"])
			}
			if target == "" {
				previous := map[string]bool{}
				for _, v := range paper.links {
					link := obj(v)
					if str(link["text"]) == content {
						if _, ok := papers[str(link["target"])]; ok {
							previous[str(link["target"])] = true
						}
					}
				}
				if len(previous) == 1 {
					for t := range previous {
						target = t
					}
				}
			}
			if _, ok := papers[target]; !ok || target == source {
				target = ""
				identifiers := map[string]bool{}
				titles := map[string]bool{}
				for other, p := range papers {
					if other == source {
						continue
					}
					compatible := !(len(doi) > 0 && len(p.doi) > 0 && !intersects(doi, p.doi) || len(arxiv) > 0 && len(p.arxiv) > 0 && !intersects(arxiv, p.arxiv))
					if intersects(doi, p.doi) || intersects(arxiv, p.arxiv) {
						identifiers[other] = compatible
					}
					title := citationWords(p.titleEN)
					if compatible && len([]rune(title)) >= 16 && len(strings.Fields(title)) >= 3 && strings.Contains(" "+citationWords(content)+" ", " "+title+" ") {
						titles[other] = true
					}
				}
				if len(identifiers) == 1 {
					for k, valid := range identifiers {
						if valid {
							target = k
						}
					}
				} else if len(identifiers) == 0 && len(titles) == 1 {
					for k := range titles {
						target = k
					}
				}
			}
			row := Object{"id": id, "text": content, "target": nil, "fingerprint": CitationFingerprint(content)}
			if key := str(ref["cite_key"]); key != "" {
				row["cite_key"] = key
			}
			if len(doi) == 1 {
				for d := range doi {
					row["source_url"] = "https://doi.org/" + strings.ReplaceAll(url.PathEscape(d), "%2F", "/")
				}
			} else if len(arxiv) == 1 {
				for d := range arxiv {
					row["source_url"] = "https://arxiv.org/abs/" + d
				}
			}
			if target != "" {
				row["target"] = target
				row["title"] = papers[target].title
				inward[target] = append(inward[target], Object{"id": source, "title": paper.title, "ref_id": id})
			}
			outward[source] = append(outward[source], row)
		}
	}
	cited := inward[pid]
	if cited == nil {
		cited = []any{}
	}
	sort.Slice(cited, func(i, j int) bool { return str(obj(cited[i])["id"]) < str(obj(cited[j])["id"]) })
	return Object{"paper": Object{"id": pid, "title": current.title}, "references": outward[pid], "cited_by": cited, "incomplete": incomplete}, nil
}
func (l *Library) LinkReference(ws *Workspace, refID, fingerprint, target string) error {
	if target == ws.ID() {
		return fmt.Errorf("目标论文不在文献库中")
	}
	if _, err := l.Workspace(target); err != nil {
		return err
	}
	return ws.Update("item", func(item Object) error {
		paper, err := ws.Load("paper")
		if err != nil {
			return err
		}
		matches := []Object{}
		for _, v := range array(paper["references"]) {
			r := obj(v)
			if fmt.Sprint(r["id"]) == refID {
				matches = append(matches, r)
			}
		}
		if len(matches) != 1 || CitationFingerprint(str(matches[0]["text"])) != fingerprint {
			return fmt.Errorf("参考文献已变化，请重新选择条目")
		}
		links := obj(item["citation_links"])
		if links == nil {
			links = Object{}
		}
		links[refID] = Object{"text": str(matches[0]["text"]), "target": target}
		item["citation_links"] = links
		return nil
	})
}
