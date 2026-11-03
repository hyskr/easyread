package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hyskr/easyread/backend/engines"
	"github.com/hyskr/easyread/backend/store"
)

var pdfJobs = struct {
	sync.Mutex
	active map[string]context.CancelFunc
}{active: map[string]context.CancelFunc{}}

func (a *App) PDFBusy() bool {
	pdfJobs.Lock()
	defer pdfJobs.Unlock()
	for root := range pdfJobs.active {
		if rel, e := filepath.Rel(a.Library.Root, root); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
func (a *App) PDFClose() {
	pdfJobs.Lock()
	defer pdfJobs.Unlock()
	for root, cancel := range pdfJobs.active {
		if rel, e := filepath.Rel(a.Library.Root, root); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			cancel()
		}
	}
}
func pdfRecord(ws *store.Workspace) string { return filepath.Join(ws.Root, "babeldoc", "status.json") }

// Caller holds pdfJobs lock, ensuring status never mistakes a just-starting job for a restart.
func pdfStatus(ws *store.Workspace) (Object, error) {
	v, e := store.ReadJSON(pdfRecord(ws))
	if e != nil {
		return nil, e
	}
	if v == nil {
		return Object{"state": "idle", "files": []string{}}, nil
	}
	if v["state"] == "running" && pdfJobs.active[ws.Root] == nil {
		v["state"] = "error"
		v["message"] = "EasyRead 已重启，任务已中断。请重新开始。"
		v["updated"] = store.Now()
		e = store.WriteJSON(pdfRecord(ws), v)
	}
	return v, e
}
func pdfFile(ws *store.Workspace, record Object, job, name string) (string, error) {
	if record["state"] != "done" || text(record["job_id"]) != job || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(job) || filepath.Base(name) != name || strings.ContainsAny(name, "/\\") || !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		return "", os.ErrNotExist
	}
	found := false
	for _, v := range array(record["files"]) {
		if v == name {
			found = true
		}
	}
	if files, ok := record["files"].([]string); ok {
		for _, v := range files {
			if v == name {
				found = true
			}
		}
	}
	if !found {
		return "", os.ErrNotExist
	}
	p := filepath.Join(ws.Root, "babeldoc", job, name)
	st, e := os.Lstat(p)
	if e != nil {
		return "", e
	}
	if !st.Mode().IsRegular() {
		return "", os.ErrNotExist
	}
	return p, nil
}
func (a *App) pdfExportGet(w http.ResponseWriter, r *http.Request, ws *store.Workspace, parts []string) error {
	pdfJobs.Lock()
	v, e := pdfStatus(ws)
	pdfJobs.Unlock()
	if e != nil {
		return e
	}
	if len(parts) == 0 {
		response(w, 200, v)
		return nil
	}
	if len(parts) != 2 {
		return os.ErrNotExist
	}
	p, e := pdfFile(ws, v, parts[0], parts[1])
	if e != nil {
		return e
	}
	w.Header().Set("Content-Type", "application/pdf")
	http.ServeFile(w, r, p)
	return nil
}
func pdfOutputDir(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "~/") {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		value = filepath.Join(home, value[2:])
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("请选择已存在的绝对输出目录")
	}
	value, e := filepath.EvalSymlinks(value)
	if e != nil {
		return "", e
	}
	st, e := os.Stat(value)
	if e != nil || !st.IsDir() {
		return "", fmt.Errorf("请选择已存在的输出目录")
	}
	f, e := os.CreateTemp(value, ".easyread-write-test-*")
	if e != nil {
		return "", fmt.Errorf("输出目录无法写入")
	}
	f.Close()
	os.Remove(f.Name())
	return value, nil
}
func pdfCopy(source, dest string) (string, error) {
	base := filepath.Base(source)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	for i := 1; i <= 10000; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s (%d).pdf", stem, i)
		}
		target := filepath.Join(dest, name)
		f, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(e, os.ErrExist) {
			continue
		}
		if e != nil {
			return "", e
		}
		src, e := os.Open(source)
		if e == nil {
			_, e = io.Copy(f, src)
			src.Close()
		}
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			os.Remove(target)
			return "", e
		}
		return target, nil
	}
	return "", fmt.Errorf("输出目录中同名文件过多")
}
func pdfSave(ws *store.Workspace, record Object, dest string) Object {
	record["output_dir"] = dest
	record["saved_files"] = []string{}
	record["updated"] = store.Now()
	delete(record, "save_error")
	if dest == "" {
		return record
	}
	names := []string{}
	for _, v := range array(record["files"]) {
		names = append(names, text(v))
	}
	if f, ok := record["files"].([]string); ok {
		names = f
	}
	saved := []string{}
	for _, name := range names {
		source, e := pdfFile(ws, record, text(record["job_id"]), name)
		if e == nil {
			var p string
			p, e = pdfCopy(source, dest)
			if e == nil {
				saved = append(saved, p)
			}
		}
		if e != nil {
			record["save_error"] = "PDF 已生成，但无法保存到所选目录，请下载保存"
			break
		}
	}
	record["saved_files"] = saved
	return record
}
func (a *App) pdfExportPost(w http.ResponseWriter, r *http.Request, ws *store.Workspace, parts []string, b Object) error {
	if len(parts) == 1 && parts[0] == "delete" {
		pdfJobs.Lock()
		defer pdfJobs.Unlock()
		if pdfJobs.active[ws.Root] != nil {
			return fmt.Errorf("这篇文献正在生成 PDF，完成后才能删除结果")
		}
		if e := os.RemoveAll(filepath.Join(ws.Root, "babeldoc")); e != nil {
			return e
		}
		response(w, 200, Object{"state": "idle", "files": []string{}})
		return nil
	}
	if len(parts) != 0 {
		return os.ErrNotExist
	}
	source, e := ws.SourcePDF()
	if e != nil || source == "" {
		return fmt.Errorf("找不到这篇论文的原始 PDF")
	}
	dest, e := pdfOutputDir(text(b["output_dir"]))
	if e != nil {
		return e
	}
	pdfJobs.Lock()
	defer pdfJobs.Unlock()
	previous, e := pdfStatus(ws)
	if e != nil {
		return e
	}
	if pdfJobs.active[ws.Root] != nil {
		if text(previous["output_dir"]) != dest {
			return fmt.Errorf("这篇论文正在生成 PDF，请完成后再选择其他输出目录")
		}
		response(w, 200, previous)
		return nil
	}
	if b["regenerate"] != true && previous["state"] == "done" && previous["target"] == "zh" {
		valid := len(array(previous["files"])) > 0
		for _, n := range array(previous["files"]) {
			if _, e = pdfFile(ws, previous, text(previous["job_id"]), text(n)); e != nil {
				valid = false
			}
		}
		if valid {
			previous = pdfSave(ws, previous, dest)
			if e = store.WriteJSON(pdfRecord(ws), previous); e != nil {
				return e
			}
			response(w, 200, previous)
			return nil
		}
	}
	cfg, e := a.Config.Load()
	if e != nil {
		return e
	}
	if mid := text(b["model"]); mid != "" {
		cfg, _, e = engines.EngineConfig(cfg, mid)
		if e != nil {
			return e
		}
	}
	if !engines.Ready(cfg) {
		return fmt.Errorf("请先安装并登录所选 CLI，或配置翻译 API")
	}
	command := os.Getenv("EASYREAD_BABELDOC")
	if command == "" {
		command = "babeldoc"
	}
	command, e = exec.LookPath(command)
	if e != nil {
		return fmt.Errorf("未找到 BabelDOC，请安装独立 babeldoc 命令并加入 PATH")
	}
	command, _ = filepath.Abs(command)
	paper, _ := ws.Load("paper")
	stem := text(object(paper["meta"])["source"])
	if stem == "" {
		stem = filepath.Base(source)
	}
	stem = filepath.Base(strings.ReplaceAll(stem, "\\", "/"))
	stem = strings.TrimSuffix(stem, filepath.Ext(stem))
	stem = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`).ReplaceAllString(stem, " ")
	stem = strings.Trim(strings.Join(strings.Fields(stem), " "), " .")
	if len(stem) > 180 {
		stem = stem[:180]
		for !utf8.ValidString(stem) {
			stem = stem[:len(stem)-1]
		}
	}
	if stem == "" {
		stem = "paper"
	}
	random := make([]byte, 16)
	if _, e = rand.Read(random); e != nil {
		return e
	}
	job := hex.EncodeToString(random)
	if e = os.MkdirAll(filepath.Join(ws.Root, "babeldoc", job), 0700); e != nil {
		return e
	}
	model := text(object(cfg[text(cfg["engine"])])["model"])
	if model == "" {
		model = engines.Name(text(cfg["engine"]))
	}
	v := Object{"state": "running", "job_id": job, "message": "正在生成中文 PDF…", "target": "zh", "model": model, "files": []string{}, "output_dir": dest, "saved_files": []string{}, "updated": store.Now()}
	if e = store.WriteJSON(pdfRecord(ws), v); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	pdfJobs.active[ws.Root] = cancel
	go a.runPDFExport(ctx, cancel, ws, command, source, cfg, model, job, stem, dest)
	response(w, 200, v)
	return nil
}

// BabelDOC CLI options verified against the official CLI documentation:
// https://github.com/funstory-ai/BabelDOC (output, language and OpenAI options).
// The bridge key is random, job-local, and unrelated to provider credentials.
func pdfBridge(ctx context.Context, token string, cfg Object, cwd string) http.Handler {
	gate := make(chan struct{}, 1)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			response(w, 401, Object{"error": Object{"message": "unauthorized"}})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" {
			response(w, 404, Object{"error": Object{"message": "not found"}})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		var body Object
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			response(w, 400, Object{"error": Object{"message": "invalid JSON"}})
			return
		}
		var prompt strings.Builder
		for _, v := range array(body["messages"]) {
			m := object(v)
			prompt.WriteString(text(m["role"]) + ":\n")
			if s, ok := m["content"].(string); ok {
				prompt.WriteString(s)
			} else {
				for _, p := range array(m["content"]) {
					prompt.WriteString(text(object(p)["text"]))
				}
			}
			prompt.WriteString("\n\n")
		}
		if prompt.Len() == 0 {
			response(w, 400, Object{"error": Object{"message": "messages are required"}})
			return
		}
		requestCtx, cancel := context.WithCancel(r.Context())
		defer cancel()
		stop := context.AfterFunc(ctx, cancel)
		defer stop()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-requestCtx.Done():
			return
		}
		answer, used, e := engines.Run(requestCtx, cfg, prompt.String(), cwd, nil)
		if e != nil {
			response(w, 502, Object{"error": Object{"message": e.Error()}})
			return
		}
		response(w, 200, Object{"id": "easyread-local", "object": "chat.completion", "created": time.Now().Unix(), "model": "easyread", "choices": []any{Object{"index": 0, "message": Object{"role": "assistant", "content": answer}, "finish_reason": "stop"}}, "usage": Object{"prompt_tokens": used["input"], "completion_tokens": used["output"]}})
	})
}
func (a *App) runPDFExport(ctx context.Context, cancel context.CancelFunc, ws *store.Workspace, command, source string, cfg Object, model, job, stem, dest string) {
	defer cancel()
	output := filepath.Join(ws.Root, "babeldoc", job)
	state := Object{"state": "error", "job_id": job, "model": model, "target": "zh", "files": []string{}, "output_dir": dest, "saved_files": []string{}, "updated": store.Now()}
	defer func() {
		if p := recover(); p != nil {
			state["message"] = fmt.Sprintf("PDF export failed: %v", p)
		}
		state["updated"] = store.Now()
		pdfJobs.Lock()
		defer pdfJobs.Unlock()
		_ = store.WriteJSON(pdfRecord(ws), state)
		delete(pdfJobs.active, ws.Root)
	}()
	if e := func() error {
		listener, e := net.Listen("tcp4", "127.0.0.1:0")
		if e != nil {
			return e
		}
		random := make([]byte, 32)
		if _, e = rand.Read(random); e != nil {
			listener.Close()
			return e
		}
		token := hex.EncodeToString(random)
		srv := &http.Server{Handler: pdfBridge(ctx, token, cfg, ws.Root), ReadHeaderTimeout: 5 * time.Second}
		defer srv.Close()
		go srv.Serve(listener)
		args := []string{"--openai", "--openai-model", "easyread", "--openai-base-url", "http://" + listener.Addr().String() + "/v1", "--openai-api-key", token, "--files", source, "--output", output, "--lang-out", "zh", "--no-dual", "--qps", "1"}
		cmd := exec.CommandContext(ctx, command, args...)
		engines.ConfigureProcessCancellation(cmd)
		cmd.Dir = output
		cmd.WaitDelay = 3 * time.Second
		var logs bytes.Buffer
		cmd.Stdout = &logs
		cmd.Stderr = &logs
		if e = cmd.Run(); e != nil {
			detail := strings.ReplaceAll(logs.String(), token, "[REDACTED]")
			o := object(cfg["openai"])
			for _, k := range []string{text(o["api_key"]), engines.ResolveKey(o, text(o["preset"]), text(o["base_url"]))} {
				if k != "" {
					detail = strings.ReplaceAll(detail, k, "[REDACTED]")
				}
			}
			detail = regexp.MustCompile(`(?i)(api[_ -]?key\s*[:=]\s*)\S+`).ReplaceAllString(detail, "${1}[REDACTED]")
			if len(detail) > 1500 {
				detail = detail[len(detail)-1500:]
			}
			return fmt.Errorf("BabelDOC failed: %w: %s", e, detail)
		}
		files, e := filepath.Glob(filepath.Join(output, "*.mono.pdf"))
		if e != nil {
			return e
		}
		if len(files) != 1 {
			return fmt.Errorf("BabelDOC 未生成唯一的中文 PDF")
		}
		st, e := os.Lstat(files[0])
		if e != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("BabelDOC 输出不是普通 PDF 文件")
		}
		f, e := os.Open(files[0])
		if e != nil {
			return e
		}
		header := make([]byte, 5)
		_, e = io.ReadFull(f, header)
		f.Close()
		if e != nil || string(header) != "%PDF-" {
			return fmt.Errorf("BabelDOC 返回的文件不是有效 PDF")
		}
		name := stem + "-zh.pdf"
		if e = os.Rename(files[0], filepath.Join(output, name)); e != nil {
			return e
		}
		state = Object{"state": "done", "job_id": job, "message": "中文 PDF 已生成", "model": model, "target": "zh", "files": []string{name}, "updated": store.Now()}
		state = pdfSave(ws, state, dest)
		return nil
	}(); e != nil {
		state["message"] = e.Error()
		state["error"] = e.Error()
	}
}

func (a *App) PDFWorkspaceBusy(root string) bool {
	pdfJobs.Lock()
	defer pdfJobs.Unlock()
	return pdfJobs.active[root] != nil
}
