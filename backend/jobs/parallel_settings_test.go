package jobs_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hyskr/easyread/backend/config"
	"github.com/hyskr/easyread/backend/server"
)

func settingsApp(t *testing.T) *server.App {
	t.Helper()
	home := t.TempDir()
	cfg, e := config.New(home)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = cfg.Save(map[string]any{"engine": "none", "check_updates": false}); e != nil {
		t.Fatal(e)
	}
	app, e := server.New(home, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(app.Close)
	return app
}
func savePaperCount(t *testing.T, a *server.App, value any) (int, map[string]any) {
	t.Helper()
	raw, e := json.Marshal(map[string]any{"paper_concurrency": value})
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("POST", "http://localhost/api/config", bytes.NewReader(raw))
	req.Header.Set("X-Token", a.Token)
	response := httptest.NewRecorder()
	a.ServeHTTP(response, req)
	var result map[string]any
	if e = json.Unmarshal(response.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	return response.Code, result
}

// Exact original invalid inputs; rejected requests must not mutate config.
func TestSettingsRejectOutOfRangeOrNonIntegerPaperCount(t *testing.T) {
	a := settingsApp(t)
	path := filepath.Join(a.Config.Home, "config.json")
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, value := range []any{0, -1, 5, 8, "4", true, 2.5, nil} {
		code, _ := savePaperCount(t, a, value)
		if code != 400 {
			t.Fatalf("%#v: status=%d", value, code)
		}
		after, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(before, after) {
			t.Fatalf("invalid %#v changed config: %v", value, e)
		}
	}
}
func TestSettingsSaveAllowedPaperCounts(t *testing.T) {
	a := settingsApp(t)
	for _, value := range []int{1, 2, 3, 4} {
		code, result := savePaperCount(t, a, value)
		if code != 200 {
			t.Fatalf("%d: %d %#v", value, code, result)
		}
		cfg, _ := result["config"].(map[string]any)
		if cfg["paper_concurrency"] != float64(value) {
			t.Fatal(result)
		}
		loaded, e := a.Config.Load()
		if e != nil || loaded["paper_concurrency"] != float64(value) {
			t.Fatal(loaded, e)
		}
	}
}
