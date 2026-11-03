// Package exporter builds portable offline reader and conversation exports.
package exporter

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hyskr/easyread/backend/chat"
	"github.com/hyskr/easyread/backend/store"
	"html"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
func arr(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	if a, ok := v.([]M); ok {
		r := []any{}
		for _, m := range a {
			r = append(r, m)
		}
		return r
	}
	if a, ok := v.([]string); ok {
		r := []any{}
		for _, s := range a {
			r = append(r, s)
		}
		return r
	}
	return nil
}
func firstString(values ...string) string {
	for _, s := range values {
		if s != "" {
			return s
		}
	}
	return ""
}
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
func safe(root, rel string) (string, error) {
	if filepath.IsAbs(rel) || strings.Contains(rel, "\\") || strings.Contains("/"+rel+"/", "/../") {
		return "", errors.New("invalid asset path")
	}
	root, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", e
	}
	p, e := filepath.EvalSymlinks(filepath.Join(root, rel))
	if e != nil {
		return "", e
	}
	r, e := filepath.Rel(root, p)
	if e != nil || r == ".." || strings.HasPrefix(r, ".."+string(os.PathSeparator)) {
		return "", errors.New("asset escapes workspace")
	}
	return p, nil
}
func uri(p string) (string, error) {
	b, e := os.ReadFile(p)
	if e != nil {
		return "", e
	}
	m := mime.TypeByExtension(filepath.Ext(p))
	if m == "" {
		m = "application/octet-stream"
	}
	return "data:" + m + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}
func HTML(ws *store.Workspace, webRoot string, mobile bool) ([]byte, string, error) {
	return HTMLWithLanguage(ws, webRoot, mobile, "zh", "zh")
}

// HTMLWithLanguage applies the configured UI locale and translation target to a portable reader.
func HTMLWithLanguage(ws *store.Workspace, webRoot string, mobile bool, language, target string) ([]byte, string, error) {
	raw, e := os.ReadFile(filepath.Join(webRoot, "reader.html"))
	if e != nil {
		return nil, "", e
	}
	page := string(raw)
	choice := language
	if choice == "" {
		choice = "auto"
	}
	if language == "" || language == "auto" {
		locale := firstString(os.Getenv("EASYREAD_LANG"), os.Getenv("EASYREAD_SYSTEM_LANG"), os.Getenv("LANG"))
		language = "en"
		if strings.HasPrefix(strings.ToLower(locale), "zh") {
			language = "zh"
			if strings.Contains(strings.ToLower(locale), "hant") || strings.Contains(strings.ToLower(locale), "tw") || strings.Contains(strings.ToLower(locale), "hk") {
				language = "zh-Hant"
			}
		}
	}
	if language != "zh" && language != "zh-Hant" && language != "en" {
		language = "en"
	}
	if target == "" {
		target = "zh"
	}
	tag := language
	if tag == "zh" {
		tag = "zh-CN"
	}
	page = strings.Replace(page, `<html lang="zh-CN">`, fmt.Sprintf(`<html lang="%s" data-lang-choice="%s" data-target="%s">`, html.EscapeString(tag), html.EscapeString(choice), html.EscapeString(target)), 1)
	if language != "zh" {
		b, er := os.ReadFile(filepath.Join(webRoot, "i18n", language+".json"))
		if er != nil {
			return nil, "", er
		}
		var dictionary any
		if er = json.Unmarshal(b, &dictionary); er != nil {
			return nil, "", er
		}
		b, _ = json.Marshal(dictionary)
		page = strings.Replace(page, "</title>", "</title>\n<script id=\"pr-i18n\" type=\"application/json\">"+string(b)+"</script>", 1)
	}
	if mobile {
		page = strings.Replace(page, "</head>", "<link rel=\"stylesheet\" href=\"/web/css/reader-mobile.css\">\n</head>", 1)
		page = strings.Replace(page, `<script src="/web/js/reader/main.js">`, `<script src="/web/js/reader/mobile.js"></script><script src="/web/js/reader/main.js">`, 1)
	}
	var failed error
	link := regexp.MustCompile(`<link rel="stylesheet" href="/web/([^"]+)"\s*/?>`)
	page = link.ReplaceAllStringFunc(page, func(tag string) string {
		rel := link.FindStringSubmatch(tag)[1]
		p, er := safe(webRoot, rel)
		if er != nil {
			failed = er
			return tag
		}
		b, er := os.ReadFile(p)
		if er != nil {
			failed = er
			return tag
		}
		css := string(b)
		// Only WOFF2 fonts ship with the application. Remove legacy fallback URLs.
		css = regexp.MustCompile(`,\s*url\(fonts/[^)]+\.(?:woff|ttf)\)\s*format\(["'](?:woff|truetype)["']\)`).ReplaceAllString(css, "")
		fonts := regexp.MustCompile(`url\(["']?(fonts/[^)"']+)["']?\)`)
		css = fonts.ReplaceAllStringFunc(css, func(v string) string {
			f, er := safe(filepath.Dir(p), fonts.FindStringSubmatch(v)[1])
			if er != nil {
				failed = er
				return v
			}
			u, er := uri(f)
			if er != nil {
				failed = er
				return v
			}
			return "url(" + u + ")"
		})
		return "<style>" + css + "</style>"
	})
	script := regexp.MustCompile(`<script src="/web/([^"]+)"></script>`)
	page = script.ReplaceAllStringFunc(page, func(tag string) string {
		p, er := safe(webRoot, script.FindStringSubmatch(tag)[1])
		if er != nil {
			failed = er
			return tag
		}
		b, er := os.ReadFile(p)
		if er != nil {
			failed = er
			return tag
		}
		return "<script>" + strings.ReplaceAll(string(b), "</script", "<\\/script") + "</script>"
	})
	if failed != nil {
		return nil, "", failed
	}
	if u, er := uri(filepath.Join(webRoot, "favicon.svg")); er == nil {
		page = strings.ReplaceAll(page, `href="/web/favicon.svg"`, `href="`+u+`"`)
	}
	paper, e := ws.Load("paper")
	if e != nil {
		return nil, "", e
	}
	meta := obj(paper["meta"])
	images := M{}
	rels := []string{}
	done := map[string]bool{}
	if mobile {
		for _, v := range arr(obj(paper["translation"])["done_pages"]) {
			done[fmt.Sprint(v)] = true
		}
	}
	for _, v := range arr(meta["pages"]) {
		p := obj(v)
		if !done[fmt.Sprint(p["n"])] {
			rels = append(rels, str(p["img"]))
		}
	}
	for _, v := range arr(paper["blocks"]) {
		b := obj(v)
		rels = append(rels, str(b["src"]), str(obj(b["translated_image"])["src"]))
	}
	for _, rel := range rels {
		if rel == "" {
			continue
		}
		p, er := safe(ws.Root, rel)
		if er != nil {
			continue
		}
		u, er := uri(p)
		if er != nil {
			return nil, "", er
		}
		images[rel] = u
	}
	data := M{"paper": paper, "images": images}
	for _, n := range []string{"discussion", "reader", "layout", "item"} {
		data[n], e = ws.Load(n)
		if e != nil {
			return nil, "", e
		}
	}
	if mobile {
		data["layout"] = M{}
	}
	payload, e := json.Marshal(data)
	if e != nil {
		return nil, "", e
	}
	page = strings.Replace(page, "<!--PR:DATA-->", `<script id="pr-data" type="application/json">`+string(payload)+`</script>`, 1)
	title := str(meta["title_zh"])
	if title == "" {
		title = str(meta["title_en"])
	}
	if title == "" {
		title = "论文"
	}
	page = regexp.MustCompile(`<title>.*?</title>`).ReplaceAllStringFunc(page, func(string) string { return "<title>" + html.EscapeString(title) + "</title>" })
	stem := str(meta["short_zh"])
	if stem == "" {
		stem = ws.ID()
	}
	stem = regexp.MustCompile(`[\\/:*?"<>|]`).ReplaceAllString(stem, "")
	suffix := "-离线版.html"
	if mobile {
		suffix = "-手机版.html"
	}
	return []byte(page), stem + suffix, nil
}
func Markdown(ws *store.Workspace, body M) (M, error) {
	threads, e := chat.Threads(ws)
	if e != nil {
		return nil, e
	}
	var thread M
	for _, t := range threads {
		if t["id"] == body["thread"] {
			thread = t
		}
	}
	ms := arr(thread["messages"])
	var answer, question M
	for i, v := range ms {
		a := obj(v)
		if a["id"] == body["id"] && a["role"] == "assistant" {
			answer = a
			for j := i - 1; j >= 0; j-- {
				if obj(ms[j])["role"] == "user" {
					question = obj(ms[j])
					break
				}
			}
			break
		}
	}
	if answer == nil {
		return nil, errors.New("Answer not found")
	}
	paper, e := ws.Load("paper")
	if e != nil {
		return nil, e
	}
	meta := obj(paper["meta"])
	title := str(meta["title_zh"])
	if title == "" {
		title = str(meta["title_en"])
	}
	if title == "" {
		title = "Paper"
	}
	subject := clip(strings.Join(strings.Fields(str(question["content"])), " "), 60)
	if subject == "" {
		subject = str(thread["title"])
	}
	if subject == "" {
		subject = "Note"
	}
	short := str(meta["short_zh"])
	if short == "" {
		short = title
	}
	filename := strings.Trim(regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`).ReplaceAllString(clip(short, 45)+"-"+subject, "-"), " .")
	if filename == "" {
		filename = "note"
	}
	assets := map[string]string{}
	local := func(value string) string {
		rel, e := url.PathUnescape(value)
		if e != nil {
			return value
		}
		rel = strings.TrimPrefix(rel, "/p/"+ws.ID()+"/")
		parts := strings.Split(rel, "/")
		if len(parts) < 2 || (parts[0] != "chat-media" && parts[0] != "figures" && parts[0] != "pages") {
			return value
		}
		ext := strings.ToLower(filepath.Ext(rel))
		if !strings.Contains("|.png|.jpg|.jpeg|.webp|.gif|", "|"+ext+"|") {
			return value
		}
		p, e := safe(ws.Root, rel)
		if e != nil {
			return value
		}
		dest := "assets/" + filepath.Base(p)
		if old, ok := assets[dest]; ok && old != p {
			dest = "assets/" + parts[0] + "-" + filepath.Base(p)
		}
		assets[dest] = p
		return dest
	}
	im := regexp.MustCompile(`(!\[[^\]]*\]\()([^\s)]+)(\))`)
	rewrite := func(text string) string {
		return im.ReplaceAllStringFunc(text, func(s string) string { m := im.FindStringSubmatch(s); return m[1] + local(m[2]) + m[3] })
	}
	lines := []string{"# " + subject, "", "来源论文：" + strings.Join(strings.Fields(title), " ")}
	source := str(meta["url"])
	if source == "" {
		source = str(meta["source_url"])
	}
	if source == "" && str(meta["doi"]) != "" {
		source = "https://doi.org/" + strings.TrimPrefix(str(meta["doi"]), "https://doi.org/")
	}
	if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://") {
		lines = append(lines, "论文链接："+source)
	}
	if str(answer["at"]) != "" {
		lines = append(lines, "记录时间："+str(answer["at"]))
	}
	lines = append(lines, "", "## 我的问题", "", rewrite(str(question["content"])))
	for i, v := range arr(question["images"]) {
		lines = append(lines, "", fmt.Sprintf("![提问附图 %d](%s)", i+1, local(str(v))))
	}
	lines = append(lines, "", "## AI 回答", "", rewrite(str(answer["content"])), "")
	payload := []byte(strings.Join(lines, "\n"))
	suffix, mimetype := ".md", "text/markdown;charset=utf-8"
	if len(assets) > 0 {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		f, er := z.Create(filename + ".md")
		if er != nil {
			return nil, er
		}
		if _, er = f.Write(payload); er != nil {
			return nil, er
		}
		for dest, p := range assets {
			raw, er := os.ReadFile(p)
			if er != nil {
				return nil, er
			}
			f, er = z.Create(dest)
			if er != nil {
				return nil, er
			}
			if _, er = f.Write(raw); er != nil {
				return nil, er
			}
		}
		if er := z.Close(); er != nil {
			return nil, er
		}
		payload = b.Bytes()
		suffix = ".zip"
		mimetype = "application/zip"
	}
	return M{"filename": filename + suffix, "mime": mimetype, "data": base64.StdEncoding.EncodeToString(payload)}, nil
}
