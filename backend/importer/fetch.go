// Package importer resolves scholarly sources and imports local collections.
package importer

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

type M = map[string]any

func obj(v any) M {
	m, _ := v.(map[string]any)
	if m == nil {
		return M{}
	}
	return m
}
func str(v any) string { s, _ := v.(string); return s }
func arr(v any) []any  { a, _ := v.([]any); return a }
func first(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

var HTTPClient = &http.Client{Timeout: 120 * time.Second}

const maxPDF = 200 << 20
const s2 = "https://api.semanticscholar.org/graph/v1/paper/"
const fields = "title,authors,year,venue,publicationDate,externalIds,openAccessPdf,abstract,url"

var arxivRE = regexp.MustCompile(`(?i)(\d{4}\.\d{4,5}(?:v\d+)?|[a-z-]+(?:\.[A-Z]{2})?/\d{7}(?:v\d+)?)`)
var doiRE = regexp.MustCompile(`(?i)10\.\d{4,9}/[^\s"<>]+`)

func get(ctx context.Context, u string, limit int64) ([]byte, string, error) {
	parsed, e := url.Parse(u)
	if e != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil, "", errors.New("valid HTTP(S) source URL required")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", u, nil)
	if e != nil {
		return nil, "", e
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 EasyRead/Go")
	req.Header.Set("Accept", "application/pdf,text/html,application/json,*/*")
	resp, e := HTTPClient.Do(req)
	if e != nil {
		return nil, "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("source HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return nil, "", e
	}
	if int64(len(b)) > limit {
		return nil, "", errors.New("source exceeds size limit")
	}
	return b, resp.Request.URL.String(), nil
}
func pdf(ctx context.Context, u string) ([]byte, error) {
	b, _, e := get(ctx, u, maxPDF)
	if e != nil {
		return nil, e
	}
	if !strings.HasPrefix(string(b[:min(4, len(b))]), "%PDF") {
		return nil, errors.New("source did not return PDF")
	}
	return b, nil
}
func jsonGet(ctx context.Context, u string) (M, error) {
	b, _, e := get(ctx, u, 5<<20)
	if e != nil {
		return nil, e
	}
	var m M
	e = json.Unmarshal(b, &m)
	return m, e
}
func name(u string) string {
	p, e := url.Parse(u)
	if e == nil {
		u = p.Path
	}
	n, _ := url.PathUnescape(path.Base(strings.TrimRight(u, "/")))
	n = regexp.MustCompile(`[^\pL\pN._-]+`).ReplaceAllString(n, "_")
	if n == "" || n == "." {
		n = "paper"
	}
	r := []rune(n)
	if len(r) > 80 {
		n = string(r[:80])
	}
	if !strings.HasSuffix(strings.ToLower(n), ".pdf") {
		n += ".pdf"
	}
	return n
}
func arxiv(ctx context.Context, aid string) ([]byte, string, M, error) {
	b, e := pdf(ctx, "https://arxiv.org/pdf/"+aid)
	if e != nil {
		base := regexp.MustCompile(`v\d+$`).ReplaceAllString(aid, "")
		if base != aid {
			b, e = pdf(ctx, "https://arxiv.org/pdf/"+base)
		}
	}
	if e != nil {
		return nil, "", nil, e
	}
	meta := M{"arxiv": "arXiv:" + aid, "url": "https://arxiv.org/abs/" + aid}
	if raw, _, er := get(ctx, "https://export.arxiv.org/api/query?id_list="+url.QueryEscape(aid), 2<<20); er == nil {
		var feed struct {
			Entries []struct {
				Title     string `xml:"title"`
				Summary   string `xml:"summary"`
				Published string `xml:"published"`
				DOI       string `xml:"doi"`
				Authors   []struct {
					Name string `xml:"name"`
				} `xml:"author"`
			} `xml:"entry"`
		}
		if xml.Unmarshal(raw, &feed) == nil && len(feed.Entries) > 0 {
			en := feed.Entries[0]
			authors := []string{}
			for _, a := range en.Authors {
				authors = append(authors, strings.Join(strings.Fields(a.Name), " "))
			}
			meta["title_en"] = strings.Join(strings.Fields(en.Title), " ")
			meta["abstract_en"] = strings.Join(strings.Fields(en.Summary), " ")
			meta["authors"] = strings.Join(authors, ", ")
			meta["doi"] = en.DOI
			if len(en.Published) >= 10 {
				meta["date"] = en.Published[:10]
				meta["year"] = en.Published[:4]
			}
		}
	}
	return b, name(aid), meta, nil
}
func fromS2(ctx context.Context, p M) ([]byte, string, M, error) {
	if len(p) == 0 {
		return nil, "", nil, errors.New("paper not found on Semantic Scholar")
	}
	ext := obj(p["externalIds"])
	authors := []string{}
	for _, a := range arr(p["authors"]) {
		authors = append(authors, str(obj(a)["name"]))
	}
	meta := M{"title_en": p["title"], "authors": strings.Join(authors, ", "), "year": fmt.Sprint(p["year"]), "date": p["publicationDate"], "venue": p["venue"], "abstract_en": p["abstract"], "url": p["url"]}
	if doi := str(ext["DOI"]); doi != "" {
		meta["doi"] = doi
		meta["url"] = "https://doi.org/" + doi
	}
	if aid := str(ext["ArXiv"]); aid != "" {
		b, n, m, e := arxiv(ctx, aid)
		if e == nil {
			for _, k := range []string{"doi", "venue"} {
				if meta[k] != nil {
					m[k] = meta[k]
				}
			}
		}
		return b, n, m, e
	}
	if u := str(obj(p["openAccessPdf"])["url"]); u != "" {
		b, e := pdf(ctx, u)
		if e == nil {
			return b, name(u), meta, nil
		}
		b, n, m, e := fromPage(ctx, u, false)
		for k, v := range meta {
			m[k] = v
		}
		return b, n, m, e
	}
	return nil, "", nil, errors.New("paper found but no open-access PDF; download it from your publisher or library")
}

var tagRE = regexp.MustCompile(`(?is)<meta\s+[^>]*>`)
var attrRE = regexp.MustCompile(`(?i)([\w:-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

func pageMeta(s string) map[string][]string {
	tags := map[string][]string{}
	for _, tag := range tagRE.FindAllString(s, -1) {
		a := map[string]string{}
		for _, m := range attrRE.FindAllStringSubmatch(tag, -1) {
			a[strings.ToLower(m[1])] = html.UnescapeString(first(m[2], m[3]))
		}
		key := strings.ToLower(first(a["name"], a["property"]))
		if key != "" {
			tags[key] = append(tags[key], a["content"])
		}
	}
	return tags
}
func fromPage(ctx context.Context, u string, lookup bool) ([]byte, string, M, error) {
	b, final, e := get(ctx, u, maxPDF)
	if e != nil {
		return nil, "", M{}, e
	}
	if strings.HasPrefix(string(b[:min(len(b), 4)]), "%PDF") {
		return b, name(final), M{"url": u}, nil
	}
	s := string(b[:min(len(b), 3_000_000)])
	tags := pageMeta(s)
	one := func(k string) string {
		if len(tags[k]) > 0 {
			return strings.TrimSpace(tags[k][0])
		}
		return ""
	}
	authors := []string{}
	for _, a := range tags["citation_author"] {
		p := strings.SplitN(a, ",", 2)
		if len(p) == 2 {
			a = strings.TrimSpace(p[1]) + " " + strings.TrimSpace(p[0])
		}
		authors = append(authors, a)
	}
	meta := M{"title_en": first(one("citation_title"), one("dc.title"), one("og:title")), "authors": strings.Join(authors, ", "), "date": first(one("citation_publication_date"), one("citation_date"), one("dc.date")), "venue": first(one("citation_journal_title"), one("citation_conference_title")), "doi": first(one("citation_doi"), strings.TrimPrefix(one("dc.identifier"), "doi:")), "url": u}
	meta["year"] = regexp.MustCompile(`(?:19|20)\d{2}`).FindString(str(meta["date"]))
	link := one("citation_pdf_url")
	if link == "" {
		m := regexp.MustCompile(`(?i)href=["']([^"']+\.pdf(?:\?[^"']*)?)["']`).FindStringSubmatch(s)
		if len(m) > 0 {
			link = m[1]
		}
	}
	if link != "" {
		base, _ := url.Parse(final)
		rel, er := url.Parse(html.UnescapeString(link))
		if er == nil {
			link = base.ResolveReference(rel).String()
			raw, er := pdf(ctx, link)
			if er == nil {
				return raw, name(link), meta, nil
			}
		}
	}
	if lookup && str(meta["doi"]) != "" {
		p, er := jsonGet(ctx, s2+"DOI:"+url.PathEscape(str(meta["doi"]))+"?fields="+fields)
		if er == nil {
			return fromS2(ctx, p)
		}
	}
	return nil, "", meta, errors.New("no downloadable PDF found on page")
}
func Fetch(ctx context.Context, ref string) ([]byte, string, M, error) {
	ref = strings.Trim(strings.TrimSpace(ref), "<>")
	if ref == "" {
		return nil, "", nil, errors.New("enter a URL, arXiv ID, DOI or paper title")
	}
	low := strings.ToLower(ref)
	isURL := strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://")
	aid := arxivRE.FindString(ref)
	if aid != "" {
		ok := !isURL && (strings.EqualFold(ref, aid) || strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(low, "arxiv:")), strings.ToLower(aid)))
		for _, host := range []string{"arxiv.org", "alphaxiv.org", "huggingface.co/papers", "hf.co/papers", "papers.cool/arxiv", "paperswithcode.com", "arxiv-sanity", "papers.labml.ai"} {
			ok = ok || (isURL && strings.Contains(low, host))
		}
		if ok {
			return arxiv(ctx, aid)
		}
	}
	doi := strings.TrimRight(doiRE.FindString(ref), ".,;)]")
	if doi != "" && (!isURL || strings.Contains(low, "doi.org")) {
		p, e := jsonGet(ctx, s2+"DOI:"+url.PathEscape(doi)+"?fields="+fields)
		if e == nil {
			return fromS2(ctx, p)
		}
		return fromPage(ctx, "https://doi.org/"+doi, false)
	}
	if isURL {
		if m := regexp.MustCompile(`(?i)(https?://(?:www\.)?(?:biorxiv|medrxiv)\.org/content/[\w./-]+?)(?:\.full(?:\.pdf)?|\.abstract)?(?:[?#].*)?$`).FindStringSubmatch(ref); len(m) > 0 {
			b, e := pdf(ctx, m[1]+".full.pdf")
			return b, name(m[1]), M{"url": m[1]}, e
		}
		if m := regexp.MustCompile(`PMC\d+`).FindString(ref); m != "" && strings.Contains(low, "ncbi.nlm.nih.gov") {
			p, e := jsonGet(ctx, s2+"PMCID:"+m+"?fields="+fields)
			if e == nil {
				return fromS2(ctx, p)
			}
		}

		if m := regexp.MustCompile(`openreview\.net/(?:forum|pdf)\?id=([\w-]+)`).FindStringSubmatch(ref); len(m) > 0 {
			b, e := pdf(ctx, "https://openreview.net/pdf?id="+m[1])
			return b, "openreview-" + m[1] + ".pdf", M{"url": "https://openreview.net/forum?id=" + m[1], "venue": "OpenReview"}, e
		}
		if m := regexp.MustCompile(`aclanthology\.org/([\w.-]+?)(?:\.pdf)?/?$`).FindStringSubmatch(ref); len(m) > 0 {
			b, e := pdf(ctx, "https://aclanthology.org/"+m[1]+".pdf")
			return b, m[1] + ".pdf", M{"url": "https://aclanthology.org/" + m[1] + "/", "venue": "ACL Anthology"}, e
		}
		b, n, m, e := fromPage(ctx, ref, true)
		if e != nil && aid != "" {
			return arxiv(ctx, aid)
		}
		return b, n, m, e
	}
	if len([]rune(ref)) < 8 {
		return nil, "", nil, errors.New("unrecognized reference; provide full paper title or URL")
	}
	p, e := jsonGet(ctx, s2+"search/match?query="+url.QueryEscape(ref)+"&fields="+fields)
	if e != nil {
		return nil, "", nil, e
	}
	a := arr(p["data"])
	if len(a) == 0 {
		return nil, "", nil, errors.New("paper not found")
	}
	return fromS2(ctx, obj(a[0]))
}
