package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// hookProjects is a sandbox with two projects in git of their own, and a
// library hook for each of Claude Code and Codex.
func hookProjects(t *testing.T) (h, a, b string) {
	h = sandbox(t)
	a, b = filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	for _, d := range []string{a, b} {
		gitInit(t, d)
		ok(t)(AddProject(d))
	}
	ok(t)(SaveHook("", Hook{Name: "bark", Event: "turn-end", Command: "say done", Agents: []string{}}))
	ok(t)(SaveHook("", Hook{Name: "fmt", Event: "after-tool", Matcher: "edit", Command: "gofmt -w .", Agents: []string{}}))
	return
}

// readHooksAt is the entries under ev in the hooks file at path.
func readHooksAt(t *testing.T, path, ev string) []json.RawMessage {
	t.Helper()
	doc, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	list, err := hookList(doc, path, ev)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return list
}

// A project's hooks go into the folder each agent reads a project's from,
// in the shape of its user-wide ones (#6874), and into nothing else: not
// the agent's own settings, not another project.
func TestProjectHooksEachAgentsFile(t *testing.T) {
	h, a, b := hookProjects(t)
	ok(t)(ProjectHooks(a, []string{"bark"}, []string{"claude", "codex"}))

	for _, c := range []struct {
		agent, path string
	}{
		{"claude", filepath.Join(a, ".claude", "settings.local.json")},
		{"codex", filepath.Join(a, ".codex", "hooks.json")},
	} {
		got := readHooksAt(t, c.path, "Stop")
		if len(got) != 1 {
			t.Fatalf("%s: %d entries, want 1 (%v)", c.agent, len(got), got)
		}
		if !strings.Contains(string(got[0]), "say done") {
			t.Errorf("%s: the entry doesn't hold the command: %s", c.agent, got[0])
		}
	}

	// not the agent's own (user-wide) hooks file
	for _, p := range []string{filepath.Join(h, ".claude", "settings.json"), filepath.Join(h, ".codex", "hooks.json")} {
		if got := readHooksAt(t, p, "Stop"); len(got) != 0 {
			t.Errorf("%s: the user-wide file was written: %v", p, got)
		}
	}
	// not another project
	for _, p := range []string{filepath.Join(b, ".claude", "settings.local.json"), filepath.Join(b, ".codex", "hooks.json")} {
		if got := readHooksAt(t, p, "Stop"); len(got) != 0 {
			t.Errorf("%s: another project was written: %v", p, got)
		}
	}
}

// The user's own hooks in a project's file stay there, and are not taken
// for magpie's: what magpie takes away is only what it wrote.
func TestProjectHooksKeepTheUsersOwn(t *testing.T) {
	_, a, _ := hookProjects(t)
	path := filepath.Join(a, ".claude", "settings.local.json")
	write(t, path, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"say mine"}]}]}}`)
	ok(t)(ProjectHooks(a, []string{"bark"}, []string{"claude"}))

	got := readHooksAt(t, path, "Stop")
	if len(got) != 2 {
		t.Fatalf("%d entries, want the user's and magpie's: %v", len(got), got)
	}
	if !strings.Contains(string(got[0]), "say mine") {
		t.Errorf("the user's entry moved or changed: %s", got[0])
	}

	// taken out of the project: magpie's goes, the user's stays
	ok(t)(ProjectHooks(a, []string{"bark"}, nil))
	got = readHooksAt(t, path, "Stop")
	if len(got) != 1 || !strings.Contains(string(got[0]), "say mine") {
		t.Fatalf("after the hook was taken out, Stop is %v, want the user's alone", got)
	}
}

// An entry the user changed is theirs from then on: magpie neither
// rewrites it nor takes it away.
func TestProjectHooksLeaveChangedEntryAlone(t *testing.T) {
	_, a, _ := hookProjects(t)
	ok(t)(ProjectHooks(a, []string{"bark"}, []string{"claude"}))
	path := filepath.Join(a, ".claude", "settings.local.json")

	// the user edits the command
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(doc), "say done", "say mine instead", 1)
	if edited == string(doc) {
		t.Fatal("the entry magpie wrote doesn't hold the command it was given")
	}
	write(t, path, edited)

	ok(t)(ProjectHooks(a, []string{"bark"}, nil))
	got := readHooksAt(t, path, "Stop")
	if len(got) != 1 || !strings.Contains(string(got[0]), "say mine instead") {
		t.Fatalf("the user's edited entry was taken away: %v", got)
	}
}

// A project's file that magpie made while it held nothing else is taken
// away again, so a project that had none has none after; a file holding
// anything else is written back, never removed.
func TestProjectHooksRemoveTheirOwnFileOnly(t *testing.T) {
	_, a, _ := hookProjects(t)
	ok(t)(ProjectHooks(a, []string{"bark"}, []string{"claude", "codex"}))
	for _, p := range []string{filepath.Join(a, ".claude", "settings.local.json"), filepath.Join(a, ".codex", "hooks.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s: magpie didn't write it: %v", p, err)
		}
	}
	ok(t)(ProjectHooks(a, []string{"bark"}, nil))
	for _, p := range []string{filepath.Join(a, ".claude", "settings.local.json"), filepath.Join(a, ".codex", "hooks.json")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s: a file magpie made and emptied was left behind", p)
		}
	}

	// a file the user's own settings live in is never removed
	path := filepath.Join(a, ".claude", "settings.local.json")
	write(t, path, `{"permissions":{"allow":["Bash(ls:*)"]}}`)
	ok(t)(ProjectHooks(a, []string{"bark"}, []string{"claude"}))
	ok(t)(ProjectHooks(a, []string{"bark"}, nil))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a file holding the user's settings was removed: %v", err)
	}
	var top map[string]any
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	if top["permissions"] == nil {
		t.Errorf("the user's settings were dropped: %s", raw)
	}
}

// A session-start hook narrowed to some starts keeps its matcher, and a
// project's Claude Code file and Codex file each get the agent's own shape.
func TestProjectHooksSessionStartMatcher(t *testing.T) {
	_, a, _ := hookProjects(t)
	ok(t)(SaveHook("", Hook{Name: "route", Event: "session-start", Matcher: "startup|clear", Command: "fixture-hook", Agents: []string{}}))
	ok(t)(ProjectHooks(a, []string{"route"}, []string{"claude", "codex"}))
	for _, c := range []struct {
		agent, path string
	}{
		{"claude", filepath.Join(a, ".claude", "settings.local.json")},
		{"codex", filepath.Join(a, ".codex", "hooks.json")},
	} {
		got := readHooksAt(t, c.path, "SessionStart")
		if len(got) != 1 {
			t.Fatalf("%s: %d SessionStart entries, want 1", c.agent, len(got))
		}
		if !strings.Contains(string(got[0]), `"startup|clear"`) {
			t.Errorf("%s: the matcher was lost or widened: %s", c.agent, got[0])
		}
	}

	// a start the agent never has is refused, not widened
	if _, err := SaveHook("", Hook{Name: "bad", Event: "session-start", Matcher: "startup|load", Command: "x"}); err == nil {
		t.Fatal("a matcher naming no start of any agent was kept")
	}
}

// ProjectHooksFile is the agent's project file, and only the two agents
// whose docs name one are listed.
func TestProjectHooksAgentsListed(t *testing.T) {
	got := ProjectHookAgents()
	if !slices.Equal(got, []string{"claude", "codex"}) {
		t.Errorf("ProjectHookAgents() = %v, want [claude codex]", got)
	}
	for _, id := range got {
		if ProjectHooksFile(id) == "" {
			t.Errorf("%s is listed but names no file", id)
		}
	}
	for _, id := range []string{"pi", "gemini", "grok", "cursor"} {
		if ProjectHooksFile(id) != "" {
			t.Errorf("%s names a project hooks file it doesn't have", id)
		}
	}
}

// Adding a hook for an agent with no project hooks file says so, rather
// than recording it and giving nothing.
func TestProjectHooksRefuseAgentWithoutFile(t *testing.T) {
	_, a, _ := hookProjects(t)
	if _, err := ProjectHooks(a, []string{"bark"}, []string{"pi"}); err == nil {
		t.Fatal("a Pi project hook was accepted")
	} else if !strings.Contains(err.Error(), "project's hooks") {
		t.Errorf("the refusal doesn't say why: %v", err)
	}
}

// A hook that is user-wide as well isn't given to the project twice in that
// agent's user-wide file: the two owners write different files.
func TestProjectHooksDoNotTouchUserWide(t *testing.T) {
	h, a, _ := hookProjects(t)
	ok(t)(HookAgents("bark", []string{"claude"}))
	ok(t)(ProjectHooks(a, []string{"bark"}, []string{"claude"}))

	own := readHooksAt(t, filepath.Join(h, ".claude", "settings.json"), "Stop")
	proj := readHooksAt(t, filepath.Join(a, ".claude", "settings.local.json"), "Stop")
	if len(own) != 1 || len(proj) != 1 {
		t.Fatalf("user-wide %d, project %d: want one each", len(own), len(proj))
	}
}

// The project's view says which hooks it has, for which agents, and the
// entries magpie wrote in which file.
func TestProjectHooksView(t *testing.T) {
	_, a, _ := hookProjects(t)
	ok(t)(ProjectHooks(a, []string{"bark"}, []string{"claude"}))

	v, err := Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	var p *ProjectView
	for i := range v.Projects {
		if v.Projects[i].Dir == a {
			p = &v.Projects[i]
		}
	}
	if p == nil {
		t.Fatal("the project isn't in the view")
	}
	if !slices.Equal(p.Hooks["bark"], []string{"claude"}) {
		t.Errorf("Hooks[bark] = %v, want [claude]", p.Hooks["bark"])
	}
	if len(p.HookFiles) != 1 {
		t.Fatalf("%d written entries recorded, want 1: %+v", len(p.HookFiles), p.HookFiles)
	}
	f := p.HookFiles[0]
	if f.Name != "bark" || f.Path != ".claude/settings.local.json" || f.Event != "Stop" || f.Hash == "" {
		t.Errorf("the record doesn't name the entry: %+v", f)
	}
}

// Removing a project takes its hooks out of the folder, as its skills and
// subagents go.
func TestProjectHooksRemovedWithProject(t *testing.T) {
	_, a, _ := hookProjects(t)
	ok(t)(ProjectHooks(a, []string{"bark"}, []string{"claude", "codex"}))
	if _, err := RemoveProject(a, false); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(a, ".claude", "settings.local.json"), filepath.Join(a, ".codex", "hooks.json")} {
		if got := readHooksAt(t, p, "Stop"); len(got) != 0 {
			t.Errorf("%s: the hooks stayed after the project was removed: %v", p, got)
		}
	}
}

// A hook written where its record was lost (a magpie that stopped before
// saving) is taken as magpie's rather than written a second time.
func TestProjectHooksNotWrittenTwice(t *testing.T) {
	_, a, _ := hookProjects(t)
	ok(t)(ProjectHooks(a, []string{"bark"}, []string{"claude"}))
	path := filepath.Join(a, ".claude", "settings.local.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// the same library, its record of what it wrote forgotten
	ok(t)(change(func(l *Library) error {
		for _, p := range l.Projects {
			if p.Dir == a {
				p.HookFiles = nil
			}
		}
		return nil
	}))
	ok(t)(ProjectHooks(a, []string{"bark"}, []string{"claude"}))
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the entry was written twice:\nbefore %s\nafter  %s", before, after)
	}
	if got := readHooksAt(t, path, "Stop"); len(got) != 1 {
		t.Errorf("%d entries, want 1: %v", len(got), got)
	}
}

// A hook whose event belongs to another agent is refused when it is assigned,
// not only noted later by a sync. Before this, `hook add <proj> <claude hook>
// agents=codex` answered "✓ … → codex", recorded the assignment, wrote no file,
// and the only complaint came from a later `library sync` that also exited 0.
func TestProjectHooksRefuseAnotherAgentsEvent(t *testing.T) {
	h, a, _ := hookProjects(t)
	ok(t)(SaveHook("", Hook{Name: "claude-only", Event: "claude:PreToolUse", Matcher: "Bash", Command: "echo claude", Agents: []string{}}))
	_ = h

	// codex's spec has no PreToolUse of Claude's family, so the pair is refused
	// and nothing is recorded
	if _, err := ProjectHooks(a, []string{"claude-only"}, []string{"codex"}); err == nil {
		t.Fatal("codex took a claude:PreToolUse hook")
	}
	v, err := Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	var p *ProjectView
	for i := range v.Projects {
		if v.Projects[i].Dir == a {
			p = &v.Projects[i]
		}
	}
	if p == nil {
		t.Fatal("project gone")
	}
	if len(p.Hooks["claude-only"]) != 0 {
		t.Errorf("the refused pair was recorded anyway: %v", p.Hooks["claude-only"])
	}

	// the agent whose event it is still takes it, and the file is written
	if _, err := ProjectHooks(a, []string{"claude-only"}, []string{"claude"}); err != nil {
		t.Fatalf("claude refused its own event: %v", err)
	}
	if got := readHooksAt(t, filepath.Join(a, ".claude", "settings.local.json"), "PreToolUse"); len(got) != 1 {
		t.Errorf("claude entries = %d, want 1", len(got))
	}
}
