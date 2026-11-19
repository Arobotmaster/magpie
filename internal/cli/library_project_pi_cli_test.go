package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/library"
)

// piCLIProject is an isolated HOME with one project in git of its own.
func piCLIProject(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	for k, v := range map[string]string{"HOME": h, "USERPROFILE": h, "XDG_CONFIG_HOME": filepath.Join(h, ".config"), "CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "", "APPDATA": "", "LOCALAPPDATA": ""} {
		t.Setenv(k, v)
	}
	proj := filepath.Join(h, "proj")
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for the project fixture")
	}
	if out, err := exec.Command(git, "init", "-q", "--template=", proj).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if _, err := library.AddProject(proj); err != nil {
		t.Fatal(err)
	}
	return proj
}

// piCLIList is the raw packages list in the project's .pi/settings.json.
func piCLIList(t *testing.T, proj string) []string {
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

// magpie library project pi add|on|off|rm: add and on both give the entry,
// off keeps it as Pi's own "-<source>", and rm takes it out for real.
func TestLibraryProjectPiOffOnRemove(t *testing.T) {
	proj := piCLIProject(t)
	const src = "npm:pi-context-view"
	pi := func(verb string) error {
		return libraryCmd([]string{"library", "project", "pi", verb, proj, "package", src})
	}

	if err := pi("add"); err != nil {
		t.Fatal(err)
	}
	if got := piCLIList(t, proj); !slices.Equal(got, []string{src}) {
		t.Fatalf("after add, packages = %v", got)
	}

	// off keeps the entry, marked the way Pi marks it
	if err := pi("off"); err != nil {
		t.Fatal(err)
	}
	if got := piCLIList(t, proj); !slices.Equal(got, []string{"-" + src}) {
		t.Fatalf("after off, packages = %v, want the entry kept as \"-%s\"", got, src)
	}
	// and the project still reads back, with the source listed
	v, err := library.Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	p := projectView(v, proj)
	if p == nil {
		t.Fatal("the project stopped loading after off")
	}
	if !slices.Contains(p.Pi["package"], src) {
		t.Errorf("the project's packages = %v, want the turned-off source listed", p.Pi["package"])
	}

	// on brings it back, once
	if err := pi("on"); err != nil {
		t.Fatal(err)
	}
	if got := piCLIList(t, proj); !slices.Equal(got, []string{src}) {
		t.Fatalf("after on, packages = %v, want one plain entry", got)
	}
	if err := pi("on"); err != nil {
		t.Fatal(err)
	}
	if got := piCLIList(t, proj); !slices.Equal(got, []string{src}) {
		t.Fatalf("after a second on, packages = %v, want still one entry", got)
	}

	// off then rm: the entry goes for real, and the file with it
	if err := pi("off"); err != nil {
		t.Fatal(err)
	}
	if err := pi("rm"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".pi", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("a file holding only magpie's entry was left after rm")
	}
	v, err = library.Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := projectView(v, proj); p != nil {
		for _, e := range p.PiEntries {
			if e.Source == src {
				t.Errorf("the project still records the entry after rm: %+v", e)
			}
		}
	}

	// remove is still rm's alias
	if err := pi("add"); err != nil {
		t.Fatal(err)
	}
	if err := pi("remove"); err != nil {
		t.Fatal(err)
	}
	if got := piCLIList(t, proj); len(got) != 0 {
		t.Errorf("after remove, packages = %v, want none", got)
	}
}

// off and rm only reach an entry the project has: naming one it doesn't have
// is refused, as rm always did.
func TestLibraryProjectPiOffAndRmRefuseWhatTheProjectLacks(t *testing.T) {
	proj := piCLIProject(t)
	const src = "npm:not-here"
	for _, verb := range []string{"off", "on", "rm", "remove"} {
		err := libraryCmd([]string{"library", "project", "pi", verb, proj, "package", src})
		if verb == "on" {
			// on gives it: it is add's twin, and needs no entry to exist
			if err != nil {
				t.Errorf("on an absent source: %v", err)
			}
			if err := libraryCmd([]string{"library", "project", "pi", "rm", proj, "package", src}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s an entry the project doesn't have was taken", verb)
		}
	}
}

// The usage text names all three verbs, so the page and the terminal offer the
// same answers.
func TestLibraryProjectPiUsageNamesTheThreeStates(t *testing.T) {
	for _, want := range []string{"project pi off ", "project pi on ", "project pi rm "} {
		if !strings.Contains(libraryProjectUsage, want) {
			t.Errorf("the project usage text doesn't mention %q", want)
		}
	}
}
