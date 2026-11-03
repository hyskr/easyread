package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/hyskr/easyread/backend/store"
	"github.com/hyskr/easyread/backend/translate"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var codeFence = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~|`[^`\n]*`")
var citation = regexp.MustCompile(`\[(\d+(?:\s*[,–-]\s*\d+)*)\]`)
var citeSplit = regexp.MustCompile(`\s*[,–-]\s*`)

func escaped(s string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}
func mathItems(id, s string) (string, []any) {
	s = codeFence.ReplaceAllString(s, " ")
	items := []any{}
	var outside strings.Builder
	for i := 0; i < len(s); {
		open, close := "", ""
		display := false
		if !escaped(s, i) {
			switch {
			case strings.HasPrefix(s[i:], "$$"):
				open = "$$"
				close = "$$"
				display = true
			case strings.HasPrefix(s[i:], `\[`):
				open = `\[`
				close = `\]`
				display = true
			case strings.HasPrefix(s[i:], `\(`):
				open = `\(`
				close = `\)`
			case s[i] == '$':
				open = "$"
				close = "$"
			}
		}
		if open == "" {
			outside.WriteByte(s[i])
			i++
			continue
		}
		end := -1
		for j := i + len(open); j < len(s); j++ {
			if strings.HasPrefix(s[j:], close) && !escaped(s, j) {
				end = j
				break
			}
		}
		if end < 0 {
			outside.WriteByte(s[i])
			i++
			continue
		}
		items = append(items, []any{id, s[i+len(open) : end], display})
		outside.WriteByte(' ')
		i = end + len(close)
	}
	return outside.String(), items
}
func blockTexts(b M) []string {
	out := []string{}
	for _, key := range []string{"en", "zh", "caption_en", "caption_zh", "image_en", "image_zh"} {
		out = append(out, translate.Str(b[key]))
	}
	for _, v := range translate.List(b["items"]) {
		if m, ok := v.(map[string]any); ok {
			out = append(out, translate.Str(m["en"]), translate.Str(m["zh"]))
		} else {
			out = append(out, fmt.Sprint(v))
		}
	}
	for _, key := range []string{"head", "rows"} {
		for _, v := range translate.List(b[key]) {
			if cells, ok := v.([]any); ok {
				for _, cell := range cells {
					out = append(out, fmt.Sprint(cell))
				}
			} else {
				out = append(out, fmt.Sprint(v))
			}
		}
	}
	return out
}
func inspectPaper(ws *store.Workspace) (M, error) {
	p, e := ws.Load("paper")
	if e != nil {
		return nil, e
	}
	disc, e := ws.Load("discussion")
	if e != nil {
		return nil, e
	}
	problems := translate.Problems(p)
	items := []any{}
	refs := map[string]bool{}
	for _, v := range translate.List(p["references"]) {
		id := translate.Obj(v)["id"]
		refs[fmt.Sprint(id)] = true
	}
	ids := map[string]bool{}
	for _, v := range translate.List(p["blocks"]) {
		b := translate.Obj(v)
		id := translate.Str(b["id"])
		ids[id] = true
		if b["type"] == "math" {
			items = append(items, []any{id, translate.Str(b["tex"]), true})
		}
		if b["type"] == "code" || b["type"] == "listing" {
			continue
		}
		for _, text := range blockTexts(b) {
			outside, math := mathItems(id, text)
			items = append(items, math...)
			if len(refs) > 0 {
				for _, m := range citation.FindAllStringSubmatch(outside, -1) {
					for _, n := range citeSplit.Split(m[1], -1) {
						if !refs[n] {
							problems = append(problems, fmt.Sprintf("%s：引用 [%s] 不在参考文献里", id, n))
						}
					}
				}
			}
		}
	}
	for _, v := range translate.List(disc["entries"]) {
		d := translate.Obj(v)
		id := translate.Str(d["id"])
		if anchor := translate.Str(d["anchor"]); anchor != "" && !ids[anchor] {
			problems = append(problems, fmt.Sprintf("讨论 %s 的锚点不存在：%s", id, anchor))
		}
		for _, key := range []string{"body", "q"} {
			_, math := mathItems(id, translate.Str(d[key]))
			items = append(items, math...)
		}
	}
	if len(items) > 0 {
		if node, e := exec.LookPath("node"); e == nil {
			dir, e := materializeAssets()
			if e != nil {
				return nil, e
			}
			defer os.RemoveAll(dir)
			script := `const katex=require(process.argv[1]);let input="";process.stdin.setEncoding("utf8");process.stdin.on("data",x=>input+=x);process.stdin.on("end",()=>{for(const [id,tex,display] of JSON.parse(input)){try{katex.renderToString(tex,{displayMode:display,throwOnError:true,strict:"ignore"})}catch(e){console.log(id+": TeX render failed: "+e.message)}}});`
			cmd := exec.Command(node, "-e", script, filepath.Join(dir, "vendor", "katex", "katex.min.js"))
			payload, _ := json.Marshal(items)
			cmd.Stdin = bytes.NewReader(payload)
			out, e := cmd.CombinedOutput()
			if e != nil {
				problems = append(problems, "TeX check failed to run: "+string(out))
			} else {
				for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					if line != "" {
						problems = append(problems, line)
					}
				}
			}
		}
	}
	done := map[int]bool{}
	for _, v := range translate.List(translate.Obj(p["translation"])["done_pages"]) {
		done[translate.Num(v)] = true
	}
	missing := []int{}
	for _, v := range translate.List(translate.Obj(p["meta"])["pages"]) {
		n := translate.Num(translate.Obj(v)["n"])
		if !done[n] {
			missing = append(missing, n)
		}
	}
	sort.Ints(missing)
	return M{"blocks": len(ids), "tex": len(items), "missing_pages": missing, "problems": problems}, nil
}
func checkPaper(ws *store.Workspace) error {
	v, e := inspectPaper(ws)
	if e != nil {
		return e
	}
	missing := fmt.Sprint(v["missing_pages"])
	if len(v["missing_pages"].([]int)) == 0 {
		missing = "无"
	}
	fmt.Printf("块 %d 个；公式 %d 处；未完成页 %s\n", v["blocks"], v["tex"], missing)
	problems := v["problems"].([]string)
	if len(problems) > 0 {
		fmt.Println("问题：\n- " + strings.Join(problems, "\n- "))
		return fmt.Errorf("检查失败：%d 个问题", len(problems))
	}
	fmt.Println("检查通过")
	return nil
}
