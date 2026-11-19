package library

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestProjectNativeResourcesAndClaudeLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating directory symlinks may require privileges")
	}
	_, project := projectLib(t)
	shared := filepath.Join(project, ".agents/skills/pdf")
	skill(t, shared, "pdf", "Project PDF")
	claude := filepath.Join(project, ".claude/skills/pdf")
	existing := filepath.Join(project, ".claude/skills/existing")
	skill(t, existing, "existing", "Claude local")
	skill(t, filepath.Join(project, ".agents/skills/existing"), "existing", "Shared")
	const mcp = `{"mcpServers":{"local":{"command":"local-test"}}}`
	const pi = `{"packages":["./example"],"extensions":["./extra.ts"]}`
	const instructions = "Project's instructions\n"
	write(t, filepath.Join(project, ".mcp.json"), mcp)
	write(t, filepath.Join(project, ".pi/settings.json"), pi)
	write(t, filepath.Join(project, "AGENTS.md"), instructions)
	res, err := ProjectSkill(project, "pdf", []string{"claude", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 0 || len(res.Skipped) != 2 || !strings.HasPrefix(res.Skipped[0].Error, errProjectAlreadyHas.Error()) {
		t.Fatalf("expected a skip for both skill destinations: %+v", res)
	}
	if target, err := os.Readlink(claude); err != nil || target != "../../.agents/skills/pdf" {
		t.Fatalf("Claude link=%q, %v", target, err)
	}
	views, err := ReadProject(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(views.Native["skills"]) != 3 || len(views.Native["servers"]) == 0 || len(views.Native["pi"]) != 2 ||
		len(views.Native["instructions"]) != 1 || len(views.SkillLinks) != 1 || len(views.Placed) != 0 {
		t.Fatalf("unexpected ownership: native=%v linked=%v placed=%v", views.Native, views.SkillLinks, views.Placed)
	}
	ok(t)(RemoveProject(project, false))
	gone(t, claude)
	for _, f := range []string{filepath.Join(shared, "SKILL.md"), filepath.Join(existing, "SKILL.md")} {
		if !exists(f) {
			t.Fatalf("native skill disappeared: %s", f)
		}
	}
	if read(t, filepath.Join(project, ".mcp.json")) != mcp ||
		read(t, filepath.Join(project, ".pi/settings.json")) != pi ||
		read(t, filepath.Join(project, "AGENTS.md")) != instructions {
		t.Fatal("native configuration changed")
	}
}

func TestProjectNativeHookAndRoleDedup(t *testing.T) {
	_, project, _ := hookProjects(t)
	const native = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done","timeout":15}]}]}}`
	shared := filepath.Join(project, ".claude/settings.json")
	write(t, shared, native)
	res, err := ProjectHooks(project, []string{"bark"}, []string{"claude", "codex"})
	if err != nil || len(res.Problems) != 0 || len(res.Skipped) != 1 || !strings.HasPrefix(res.Skipped[0].Error, errProjectAlreadyHas.Error()) {
		t.Fatalf("expected the Claude hook to be skipped: %+v, %v", res, err)
	}
	gone(t, filepath.Join(project, ".claude/settings.local.json"))
	if !exists(filepath.Join(project, ".codex/hooks.json")) {
		t.Fatal("a Claude hook suppressed the Codex hook")
	}
	s := reviewer
	s.Agents = []string{}
	ok(t)(SaveSubagent("", s))
	role := filepath.Join(project, ".claude/agents/custom.md")
	const roleBody = "---\nname: reviewer\ndescription: project role\n---\nReview this project.\n"
	write(t, role, roleBody)
	ok(t)(ProjectSubagents(project, []string{"reviewer"}, []string{"claude", "codex"}))
	gone(t, filepath.Join(project, ".claude/agents/reviewer.md"))
	view, err := ReadProject(project)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(view.Native["subagents"], func(e ProjectResource) bool { return e.Name == "reviewer" && e.Agent == "claude" }) ||
		!slices.ContainsFunc(view.Native["hooks"], func(e ProjectResource) bool { return e.Name == "say done" && e.Event == "Stop" }) {
		t.Fatalf("expected project-provided role and hook: %+v", view.Native)
	}
	ok(t)(RemoveProject(project, false))
	if read(t, shared) != native || read(t, role) != roleBody {
		t.Fatal("project-provided files changed")
	}
}
