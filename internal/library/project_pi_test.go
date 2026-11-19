package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// piProjects is a sandbox with one project in git of its own.
func piProjects(t *testing.T) (h, proj string) {
	h = sandbox(t)
	proj = filepath.Join(t.TempDir(), "app")
	gitInit(t, proj)
	ok(t)(AddProject(proj))
	return h, proj
}

func piProjectFile(t *testing.T, proj string) map[string]any {
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
	return m
}

func piList(t *testing.T, proj, key string) []string {
	t.Helper()
	m := piProjectFile(t, proj)
	if m == nil {
		return nil
	}
	raw, ok := m[key]
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw.([]any) {
		out = append(out, v.(string))
	}
	return out
}

// A project's Pi packages and extensions go into its own .pi/settings.json,
// and into nothing else: not the agent's own settings.
func TestProjectPiEachAgentsFile(t *testing.T) {
	h, proj := piProjects(t)
	ok(t)(ProjectPi(proj, "package", []string{"npm:pi-inspector@1.4.0"}, true))
	ok(t)(ProjectPi(proj, "extension", []string{"./extensions/format-notes.ts"}, true))

	got := piList(t, proj, "packages")
	if !slices.Equal(got, []string{"npm:pi-inspector@1.4.0"}) {
		t.Errorf("packages = %v", got)
	}
	got = piList(t, proj, "extensions")
	if !slices.Equal(got, []string{"./extensions/format-notes.ts"}) {
		t.Errorf("extensions = %v", got)
	}

	// the agent's own (user-wide) settings is left exactly as it was
	userPath := filepath.Join(h, ".pi", "agent", "settings.json")
	before, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	ok(t)(ProjectPi(proj, "package", []string{"npm:other-package"}, true))
	after, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the user-wide Pi settings changed:\nbefore %s\nafter  %s", before, after)
	}
}

// Other keys in the project's Pi settings, and entries the user wrote, stay
// exactly as they are.
func TestProjectPiKeepTheUsersOwn(t *testing.T) {
	_, proj := piProjects(t)
	write(t, filepath.Join(proj, ".pi", "settings.json"), `{
  "theme": "dark",
  "packages": ["npm:pi-reporter"],
  "extensions": ["./extensions/mine.ts"]
}`)
	ok(t)(ProjectPi(proj, "package", []string{"npm:pi-context-view"}, true))
	ok(t)(ProjectPi(proj, "extension", []string{"./extensions/format-notes.ts"}, true))

	m := piProjectFile(t, proj)
	if m["theme"] != "dark" {
		t.Errorf("an unrelated key was lost: %v", m["theme"])
	}
	if got := piList(t, proj, "packages"); !slices.Equal(got, []string{"npm:pi-reporter", "npm:pi-context-view"}) {
		t.Errorf("packages = %v, want the user's then magpie's", got)
	}
	if got := piList(t, proj, "extensions"); !slices.Equal(got, []string{"./extensions/mine.ts", "./extensions/format-notes.ts"}) {
		t.Errorf("extensions = %v, want the user's kept", got)
	}

	// turned off: magpie's entry STAYS, as Pi's own "-<source>", and the
	// user's is untouched. Turning off is a retained state, not a removal:
	// coming back needs no re-entry of the source.
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiOff))
	if got := piList(t, proj, "packages"); !slices.Equal(got, []string{"npm:pi-reporter", "-npm:pi-context-view"}) {
		t.Errorf("after turning it off, packages = %v", got)
	}
	// and back on: the plain source again, still only once
	ok(t)(ProjectPi(proj, "package", []string{"npm:pi-context-view"}, true))
	if got := piList(t, proj, "packages"); !slices.Equal(got, []string{"npm:pi-reporter", "npm:pi-context-view"}) {
		t.Errorf("after turning it back on, packages = %v", got)
	}
}

// An entry the user changed is theirs from then on: magpie neither
// rewrites it nor takes it away.
func TestProjectPiLeaveChangedEntryAlone(t *testing.T) {
	_, proj := piProjects(t)
	ok(t)(ProjectPi(proj, "package", []string{"npm:pi-context-view"}, true))
	path := filepath.Join(proj, ".pi", "settings.json")

	// the user adds filters to magpie's entry: Pi's object form
	write(t, path, `{"packages":[{"source":"npm:pi-context-view","skills":["one"]}]}`)
	ok(t)(ProjectPi(proj, "package", []string{"npm:pi-context-view"}, false))

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "pi-context-view") {
		t.Errorf("the user's changed entry was taken away: %s", raw)
	}
	if !strings.Contains(string(raw), `"skills"`) {
		t.Errorf("the user's filters were dropped: %s", raw)
	}
}

// A file magpie made keeps the turned-off entry rather than being removed:
// turning a project's Pi entry off is a retained state (Pi's "-<source>"), so
// the file is never emptied by it. A file holding only magpie's entry is still
// written back, never removed, while that entry exists in any form.
func TestProjectPiRemoveTheirOwnFileOnly(t *testing.T) {
	_, proj := piProjects(t)
	ok(t)(ProjectPi(proj, "package", []string{"npm:pi-context-view"}, true))
	path := filepath.Join(proj, ".pi", "settings.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("magpie didn't write it: %v", err)
	}
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiOff))
	if _, err := os.Stat(path); err != nil {
		t.Errorf("turning it off removed the file instead of keeping the entry off: %v", err)
	}
	if got := piList(t, proj, "packages"); !slices.Equal(got, []string{"-npm:pi-context-view"}) {
		t.Errorf("packages = %v, want the turned-off entry kept", got)
	}
	// and REMOVING it really takes it out: the kept entry and the source both go
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiRemove))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a file holding only magpie's entry was left after a removal")
	}

	// a file with the user's own settings is never removed
	write(t, path, `{"theme":"dark"}`)
	ok(t)(ProjectPi(proj, "package", []string{"npm:pi-context-view"}, true))
	ok(t)(ProjectPi(proj, "package", []string{"npm:pi-context-view"}, false))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a file holding the user's settings was removed: %v", err)
	}
	if m := piProjectFile(t, proj); m["theme"] != "dark" {
		t.Errorf("the user's settings were dropped: %v", m)
	}
}

// The project's view says what magpie wrote, and does not claim entries the
// user wrote.
func TestProjectPiView(t *testing.T) {
	_, proj := piProjects(t)
	write(t, filepath.Join(proj, ".pi", "settings.json"), `{"packages":["npm:pi-reporter"]}`)
	ok(t)(ProjectPi(proj, "package", []string{"npm:pi-context-view"}, true))

	v, err := Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	var p *ProjectView
	for i := range v.Projects {
		if v.Projects[i].Dir == proj {
			p = &v.Projects[i]
		}
	}
	if p == nil {
		t.Fatal("the project isn't in the view")
	}
	var got []string
	for _, e := range p.PiEntries {
		got = append(got, e.Kind+":"+e.Source)
	}
	if !slices.Equal(got, []string{"package:npm:pi-context-view"}) {
		t.Errorf("PiEntries = %v, want only magpie's", got)
	}
}

// Removing a project takes its Pi entries out of the folder.
func TestProjectPiRemovedWithProject(t *testing.T) {
	_, proj := piProjects(t)
	ok(t)(ProjectPi(proj, "package", []string{"npm:pi-context-view"}, true))
	if _, err := RemoveProject(proj, false); err != nil {
		t.Fatal(err)
	}
	if got := piList(t, proj, "packages"); len(got) != 0 {
		t.Errorf("the entries stayed after the project was removed: %v", got)
	}
}

// Only Pi has a project settings file this writes.
func TestProjectPiSettingsAgents(t *testing.T) {
	if ProjectPiSettings("pi") == "" {
		t.Error("pi names no project settings file")
	}
	for _, id := range []string{"claude", "codex", "gemini", "cursor"} {
		if ProjectPiSettings(id) != "" {
			t.Errorf("%s names a project Pi settings file it doesn't have", id)
		}
	}
	if _, err := ProjectPi(t.TempDir(), "notakind", []string{"x"}, true); err == nil {
		t.Error("an unknown Pi kind was accepted")
	}
}
