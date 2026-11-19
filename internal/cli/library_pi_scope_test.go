package cli

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/library"
)

// magpie library project pi scope <dir> <kind> <source> anywhere|project
// (project_role.go): where a Pi entry belongs. An unknown scope, a kind
// that isn't one, and an entry the project doesn't have are all refused.
func TestLibraryProjectPiScope(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for the project fixture")
	}
	proj := piCLIProject(t)
	const src = "npm:@example/pi-tools@0.1.0"
	if err := libraryCmd([]string{"library", "project", "pi", "scope", proj, "package", src, "project"}); err == nil {
		t.Error("a scope was set for an entry the project doesn't have")
	}
	if _, err := library.ProjectPi(proj, "package", []string{src}, true); err != nil {
		t.Fatal(err)
	}
	if err := libraryCmd([]string{"library", "project", "pi", "scope", proj, "package", src, "elsewhere"}); err == nil || !strings.Contains(err.Error(), "anywhere") {
		t.Errorf("an unknown scope was taken: %v", err)
	}
	if err := libraryCmd([]string{"library", "project", "pi", "scope", proj, "widget", src, "project"}); err == nil {
		t.Errorf("a kind that isn't one was taken")
	}
	if err := libraryCmd([]string{"library", "project", "pi", "scope", proj, "package", src, "project"}); err != nil {
		t.Fatal(err)
	}
	v, err := library.Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, p := range v.Projects {
		for _, e := range p.PiEntries {
			if e.Source == src {
				got = e.Scope
			}
		}
	}
	if got != library.ScopeProject {
		t.Errorf("scope after: %q", got)
	}
	// and back: the project's set
	if err := libraryCmd([]string{"library", "project", "pi", "scope", proj, "package", src, "anywhere"}); err != nil {
		t.Fatal(err)
	}
	v, _ = library.Read(nil)
	for _, p := range v.Projects {
		for _, e := range p.PiEntries {
			if e.Source == src && e.Scope != "" {
				t.Errorf("scope after anywhere: %q", e.Scope)
			}
		}
	}
}
