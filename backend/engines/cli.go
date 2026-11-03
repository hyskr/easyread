package engines

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var CLIEngines = []string{"claude", "codex", "codebuddy", "opencode", "zcode", "qoder", "grok", "agy", "cursor"}
var names = map[string]string{"claude": "Claude Code", "codex": "Codex CLI", "codebuddy": "CodeBuddy CLI", "opencode": "OpenCode", "zcode": "ZCode CLI", "qoder": "Qoder CN CLI", "grok": "Grok Build", "agy": "Antigravity CLI", "cursor": "Cursor CLI", "openai": "OpenAI-compatible API"}

func Name(e string) string {
	if n := names[e]; n != "" {
		return n
	}
	return e
}
func cliPath(engine string, c Object) string {
	commands := []string{engine}
	if engine == "cursor" {
		commands = []string{"agent", "cursor-agent"}
	}
	if engine == "qoder" {
		commands = []string{"qodercn", "qoderclicn"}
	}
	if s := str(c["command"]); s != "" {
		commands = []string{s}
	}
	for _, cmd := range commands {
		p, e := exec.LookPath(cmd)
		if e == nil {
			if engine == "cursor" && str(c["command"]) == "" {
				if _, e = os.Stat(filepath.Join(filepath.Dir(p), "grok")); e == nil {
					continue
				}
			}
			p, _ = filepath.Abs(p)
			return p
		}
	}
	return ""
}

const denied = "Bash,Write,Edit,MultiEdit,NotebookEdit,Agent,Task"

func cliRun(ctx context.Context, cfg Object, prompt, cwd string, images []string, emit func(string) error) (string, Object, error) {
	images = append([]string(nil), images...)
	engine := str(cfg["engine"])
	if _, ok := names[engine]; !ok {
		return "", nil, fmt.Errorf("unknown engine: %s", engine)
	}
	c := obj(cfg[engine])
	exe := cliPath(engine, c)
	if exe == "" {
		return "", nil, fmt.Errorf("%s executable not found", Name(engine))
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(c, 1200))
	defer cancel()
	work, e := os.MkdirTemp("", "easyread-"+engine+"-")
	if e != nil {
		return "", nil, e
	}
	defer os.RemoveAll(work)
	if truth(c["_chat"]) {
		for _, name := range []string{"paper.json", "reader.json"} {
			if b, e := os.ReadFile(filepath.Join(cwd, name)); e == nil {
				if e = os.WriteFile(filepath.Join(work, name), b, 0600); e != nil {
					return "", nil, e
				}
			}
		}
		prompt += "\nYou may read the supplied paper and use available web search for relevant sources. Cite source URLs. Do not modify files or run commands."
	}
	for i, p := range images {
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		images[i], e = filepath.Abs(p)
		if e != nil {
			return "", nil, e
		}
	}
	allowed := "Read"
	if truth(c["_chat"]) {
		allowed = "Read,Glob,Grep,WebSearch,WebFetch"
	}
	args := []string{}
	stdin := prompt
	env := os.Environ()
	last := filepath.Join(work, "last.txt")
	model := str(c["model"])
	effort := str(c["reasoning_effort"])
	extra, extraErr := safeExtraArgs(engine, c["extra_args"])
	if extraErr != nil {
		return "", nil, extraErr
	}
	switch engine {
	case "claude":
		args = []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--tools", allowed, "--allowedTools", allowed, "--disallowedTools", denied, "--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence", "--settings", `{"disableAllHooks":true}`, "--setting-sources", "user"}
		if effort != "" {
			args = append(args, "--effort", effort)
		}
	case "codex":
		args = []string{"exec", "--skip-git-repo-check", "--sandbox", "read-only", "--ephemeral", "--color", "never", "--json", "-o", last}
		for _, p := range images {
			args = append(args, "-i", p)
		}
		for key, field := range map[string]string{"model_reasoning_effort": "reasoning_effort", "service_tier": "service_tier"} {
			if s := str(c[field]); s != "" {
				b, _ := json.Marshal(s)
				args = append(args, "-c", key+"="+string(b))
			}
		}
	case "codebuddy":
		args = []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--tools", allowed, "--allowedTools", allowed, "--disallowedTools", denied, "--strict-mcp-config", "--permission-mode", "default", "--setting-sources", "user", "--settings", `{"disableAllHooks":true}`, "--no-session-persistence"}
		parts := []any{Object{"type": "text", "text": prompt}}
		for _, p := range images {
			u, e := imageURL(p)
			if e != nil {
				return "", nil, e
			}
			a := strings.SplitN(strings.TrimPrefix(u, "data:"), ";base64,", 2)
			parts = append(parts, Object{"type": "image", "source": Object{"type": "base64", "media_type": a[0], "data": a[1]}})
		}
		b, _ := json.Marshal(Object{"type": "user", "message": Object{"role": "user", "content": parts}})
		stdin = string(b) + "\n"
		if os.Getenv("CODEBUDDY_INTERNET_ENVIRONMENT") == "" {
			env = append(env, "CODEBUDDY_INTERNET_ENVIRONMENT=internal")
		}
	case "opencode":
		args = []string{"run", "--format", "json"}
		for _, p := range images {
			args = append(args, "--file", p)
		}
		permissions := Object{"*": "deny"}
		if truth(c["_chat"]) {
			for _, k := range []string{"read", "glob", "grep", "list", "websearch", "webfetch"} {
				permissions[k] = "allow"
			}
		}
		b, _ := json.Marshal(permissions)
		config, _ := json.Marshal(Object{"autoupdate": false, "share": "disabled", "permission": permissions})
		env = append(env, "OPENCODE_DISABLE_AUTOUPDATE=true", "OPENCODE_DISABLE_CLAUDE_CODE=true", "OPENCODE_DISABLE_AUTOCOMPACT=true", "OPENCODE_DISABLE_DEFAULT_PLUGINS=true", "OPENCODE_PERMISSION="+string(b), "OPENCODE_CONFIG_CONTENT="+string(config))
	case "qoder":
		args = []string{"-p", "--output-format", "json", "--permission-mode", "default", "--tools", allowed, "--allowed-tools", allowed, "--disallowed-tools", denied, "--strict-mcp-config", "--setting-sources", "user", "--settings", `{"disableAllHooks":true}`, "--no-session-persistence"}
		for _, p := range images {
			args = append(args, "--attachment", p)
		}
	case "zcode":
		if model != "" || effort != "" || str(c["service_tier"]) != "" {
			return "", nil, errors.New("ZCode follows its own model and reasoning configuration")
		}
		task := filepath.Join(work, "easyread-task.txt")
		if e = os.WriteFile(task, []byte(prompt), 0600); e != nil {
			return "", nil, e
		}
		runtime := `{"features":{"mcp":false,"subagent":false,"memory":false,"skill":false},"memory":{"use":false},"plugins":{"enabled":false},"skills":{"enabled":false},"hooks":{"enabled":false}}`
		if e = os.WriteFile(filepath.Join(work, "zcode.json"), []byte(runtime), 0600); e != nil {
			return "", nil, e
		}
		args = []string{"--prompt", "Read the attached easyread-task.txt completely and answer it. Attachments are reference images. Do not modify files or run commands.", "--cwd", work, "--mode", "plan", "--disallowed-tools", strings.ReplaceAll(denied, ",", " "), "--workflow-mode", "disabled", "--output-format", "json", "--no-color", "--attach", task}
		for _, p := range images {
			args = append(args, "--attach", p)
		}
		stdin = ""
	case "grok":
		task := filepath.Join(work, "prompt.md")
		for _, p := range images {
			prompt += "\nRead reference image: " + p
		}
		if e = os.WriteFile(task, []byte(prompt), 0600); e != nil {
			return "", nil, e
		}
		args = []string{"--prompt-file", task, "--output-format", "json", "--no-auto-update", "--tools", "read_file", "--disallowed-tools", "search_tool,use_tool", "--disable-web-search"}
		if effort == "" {
			effort = "low"
		}
		args = append(args, "--reasoning-effort", effort)
		stdin = ""
	case "agy":
		for _, p := range images {
			prompt += "\nRead reference image: " + p
		}
		args = []string{"--input-format", "stream-json", "--output-format", "stream-json"}
		if effort != "" {
			args = append(args, "--effort", effort)
		}
		b, _ := json.Marshal(Object{"event": "user", "message": Object{"content": prompt}})
		stdin = string(b) + "\n"
	case "cursor":
		args = []string{"-p", "--output-format", "json", "--trust", "--mode", "ask"}
		for _, p := range images {
			stdin += "\nRead reference image: " + p
		}
	}
	if engine == "claude" {
		for _, p := range images {
			stdin += "\nRead reference image: " + p
		}
	}
	args = append(extra, args...)
	if model != "" {
		args = append(args, "--model", model)
	}
	if engine == "codex" {
		args = append(args, codexLeanArgs()...)
		args = append(args, "-")
	}
	retriedTools := false
runProcess:
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = work
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	cmd.WaitDelay = 2 * time.Second
	configureProcess(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return "", nil, e
	}
	if e = cmd.Start(); e != nil {
		return "", nil, e
	}
	events := []Object{}
	var raw strings.Builder
	var streamed strings.Builder
	used := Object{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	var callbackErr error
	for scanner.Scan() {
		line := scanner.Text()
		if raw.Len() < 32<<20 {
			raw.WriteString(line)
			raw.WriteByte('\n')
		}
		var ev Object
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		events = append(events, ev)
		piece := ""
		if engine == "claude" || engine == "codebuddy" {
			x := ev
			if str(ev["type"]) == "stream_event" {
				x = obj(ev["event"])
			}
			if str(x["type"]) == "content_block_delta" && str(obj(x["delta"])["type"]) == "text_delta" {
				piece = str(obj(x["delta"])["text"])
			}
		}
		if engine == "opencode" && str(ev["type"]) == "text" {
			part := obj(ev["part"])
			if !truth(part["synthetic"]) && !truth(part["ignored"]) {
				piece = str(part["text"])
			}
		}
		if engine == "codex" && str(ev["type"]) == "item.completed" {
			it := obj(ev["item"])
			if str(it["type"]) == "agent_message" {
				piece = str(it["text"])
			}
		}
		if piece != "" && emit != nil {
			if callbackErr = emit(piece); callbackErr != nil {
				cancel()
				break
			}
			streamed.WriteString(piece)
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if callbackErr != nil {
		return streamed.String(), used, callbackErr
	}
	if ctx.Err() != nil {
		return streamed.String(), used, ctx.Err()
	}
	if scanErr != nil {
		return "", used, scanErr
	}
	if waitErr != nil && engine == "claude" && !retriedTools && raw.Len() == 0 && regexp.MustCompile(`(?im)^\s*(?:error:\s*)?(?:unknown|unrecognized|unsupported) option ['"]--tools['"]\s*$`).MatchString(strings.TrimSpace(stderr.String())) {
		retriedTools = true
		for i, arg := range args {
			if arg == "--tools" && i+1 < len(args) {
				args = append(args[:i:i], args[i+2:]...)
				goto runProcess
			}
		}
	}
	if waitErr != nil {
		return "", used, fmt.Errorf("%s failed: %v: %s", Name(engine), waitErr, redact(stderr.String(), obj(cfg["openai"])))
	}
	if len(events) == 0 {
		var v Object
		if json.Unmarshal([]byte(raw.String()), &v) == nil {
			events = append(events, v)
		}
	}
	answer := ""
	for _, ev := range events {
		if str(ev["type"]) == "error" || truth(ev["is_error"]) {
			return "", used, fmt.Errorf("%s error: %v", Name(engine), ev["message"])
		}
		if u := obj(ev["usage"]); len(u) > 0 {
			mergeUsage(used, cliUsage(u, engine))
		}
		if str(ev["type"]) == "result" {
			if sub := str(ev["subtype"]); sub != "" && sub != "success" {
				return "", used, fmt.Errorf("%s: %s", Name(engine), str(ev["result"]))
			}
			answer = str(ev["result"])
		}
		switch engine {
		case "grok":
			if t := str(ev["text"]); t != "" {
				answer = t
			}
		case "agy":
			if str(ev["event"]) == "result" {
				r := obj(ev["result"])
				if str(r["status"]) != "SUCCESS" {
					return "", used, fmt.Errorf("Antigravity failed: %v", r["error"])
				}
				answer = str(r["response"])
				mergeUsage(used, cliUsage(obj(r["usage"]), engine))
			}
		case "zcode":
			if ev["error"] != nil {
				return "", used, fmt.Errorf("ZCode error: %v", ev["error"])
			}
			if t := str(ev["response"]); t != "" {
				answer = t
			}
		case "opencode":
			if str(ev["type"]) == "text" {
				p := obj(ev["part"])
				if !truth(p["synthetic"]) && !truth(p["ignored"]) {
					answer += str(p["text"])
				}
			}
			if str(ev["type"]) == "step_finish" {
				t := obj(obj(ev["part"])["tokens"])
				cache := obj(t["cache"])
				used["input"] = num(used["input"]) + num(t["input"]) + num(cache["read"]) + num(cache["write"])
				used["output"] = num(used["output"]) + num(t["output"]) + num(t["reasoning"])
				used["cached"] = num(used["cached"]) + num(cache["read"])
			}
		case "codex":
			if str(ev["type"]) == "turn.failed" {
				return "", used, fmt.Errorf("Codex failed: %v", ev["error"])
			}
			it := obj(ev["item"])
			if str(ev["type"]) == "item.completed" && str(it["type"]) == "agent_message" {
				answer += str(it["text"])
			}
		}
	}
	if engine == "codex" {
		if b, e := os.ReadFile(last); e == nil && len(bytes.TrimSpace(b)) > 0 {
			answer = string(b)
		}
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", used, fmt.Errorf("%s returned no answer", Name(engine))
	}
	if emit != nil && streamed.Len() == 0 {
		if e = emit(answer); e != nil {
			return answer, used, e
		}
	}
	return answer, used, nil
}
func cliUsage(u Object, engine string) Object {
	r := Usage(u)
	cached := num(u["cache_read_input_tokens"]) + num(u["cache_read_tokens"]) + num(u["cached_input_tokens"]) + num(u["cacheReadTokens"])
	r["cached"] = cached
	if engine == "claude" || engine == "codebuddy" || engine == "qoder" || engine == "grok" {
		r["input"] = num(r["input"]) + cached + num(u["cache_creation_input_tokens"])
	}
	r["output"] = num(r["output"]) + num(u["thinking_tokens"])
	if engine == "zcode" {
		r["input"] = num(u["inputTokens"])
		r["output"] = num(u["outputTokens"])
	}
	return r
}

// ConfigureProcessCancellation makes cancellation terminate the subprocess group
// on Unix, including inherited child processes. Windows uses CommandContext kill.
func ConfigureProcessCancellation(cmd *exec.Cmd) { configureProcess(cmd) }
