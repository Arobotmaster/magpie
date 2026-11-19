package gui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/library"
)

func TestPluginDiscoveryProjectRoute(t *testing.T) {
	project := sandboxHome(t)
	cfg := filepath.Join(project, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	const config = "[mcp_servers.lookup]\ncommand = \"fixture\"\n[mcp_servers.optional]\ncommand = \"fixture\"\n[mcp_servers.other]\ncommand = \"fixture\"\n"
	if err := os.WriteFile(cfg, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	libraryRoutes(mux, &opens{})
	readOrigins := func() map[string]library.PluginPartState {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/api/library/projects/plugin-origins?dir="+url.QueryEscape(project), nil))
		var rows []library.PluginOrigin
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); rec.Code != http.StatusOK || err != nil {
			t.Fatalf("plugin origins: %d %s", rec.Code, rec.Body)
		}
		parts := make(map[string]library.PluginPartState)
		for _, row := range rows {
			if row.Origin == "codex-mcp" && row.Scope == "project" {
				parts[row.Name] = row.Parts[0]
			}
		}
		return parts
	}
	before := readOrigins()
	if len(before) != 3 || before["lookup"].Discovered != nil {
		t.Fatalf("discovery without a session: %+v", before)
	}
	body, err := json.Marshal(map[string]any{
		"cwd": project, "tool": "codex", "seen": []string{before["lookup"].DiscoveryKey},
		"complete": []string{"mcp"}, "missing": []string{before["optional"].DiscoveryKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := library.RecordPluginDiscovery(project, bytes.NewReader(body), false); err != nil {
		t.Fatal(err)
	}
	after := readOrigins()
	yes, no := after["lookup"].Discovered, after["optional"].Discovered
	if yes == nil || !*yes || no == nil || *no || after["other"].Discovered != nil {
		t.Fatalf("session evidence not preserved by the project API: %+v", after)
	}
}

func TestProjectWritesRoutePreview(t *testing.T) {
	dir := piRouteProject(t)
	server := httptest.NewServer(Handler(&opens{}, nil))
	t.Cleanup(server.Close)
	post := func(body map[string]any) (int, []byte) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := server.Client().Post(server.URL+"/api/library/projects/instructions", "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, raw
	}
	body := map[string]any{"dir": dir, "instr": "fixture instructions", "agents": []string{"claude"}, "preview": true}
	status, response := post(body)
	var plan library.Result
	if status != http.StatusOK || json.Unmarshal(response, &plan) != nil || plan.PlanID == "" {
		t.Fatalf("preview: %d %s", status, response)
	}
	path := filepath.Join(dir, library.ProjectInstrFile("claude"))
	if !slices.ContainsFunc(plan.Writes, func(w library.ProjectWrite) bool { return filepath.Base(w.Path) == filepath.Base(path) }) {
		t.Fatalf("missing path in %+v", plan.Writes)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("preview created the project file: %v", err)
	}

	body["preview"], body["planId"] = false, plan.PlanID
	if status, response = post(body); status != http.StatusOK {
		t.Fatalf("apply: %d %s", status, response)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Contains(data, []byte("fixture instructions")) {
		t.Fatalf("planned file wasn't written: %v", err)
	}
}

func TestProjectBundleRouteUsesSharedPlan(t *testing.T) {
	dir := piRouteProject(t)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(`{"name":"route-bundle","pi":{"extensions":["./example.ts"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "example.ts"), []byte("export default function() {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := library.InstallPlugins(source, nil, []string{"none"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(Handler(&opens{}, nil))
	t.Cleanup(server.Close)
	post := func(on, preview bool, id string) *http.Response {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"dir": dir, "name": "route-bundle", "agents": []string{"pi"}, "on": on, "preview": preview, "planId": id,
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := server.Client().Post(server.URL+"/api/library/projects/bundle", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}
	if res := post(true, false, ""); res.StatusCode == http.StatusOK {
		t.Fatal("apply without a preview was allowed")
	}
	settings := filepath.Join(dir, ".pi", "settings.json")
	for _, on := range []bool{true, false} {
		preview := post(on, true, "")
		var plan library.Result
		if preview.StatusCode != http.StatusOK || json.NewDecoder(preview.Body).Decode(&plan) != nil || plan.PlanID == "" || len(plan.Problems) != 0 {
			t.Fatalf("preview on=%t: %d %+v", on, preview.StatusCode, plan)
		}
		if _, err := os.Stat(settings); !on && err != nil || on && !os.IsNotExist(err) {
			t.Fatalf("preview changed the Pi settings: on=%t err=%v", on, err)
		}
		apply := post(on, false, plan.PlanID)
		if apply.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(apply.Body)
			t.Fatalf("apply on=%t: %d %s", on, apply.StatusCode, body)
		}
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Fatalf("withdraw did not remove the created settings: %v", err)
	}
}
