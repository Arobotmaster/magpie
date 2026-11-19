package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/library"
)

// piRouteProject is a sandboxed HOME with one project in git of its own.
func piRouteProject(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for the project fixture")
	}
	h := sandboxHome(t)
	t.Setenv("PATH", filepath.Dir(git))
	proj := filepath.Join(h, "app")
	if out, err := exec.Command(git, "init", "-q", "--template=", proj).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if _, err := library.AddProject(proj); err != nil {
		t.Fatal(err)
	}
	return proj
}

// piRouteList is the raw packages list in the project's .pi/settings.json.
func piRouteList(t *testing.T, proj string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(proj, ".pi", "settings.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	v, ok := m["packages"]
	if !ok {
		return nil
	}
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// The projects/pi route's Mode field is the page's third answer: mode "off"
// keeps the entry and stops loading it, while on:false with no mode is still
// the removal it always was.
func TestProjectsPiRouteMode(t *testing.T) {
	proj := piRouteProject(t)
	const src = "npm:pi-context-view"
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		Handler(&opens{}, nil).ServeHTTP(rec, httptest.NewRequest("POST", "/api/library/projects/pi", strings.NewReader(body)))
		return rec
	}
	body := func(extra string) string {
		return `{"dir":"` + proj + `","kind":"package","names":["` + src + `"]` + extra + `}`
	}

	if rec := post(body(`, "on": true`)); rec.Code != http.StatusOK {
		t.Fatalf("on: %d %s", rec.Code, rec.Body)
	}
	if got := piRouteList(t, proj); !slices.Equal(got, []string{src}) {
		t.Fatalf("after on, packages = %v", got)
	}

	// mode "off": the entry stays, marked the way Pi marks it
	if rec := post(body(`, "on": false, "mode": "off"`)); rec.Code != http.StatusOK {
		t.Fatalf("off: %d %s", rec.Code, rec.Body)
	}
	if got := piRouteList(t, proj); !slices.Equal(got, []string{"-" + src}) {
		t.Fatalf("after mode off, packages = %v, want the entry kept", got)
	}

	// back on, and once
	if rec := post(body(`, "on": true`)); rec.Code != http.StatusOK {
		t.Fatalf("on again: %d %s", rec.Code, rec.Body)
	}
	if got := piRouteList(t, proj); !slices.Equal(got, []string{src}) {
		t.Fatalf("after on again, packages = %v, want one plain entry", got)
	}

	// no mode with on:false is the removal: the trash button sends this
	if rec := post(body(`, "on": false`)); rec.Code != http.StatusOK {
		t.Fatalf("rm: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(proj, ".pi", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("a file holding only magpie's entry was left after the removal")
	}
}
