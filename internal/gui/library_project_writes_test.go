package gui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/library"
)

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
