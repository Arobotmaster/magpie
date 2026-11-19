package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// piProjectInView is one project's view, by its folder.
func piProjectInView(t *testing.T, v *View, dir string) *ProjectView {
	t.Helper()
	for i := range v.Projects {
		if v.Projects[i].Dir == dir {
			return &v.Projects[i]
		}
	}
	return nil
}

// piSourcesIn is the raw list under key in the project's .pi/settings.json,
// exactly as it is written: an off entry is Pi's own "-<source>".
func piSourcesIn(t *testing.T, proj, key string) []string {
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

// off keeps the entry in the project's file as Pi's own "-<source>", and the
// project still reads back with it: the entry is a state, not a loss.
func TestProjectPiOffKeepsTheEntry(t *testing.T) {
	_, proj := piProjects(t)
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiOn))
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiOff))

	if got := piSourcesIn(t, proj, "packages"); !slices.Equal(got, []string{"-npm:pi-context-view"}) {
		t.Errorf("packages = %v, want the entry kept as \"-npm:pi-context-view\"", got)
	}
	v, err := Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	p := piProjectInView(t, v, proj)
	if p == nil {
		t.Fatal("the project stopped loading")
	}
	if !slices.Contains(p.Pi["package"], "npm:pi-context-view") {
		t.Errorf("the project's packages = %v, want the turned-off source still listed", p.Pi["package"])
	}
	var raw string
	for _, e := range p.PiEntries {
		if e.Kind == "package" && e.Source == "npm:pi-context-view" {
			raw = e.Raw
		}
	}
	if raw != `"-npm:pi-context-view"` {
		t.Errorf("the entry's record = %s, want magpie's turned-off entry", raw)
	}
}

// on brings a kept entry back as the plain source, once: no second copy is
// added beside the entry that was kept.
func TestProjectPiOnRestoresWithoutADuplicate(t *testing.T) {
	_, proj := piProjects(t)
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiOn))
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiOff))
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiOn))
	// and a second on, when it is already on, changes nothing either
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiOn))

	if got := piSourcesIn(t, proj, "packages"); !slices.Equal(got, []string{"npm:pi-context-view"}) {
		t.Errorf("packages = %v, want exactly one plain entry", got)
	}
	v, err := Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, e := range piProjectInView(t, v, proj).PiEntries {
		if e.Kind == "package" && e.Source == "npm:pi-context-view" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("magpie records %d entries for one source, want 1", n)
	}
}

// rm takes the entry out of the project's own .pi/settings.json for real:
// neither the plain source nor the kept "-<source>" is left.
func TestProjectPiRemoveTakesItOut(t *testing.T) {
	_, proj := piProjects(t)
	path := filepath.Join(proj, ".pi", "settings.json")
	// a file holding only the user's own key, so the file itself stays and
	// the removal has to be visible inside it
	write(t, path, `{"theme":"dark"}`)
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiOn))
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiOff))
	if got := piSourcesIn(t, proj, "packages"); !slices.Equal(got, []string{"-npm:pi-context-view"}) {
		t.Fatalf("packages before the removal = %v", got)
	}

	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiRemove))

	if got := piSourcesIn(t, proj, "packages"); len(got) != 0 {
		t.Errorf("packages after the removal = %v, want none", got)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the user's file was removed with the entry: %v", err)
	}
	if strings.Contains(string(b), "pi-context-view") {
		t.Errorf("the entry survived a removal: %s", b)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["theme"] != "dark" {
		t.Errorf("the user's own key was dropped: %v", m)
	}
	v, err := Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := piProjectInView(t, v, proj); p != nil {
		for _, e := range p.PiEntries {
			if e.Source == "npm:pi-context-view" {
				t.Errorf("the project still records the entry: %+v", e)
			}
		}
	}
}

// A removal of a source that is only kept off, never on, removes the kept
// entry too: rm is about the file, not about what magpie last did to it.
func TestProjectPiRemoveDropsTheKeptEntry(t *testing.T) {
	_, proj := piProjects(t)
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiOn))
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiOff))
	if got := piSourcesIn(t, proj, "packages"); !slices.Equal(got, []string{"-npm:pi-context-view"}) {
		t.Fatalf("packages = %v", got)
	}
	ok(t)(ProjectPiChange(proj, "package", []string{"npm:pi-context-view"}, PiRemove))
	if _, err := os.Stat(filepath.Join(proj, ".pi", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("a file holding only magpie's kept entry was left after a removal")
	}
}

// The old shape still means what it meant: ProjectPi(on) is PiOn and
// ProjectPi(off) keeps the entry, which is what the view and the routes call
// when they say on or off with no mode.
func TestProjectPiOldShapeKeepsOff(t *testing.T) {
	_, proj := piProjects(t)
	ok(t)(ProjectPi(proj, "package", []string{"npm:pi-context-view"}, true))
	ok(t)(ProjectPi(proj, "package", []string{"npm:pi-context-view"}, false))
	if got := piSourcesIn(t, proj, "packages"); !slices.Equal(got, []string{"-npm:pi-context-view"}) {
		t.Errorf("ProjectPi(on:false) = %v, want the entry kept turned off", got)
	}
}
