package library

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func projectBundleFixture(t *testing.T) (h, project, other, source string) {
	t.Helper()
	h = sandbox(t)
	pluginAgents(t, h)
	root := t.TempDir()
	project, other, source = filepath.Join(root, "project"), filepath.Join(root, "other"), filepath.Join(root, "catalogue", "plugins", "workbench")
	for _, dir := range []string{project, other} {
		gitInit(t, dir)
		ok(t)(AddProject(dir))
	}
	write(t, filepath.Join(source, ".claude-plugin/plugin.json"), `{"name":"workbench","version":"1.0.0"}`)
	write(t, filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"workbench","version":"1.0.0","hooks":"./hooks/codex.json"}`)
	write(t, filepath.Join(source, "skills/triage/SKILL.md"), "---\nname: triage\ndescription: Triage findings\n---\nTriage these findings.\n")
	write(t, filepath.Join(source, "agents/analyst.md"), "---\nname: analyst\ndescription: Analyze findings\n---\nAnalyze these findings.\n")
	write(t, filepath.Join(source, ".mcp.json"), `{"mcpServers":{"triage-db":{"command":"node","args":["triage-db.js"]}}}`)
	write(t, filepath.Join(source, "hooks/hooks.json"), `{"hooks":{"PreCompact":[{"hooks":[{"type":"command","command":"echo claude_fixture"}]}]}}`)
	write(t, filepath.Join(source, "hooks/codex.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo codex_fixture"}]}]}}`)
	ok(t)(InstallPlugins(source, nil, nil))
	return h, project, other, source
}

func TestProjectBundleWritesAllSelectedParts(t *testing.T) {
	h, project, other, _ := projectBundleFixture(t)
	claude := filepath.Join(project, ".claude/settings.local.json")
	codex := filepath.Join(project, ".codex/config.toml")
	write(t, claude, `{"permissions":{"allow":["Read"]}}`)
	write(t, codex, "model = \"fixture\"\n")
	before := read(t, path())
	if !strings.Contains(before, `"claude:PreCompact"`) {
		t.Fatal("the dual-manifest import must keep Claude's native event")
	}
	// Re-create the saved event from an earlier dual-manifest import.
	before = strings.Replace(before, `"claude:PreCompact"`, `"claude:claude:PreCompact"`, 1)
	write(t, path(), before)
	ids := []string{"codex", "claude"}
	plan := ok(t)(ProjectBundle(project, "workbench", ids, ProjectWriteOptions{Preview: true}))
	if len(plan.Problems) != 0 || len(plan.Skipped) != 2 || read(t, path()) != before {
		t.Fatalf("preview changed state or chose incorrect parts: %+v", plan)
	}
	files := []string{
		".claude/skills/triage/SKILL.md", ".agents/skills/triage/SKILL.md",
		".claude/agents/analyst.md", ".codex/agents/analyst.toml",
		".codex/hooks.json", ".mcp.json",
	}
	for _, file := range files {
		gone(t, filepath.Join(project, file))
	}
	result := ok(t)(ProjectBundle(project, "workbench", slices.Clone(ids), ProjectWriteOptions{PlanID: plan.PlanID}))
	if len(result.Problems) != 0 || result.PlanID != plan.PlanID {
		t.Fatalf("apply: %+v", result)
	}
	for _, file := range files {
		if read(t, filepath.Join(project, file)) == "" {
			t.Errorf("expected project resource %s", file)
		}
		gone(t, filepath.Join(other, file))
		gone(t, filepath.Join(h, file))
	}
	if got := read(t, claude); strings.Count(got, "claude_fixture") != 1 || strings.Contains(got, "codex_fixture") || !strings.Contains(got, "permissions") {
		t.Errorf("Claude settings: %s", got)
	}
	if got := read(t, filepath.Join(project, ".codex/hooks.json")); strings.Count(got, "codex_fixture") != 1 || strings.Contains(got, "claude_fixture") {
		t.Errorf("Codex hooks: %s", got)
	}
	if got := read(t, codex); !strings.Contains(got, `model = "fixture"`) || !strings.Contains(got, "triage-db") {
		t.Errorf("Codex settings: %s", got)
	}
	if result := ok(t)(ProjectBundle(project, "workbench", ids)); len(result.Writes) != 0 || len(result.Problems) != 0 {
		t.Fatalf("repeat apply should be a no-op: %+v", result)
	}
}

func TestProjectBundleRejectsPartialWrite(t *testing.T) {
	_, project, _, _ := projectBundleFixture(t)
	ids := []string{"claude", "codex"}
	broken := filepath.Join(project, ".codex/hooks.json")
	write(t, broken, "{")
	before := read(t, path())
	plan, err := ProjectBundle(project, "workbench", ids, ProjectWriteOptions{Preview: true})
	if err != nil || len(plan.Problems) == 0 || len(plan.Writes) == 0 {
		t.Fatalf("fixture should stage some resources and find a problem: %+v %v", plan, err)
	}
	result, err := ProjectBundle(project, "workbench", ids, ProjectWriteOptions{PlanID: plan.PlanID})
	if err != nil || len(result.Problems) == 0 || len(result.Writes) != 0 || read(t, path()) != before || read(t, broken) != "{" {
		t.Fatalf("a blocked bundle changed state or files: %+v %v", result, err)
	}
	gone(t, filepath.Join(project, ".claude/skills/triage"))
	gone(t, filepath.Join(project, ".codex/agents/analyst.toml"))
}

func TestProjectBundleUsesEffectiveNativeSource(t *testing.T) {
	h, project, other, source := projectBundleFixture(t)
	market := filepath.Dir(filepath.Dir(source))
	const catalogue = `{"name":"team","plugins":[{"name":"workbench","source":"./plugins/workbench"}]}`
	write(t, filepath.Join(market, ".claude-plugin/marketplace.json"), catalogue)
	write(t, filepath.Join(market, ".agents/plugins/marketplace.json"), catalogue)
	claude := filepath.Join(h, ".claude")
	claudeInstall := filepath.Join(claude, "plugins/cache/team/workbench/1.0.0")
	write(t, filepath.Join(claudeInstall, ".claude-plugin/plugin.json"), `{"name":"workbench","version":"1.0.0"}`)
	record, err := json.Marshal(map[string][]claudeInstallRecord{"workbench@team": {{
		Scope: "local", ProjectPath: project, InstallPath: claudeInstall,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	write(t, claudeInstalled(), `{"version":2,"plugins":`+string(record)+`}`)
	write(t, claudeMarketplaces(), fmt.Sprintf(`{"team":{"source":{"source":"directory","path":%q},"installLocation":%q}}`, market, market))
	claudeFlags := `{"enabledPlugins":{"workbench@team":true},"permissions":{"allow":["Read"]}}`
	write(t, claudeSettings(), claudeFlags)

	codex := filepath.Join(h, ".codex")
	write(t, filepath.Join(codex, "plugins/cache/team/workbench/1.0.0/.codex-plugin/plugin.json"), `{"name":"workbench","version":"1.0.0"}`)
	codexUser := fmt.Sprintf("[marketplaces.team]\nsource_type = 'local'\nsource = %q\n[plugins.'workbench@team']\nenabled = true\n[projects.%q]\ntrust_level = 'trusted'\n", market, realDirOf(other))
	codexUserFile := filepath.Join(codex, "config.toml")
	write(t, codexUserFile, codexUser)
	codexOther := filepath.Join(other, ".codex/config.toml")
	write(t, codexOther, "[plugins.'workbench@team']\nenabled = false\n")

	ids := []string{"claude", "codex"}
	native := ok(t)(ProjectBundle(project, "workbench", ids))
	if len(native.Writes) != 0 || len(native.Skipped) != 2 {
		t.Fatalf("same-source native bundle should satisfy both targets: %+v", native)
	}
	gone(t, filepath.Join(project, ".claude/skills/triage"))
	gone(t, filepath.Join(project, ".agents/skills/triage"))
	if read(t, codexUserFile) != codexUser || read(t, claudeSettings()) != claudeFlags {
		t.Fatal("native files changed")
	}
	ok(t)(ProjectSkills(project, []string{"triage"}, ids))
	if read(t, filepath.Join(project, ".agents/skills/triage/SKILL.md")) == "" {
		t.Fatal("the single-item assignment must exist before deduplicating it")
	}
	ok(t)(ProjectBundle(project, "workbench", ids))
	gone(t, filepath.Join(project, ".claude/skills/triage"))
	gone(t, filepath.Join(project, ".agents/skills/triage"))
	if read(t, codexUserFile) != codexUser || read(t, claudeSettings()) != claudeFlags {
		t.Fatal("deduplicating the prior assignment rewrote native settings")
	}

	// The Claude local install belongs to the first folder, and Codex's
	// trusted project override disables the user's install in the second.
	otherPlan := ok(t)(ProjectBundle(other, "workbench", ids, ProjectWriteOptions{Preview: true}))
	if !writesPath(otherPlan, filepath.Join(other, ".claude/skills/triage")) || !writesPath(otherPlan, filepath.Join(other, ".agents/skills/triage")) {
		t.Fatalf("unavailable native plugin masked project resources: %+v", otherPlan)
	}
	write(t, codexOther, "[plugins.'workbench@team']\nenabled = true\n")
	if _, err := ProjectBundle(other, "workbench", ids, ProjectWriteOptions{PlanID: otherPlan.PlanID}); err == nil || !strings.Contains(err.Error(), "plan changed") {
		t.Fatalf("a native enable change should stale the plan: %v", err)
	}
	gone(t, filepath.Join(other, ".claude/skills/triage"))

	// An identically named native marketplace entry from a different
	// directory cannot satisfy the library assignment by its name.
	unrelated := t.TempDir()
	write(t, filepath.Join(unrelated, ".agents/plugins/marketplace.json"), catalogue)
	write(t, filepath.Join(unrelated, "plugins/workbench/.codex-plugin/plugin.json"), `{"name":"workbench"}`)
	write(t, codexUserFile, strings.Replace(codexUser, fmt.Sprintf("%q", market), fmt.Sprintf("%q", unrelated), 1))
	result := ok(t)(ProjectBundle(other, "workbench", ids))
	if len(result.Problems) != 0 {
		t.Fatalf("different native source: %+v", result)
	}
	if read(t, filepath.Join(other, ".agents/skills/triage/SKILL.md")) == "" {
		t.Fatal("same name from another marketplace suppressed the library skill")
	}
}
