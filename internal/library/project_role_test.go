package library

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/gateway"
)

// scopedProject makes a project with three linked worktrees and two
// test servers assigned to Pi, Claude Code and Codex.
func scopedProject(t *testing.T) (h, repo, selected string, copies []string) {
	h = hubSandbox(t)
	ok(t)(SaveServer("", Server{Name: "catalog-search", Transport: "stdio", Command: "catalog-search-mcp"}))
	ok(t)(SaveServer("", Server{Name: "project-search", Transport: "stdio", Command: "project-search-mcp"}))
	// Claude Code's own record of every project, as a machine that has run it
	write(t, filepath.Join(h, ".claude.json"), "{}\n")
	repo = filepath.Join(t.TempDir(), "example-project")
	selected = repo
	write(t, filepath.Join(selected, ".gitignore"), "node_modules/\n")
	gitInit(t, selected)
	ok(t)(AddProject(selected))
	ok(t)(ProjectServer(selected, "catalog-search", []string{"pi", "claude", "codex"}))
	ok(t)(ProjectServer(selected, "project-search", []string{"pi", "claude", "codex"}))
	for _, n := range []string{"1", "2", "3"} {
		top := filepath.Join(t.TempDir(), "worktree-"+n)
		projectWorktree(t, selected, top)
		copies = append(copies, top)
	}
	return h, repo, selected, copies
}

// piServerNames are the servers in a folder's .pi/mcp.json, by name.
func piServerNames(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for k := range piServers(t, filepath.Join(dir, ".pi", "mcp.json")) {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Even a full sync creates no MCP entries in related copies.
func TestProjectRoleStaysOutOfCopies(t *testing.T) {
	_, _, selected, copies := scopedProject(t)
	ok(t)(Sync())
	if got := piServerNames(t, selected); !slices.Equal(got, []string{"catalog-search", "project-search"}) {
		t.Fatalf("the project's own .pi/mcp.json: %v", got)
	}
	for _, copyDir := range copies {
		for _, rel := range []string{".pi/mcp.json", ".mcp.json", ".codex/config.toml"} {
			gone(t, filepath.Join(copyDir, rel))
		}
	}
}

// With the router on, a project's hub address is its whole set, and every
// worktree of the repository reads that one address: a server scoped to the
// project's own folder is not served through it, or a copy would reach the
// folder-scoped server there instead of through its own files
// (project_role.go, inHub). The project's own folder gets it as its own
// entry, and the router still serves what a copy inherits.
func TestProjectRoleNotThroughTheRouter(t *testing.T) {
	h, _, selected, copies := scopedProject(t)
	ok(t)(SetMCPHub(true))
	ok(t)(ServerScope("project-search", ScopeProject))
	ok(t)(ProjectApply(selected))

	// the router's project address, which every worktree of the repository
	// reads, serves the shared server and not the folder-scoped server
	l, _ := load()
	key := projectKey(selected)
	names := func(id string) []string {
		var out []string
		for _, s := range l.hubServers(id) {
			out = append(out, s.Name)
		}
		return out
	}
	scoped := "pi" + gateway.HubProjectSep + key
	if got := names(scoped); slices.Contains(got, "project-search") {
		t.Errorf("the router serves the folder-scoped server to a copy: %v", got)
	} else if !slices.Contains(got, "catalog-search") {
		t.Errorf("the router lost the server a copy inherits: %v", got)
	}

	// the project's own folder is given it as its own entry, not through
	// the hub: Claude Code's project scope, .mcp.json, and not the hub
	// entry in its record
	if s := servers(t, filepath.Join(selected, ".mcp.json"), "mcpServers"); s["project-search"] == nil {
		t.Errorf("the project's own .mcp.json has no entry of its own for it:\n%s", read(t, filepath.Join(selected, ".mcp.json")))
	}
	rec := claudeRecord(t, h, selected)
	hub, _ := rec["mcpServers"].(map[string]any)["magpie"].(map[string]any)
	if u, _ := hub["url"].(string); !strings.HasSuffix(u, gateway.HubMCPPath+"claude"+gateway.HubProjectSep+key) {
		t.Errorf("claude's project hub entry: %v", rec["mcpServers"])
	}
	// No project MCP files are written into the copies.
	for _, copyDir := range copies {
		gone(t, filepath.Join(copyDir, ".pi", "mcp.json"))
	}
}

// A copy that still holds a scoped server from a sync before the scope was
// set is taken back out of it: its .pi/mcp.json, .mcp.json and
// .codex/config.toml lose the entry, and the file magpie made goes once it
// is empty again. Nothing else in those files is touched.
func TestProjectRoleTakenBackFromAnExistingCopy(t *testing.T) {
	_, _, selected, copies := scopedProject(t)
	copyDir := copies[0]
	rel := ".pi/mcp.json"
	pi := filepath.Join(copyDir, rel)
	write(t, pi, `{"mcpServers":{"project-search":{"command":"project-search-mcp"},"theirs":{"command":"mine"}},"theirs":1}`)
	ok(t)(change(func(l *Library) error {
		p := l.project(selected)
		p.Trees = []*Project{{Dir: copyDir, Wrote: map[string][]string{rel: {"project-search"}}}}
		return nil
	}))
	ok(t)(ProjectApply(selected))
	if got := piServerNames(t, copyDir); !slices.Equal(got, []string{"project-search", "theirs"}) {
		t.Fatalf("project apply touched a legacy copy: %v", got)
	}
	ok(t)(Sync())
	if got := piServerNames(t, copyDir); !slices.Equal(got, []string{"theirs"}) || jsonOf(t, pi)["theirs"] == nil {
		t.Errorf("legacy cleanup left: %s", read(t, pi))
	}
}

// A server given to the agents themselves is written in each agent's own
// config, which reads for every folder, so a project scope would promise
// more than it kept: it is refused, and says how to make it a project's.
// So is a plugin's server, which every project that turns the plugin on is
// given.
func TestProjectScopeRefusedWhereItCannotHold(t *testing.T) {
	h, _, selected, _ := scopedProject(t)
	if _, err := ServerScope("catalog-search", "elsewhere"); err == nil {
		t.Error("an unknown scope was taken")
	}
	ok(t)(ServerAgents("catalog-search", []string{"claude"}))
	if _, err := ServerScope("catalog-search", ScopeProject); err == nil || !strings.Contains(err.Error(), "take it from them first") {
		t.Errorf("a server given to the agents themselves was scoped: %v", err)
	}
	ok(t)(ServerAgents("catalog-search", nil))
	ok(t)(ServerScope("catalog-search", ScopeProject))
	// the project's own folder is written first, so nothing else changed
	if s := servers(t, filepath.Join(h, ".claude.json"), "mcpServers"); s["catalog-search"] != nil {
		t.Errorf("a project's server reached the agents' own config: %v", s)
	}
	_ = selected
}

// piProject is a sandbox with a repository holding a selected checkout
// and one linked worktree.
func piProject(t *testing.T) (selected string, copyDir string) {
	_, _, selected, copies := scopedProject(t)
	return selected, copies[0]
}

// A Pi package the project has in a role no copy has stays in the project's
// own folder: the .pi/settings.json magpie made is kept out of git, so git
// can't carry it into a worktree where Pi would load it for an ordinary
// copy. That is a real path, not a theory: a committed .pi/settings.json
// reaches every worktree of its repository, and Pi given the project's
// trust loads it as scope=project, origin=package.
func TestProjectPiRoleStaysOutOfCopies(t *testing.T) {
	selected, copyDir := piProject(t)
	ok(t)(ProjectPi(selected, "package", []string{"npm:@example/pi-tools@0.1.0"}, true))

	// the project's own folder has the entry
	pi := filepath.Join(selected, ".pi", "settings.json")
	if !strings.Contains(read(t, pi), "@example/pi-tools") {
		t.Fatalf("the project's own .pi/settings.json: %s", read(t, pi))
	}
	// of the project's set it is not kept out of git: a copy is meant to
	// load it, as it did before this setting
	l, _ := load()
	if slices.Contains(l.project(selected).MadeFiles, piSettingsRel) {
		t.Error("an entry of the project's set was kept out of git")
	}

	// scoped: the file magpie made is kept out of git
	ok(t)(ProjectPiScope(selected, "package", "npm:@example/pi-tools@0.1.0", ScopeProject))
	l, _ = load()
	if !slices.Contains(l.project(selected).MadeFiles, piSettingsRel) {
		t.Fatalf("MadeFiles after scoping: %v", l.project(selected).MadeFiles)
	}
	if g := excluded(t, selected); !strings.Contains(g, "/.pi/settings.json\n") {
		t.Errorf("info/exclude:\n%s", g)
	}
	// the entry itself stays in the project's own folder
	if !strings.Contains(read(t, pi), "@example/pi-tools") {
		t.Errorf("the project's own folder lost the entry: %s", read(t, pi))
	}

	// given back to the project's set, the file is the project's own again
	ok(t)(ProjectPiScope(selected, "package", "npm:@example/pi-tools@0.1.0", "anywhere"))
	l, _ = load()
	if slices.Contains(l.project(selected).MadeFiles, piSettingsRel) {
		t.Error("the file is still kept out of git after scope anywhere")
	}
	if !exists(pi) {
		t.Error("the project's file was taken away")
	}
	_ = copyDir
}

// The scope survives a sync: an entry still wanted is recorded again, and a
// scope lost there would quietly put the file back into git.
func TestProjectPiScopeSurvivesASync(t *testing.T) {
	selected, _ := piProject(t)
	src := "npm:@example/pi-tools@0.1.0"
	ok(t)(ProjectPi(selected, "package", []string{src}, true))
	ok(t)(ProjectPiScope(selected, "package", src, ScopeProject))
	ok(t)(Sync())
	l, _ := load()
	if got := l.project(selected).piScopeOf("package", src); got != ScopeProject {
		t.Fatalf("scope after a sync: %q", got)
	}
	if !slices.Contains(l.project(selected).MadeFiles, piSettingsRel) {
		t.Errorf("MadeFiles after a sync: %v", l.project(selected).MadeFiles)
	}
}

// A file the user already had is left alone, and the scope says what it
// cannot do rather than pretending: info/exclude keeps a file from being
// added, not from staying committed, and magpie never writes the user's
// .gitignore.
func TestProjectPiScopeOnTheUsersOwnFile(t *testing.T) {
	selected, _ := piProject(t)
	src := "npm:@example/pi-tools@0.1.0"
	// the user's own file, with their own key in it
	pi := filepath.Join(selected, ".pi", "settings.json")
	write(t, pi, "{\n  \"packages\": [\"npm:theirs@1.0.0\"]\n}\n")
	ok(t)(ProjectPi(selected, "package", []string{src}, true))
	// the scope itself is taken; the complaint is made by the sync below
	if _, err := ProjectPiScope(selected, "package", src, ScopeProject); err != nil {
		t.Fatal(err)
	}

	l, _ := load()
	if slices.Contains(l.project(selected).MadeFiles, piSettingsRel) {
		t.Error("the user's own file was claimed as magpie's to keep out of git")
	}
	// their key is still there
	if !strings.Contains(read(t, pi), "npm:theirs@1.0.0") {
		t.Errorf("the user's own entry was taken away: %s", read(t, pi))
	}
	// the sync says the boundary isn't enforced, instead of passing silently
	res, err := Sync()
	if err != nil {
		t.Fatal(err)
	}
	var said bool
	for _, p := range res.Problems {
		if p.What == "project:pi" && strings.Contains(p.Error, ".gitignore") {
			said = true
		}
	}
	if !said {
		t.Errorf("a scoped entry on the user's own file was reported as enforced: %v", res.Problems)
	}
}

// A scope is refused for an entry magpie hasn't written, and for a kind
// that isn't one Pi reads.
func TestProjectPiScopeRefusals(t *testing.T) {
	selected, _ := piProject(t)
	if _, err := ProjectPiScope(selected, "package", "npm:absent@1", ScopeProject); err == nil {
		t.Error("a scope was set for an entry the project doesn't have")
	}
	ok(t)(ProjectPi(selected, "package", []string{"npm:here@1"}, true))
	if _, err := ProjectPiScope(selected, "widget", "npm:here@1", ScopeProject); err == nil {
		t.Error("a scope was set for a kind that isn't one")
	}
	if _, err := ProjectPiScope(selected, "package", "npm:here@1", "elsewhere"); err == nil {
		t.Error("an unknown scope was taken")
	}
}
