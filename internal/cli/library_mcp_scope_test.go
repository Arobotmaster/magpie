package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/library"
)

// magpie library mcp scope <name> anywhere|project (project_role.go): a
// server given to a project is the project's set, written into each
// worktree of its repository, or the project's own folder alone. A flag is
// never taken as a name, an unknown scope is refused, and a server given to
// the agents themselves can't be scoped.
func TestLibraryMCPScope(t *testing.T) {
	h := t.TempDir()
	for k, v := range map[string]string{"HOME": h, "USERPROFILE": h, "XDG_CONFIG_HOME": filepath.Join(h, ".config"), "CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "", "APPDATA": "", "LOCALAPPDATA": ""} {
		t.Setenv(k, v)
	}
	if err := os.MkdirAll(filepath.Join(h, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h, ".claude/settings.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := library.SaveServer("", library.Server{Name: "lookup", Transport: "stdio", Command: "lookup-mcp"}); err != nil {
		t.Fatal(err)
	}
	scope := func() string {
		t.Helper()
		v, err := library.Read(nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range v.Servers {
			if s.Name == "lookup" {
				return s.Scope
			}
		}
		t.Fatal("lookup is gone")
		return ""
	}

	if err := libraryCmd([]string{"library", "mcp", "scope", "lookup", "elsewhere"}); err == nil || !strings.Contains(err.Error(), "anywhere") {
		t.Errorf("an unknown scope was taken: %v", err)
	}
	if err := libraryCmd([]string{"library", "mcp", "scope", "lookup", library.ScopeProject}); err != nil {
		t.Fatal(err)
	}
	if got := scope(); got != library.ScopeProject {
		t.Errorf("scope: %q", got)
	}
	// a flag is never a server's name
	if err := libraryCmd([]string{"library", "mcp", "scope", "--help", "project"}); err == nil {
		t.Error("a flag was taken as a server's name")
	}
	// given to an agent of its own, it has no project to be kept to
	if _, err := library.ServerAgents("lookup", []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	if err := libraryCmd([]string{"library", "mcp", "scope", "lookup", library.ScopeProject}); err == nil || !strings.Contains(err.Error(), "take it from them first") {
		t.Errorf("a server given to the agents themselves was scoped: %v", err)
	}
	if _, err := library.ServerAgents("lookup", nil); err != nil {
		t.Fatal(err)
	}
	if err := libraryCmd([]string{"library", "mcp", "scope", "lookup", "anywhere"}); err != nil {
		t.Fatal(err)
	}
	if got := scope(); got != "" {
		t.Errorf("scope after anywhere: %q", got)
	}
}
