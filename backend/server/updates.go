package server

import (
	"context"
	"encoding/json"
	"github.com/hyskr/easyread/backend/engines"
	"github.com/hyskr/easyread/backend/store"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

func presets() []Object { return engines.Presets() }
func presetGroups() [][]string {
	return [][]string{{"cn", "国内直连"}, {"intl", "海外（国内要开梯子）"}, {"local", "本机运行（离线、免费）"}}
}
func newer(latest, current string) bool {
	r := regexp.MustCompile(`[0-9]+`)
	a, b := r.FindAllString(latest, 3), r.FindAllString(current, 3)
	for len(a) < 3 {
		a = append(a, "0")
	}
	for len(b) < 3 {
		b = append(b, "0")
	}
	for i := 0; i < 3; i++ {
		x, _ := strconv.Atoi(a[i])
		y, _ := strconv.Atoi(b[i])
		if x != y {
			return x > y
		}
	}
	return false
}
func (a *App) update(ctx context.Context, force bool) (Object, error) {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	cfg, e := a.Config.Load()
	if e != nil {
		return nil, e
	}
	out := Object{"current": Version, "latest": "", "newer": false, "enabled": cfg["check_updates"] != false, "failed": false}
	if out["enabled"] == false && !force {
		return out, nil
	}
	p := filepath.Join(a.Config.Home, "update.json")
	cache, e := store.ReadJSON(p)
	if e != nil {
		return nil, e
	}
	if cache == nil {
		cache = Object{}
	}
	wait := 24 * time.Hour
	if cache["failed"] == true || text(cache["latest"]) == "" {
		wait = 3 * time.Hour
	}
	if force || time.Since(time.Unix(int64(integer(cache["checked"])), 0)) > wait {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		endpoint := a.UpdateURL
		if endpoint == "" {
			endpoint = "https://api.github.com/repos/Edwardxlai/easyread/releases/latest"
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		req.Header.Set("User-Agent", "EasyRead/"+Version)
		req.Header.Set("Accept", "application/vnd.github+json")
		r, err := http.DefaultClient.Do(req)
		cache["checked"] = time.Now().Unix()
		cache["failed"] = true
		if err == nil {
			defer r.Body.Close()
			var rel Object
			if r.StatusCode == 200 && json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1024*1024)).Decode(&rel) == nil {
				cache["latest"] = text(rel["tag_name"])
				cache["url"] = rel["html_url"]
				cache["notes"] = rel["body"]
				cache["published"] = rel["published_at"]
				cache["failed"] = false
			}
		}
		_ = store.WriteJSON(p, cache)
	}
	for _, k := range []string{"latest", "url", "notes", "published", "failed"} {
		if v, ok := cache[k]; ok {
			out[k] = v
		}
	}
	out["newer"] = newer(text(out["latest"]), Version)
	return out, nil
}
