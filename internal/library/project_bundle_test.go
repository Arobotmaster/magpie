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
	role := filepath.Join(project, ".codex/agents/analyst.toml")
	if !writesPath(native, role) || len(native.Skipped) != 2 {
		t.Fatalf("same-source native plugin should leave Codex role conversion available: %+v", native)
	}
	gone(t, filepath.Join(project, ".claude/agents/analyst.md"))
	gone(t, filepath.Join(project, ".codex/hooks.json"))
	gone(t, filepath.Join(project, ".mcp.json"))
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
	ok(t)(RemoveProjectBundle(project, "workbench", ids))
	gone(t, role)
	for _, rel := range []string{".claude/skills/triage/SKILL.md", ".agents/skills/triage/SKILL.md"} {
		if read(t, filepath.Join(project, rel)) == "" {
			t.Fatalf("pre-existing single assignment %s was lost", rel)
		}
	}
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

func TestProjectBundleWithdrawKeepsOtherAssignmentsAndEdits(t *testing.T) {
	_, project, _, _ := projectBundleFixture(t)
	ids := []string{"claude", "codex"}
	ok(t)(ProjectSkill(project, "triage", []string{"codex"}))
	ok(t)(ProjectCopy(project, true))
	ok(t)(ProjectBundle(project, "workbench", ids))
	if again := ok(t)(ProjectBundle(project, "workbench", ids)); len(again.Writes) != 0 {
		t.Fatalf("repeat enable wrote: %+v", again)
	}
	claudeSkill := filepath.Join(project, ".claude/skills/triage/SKILL.md")
	claudeRole := filepath.Join(project, ".claude/agents/analyst.md")
	claudeMCP := filepath.Join(project, ".mcp.json")
	claudeHook := filepath.Join(project, ".claude/settings.local.json")
	for file, edit := range map[string]func(string) string{
		claudeSkill: func(s string) string { return s + "\nUser changed the skill.\n" },
		claudeRole:  func(s string) string { return s + "\nUser changed the role.\n" },
		claudeMCP:   func(s string) string { return strings.Replace(s, "triage-db.js", "my-tool.js", 1) },
		claudeHook:  func(s string) string { return strings.Replace(s, "echo claude_fixture", "echo my_hook", 1) },
	} {
		original := read(t, file)
		changed := edit(original)
		if changed == original {
			t.Fatalf("fixture did not change %s", file)
		}
		write(t, file, changed)
	}
	keep := map[string]string{}
	for _, file := range []string{claudeSkill, claudeRole, claudeMCP, claudeHook, filepath.Join(project, ".agents/skills/triage/SKILL.md")} {
		keep[file] = read(t, file)
	}
	plan := ok(t)(RemoveProjectBundle(project, "workbench", ids, ProjectWriteOptions{Preview: true}))
	if len(plan.Problems) != 0 || !writesPath(plan, filepath.Join(project, ".codex/agents/analyst.toml")) {
		t.Fatalf("withdraw preview: %+v", plan)
	}
	config := filepath.Join(project, ".codex/config.toml")
	write(t, config, "model = 'fixture'\n"+read(t, config))
	if _, err := RemoveProjectBundle(project, "workbench", ids, ProjectWriteOptions{PlanID: plan.PlanID}); err == nil || !strings.Contains(err.Error(), "plan changed") {
		t.Fatalf("withdraw accepted a stale plan: %v", err)
	}
	plan = ok(t)(RemoveProjectBundle(project, "workbench", ids, ProjectWriteOptions{Preview: true}))
	res := ok(t)(RemoveProjectBundle(project, "workbench", ids, ProjectWriteOptions{PlanID: plan.PlanID}))
	if len(res.Problems) != 0 || res.PlanID != plan.PlanID {
		t.Fatalf("withdraw apply: %+v", res)
	}
	for file, want := range keep {
		if got := read(t, file); got != want {
			t.Errorf("withdraw changed %s", file)
		}
	}
	gone(t, filepath.Join(project, ".codex/agents/analyst.toml"))
	if strings.Contains(read(t, filepath.Join(project, ".codex/hooks.json")), "codex_fixture") || strings.TrimSpace(read(t, config)) != "model = 'fixture'" {
		t.Fatal("withdraw must take out its entries and keep the user's Codex setting")
	}
	if again := ok(t)(RemoveProjectBundle(project, "workbench", ids)); len(again.Writes) != 0 {
		t.Fatalf("repeat withdrawal wrote: %+v", again)
	}
}

func TestProjectBundleWithdrawSharesPiPackage(t *testing.T) {
	_, project := piProjects(t)
	source := filepath.Join(t.TempDir(), "fixture")
	importPiBundleFixture(t, source, false)
	aliasName := "other-bundle"
	// The same Pi source imported under another bundle name is deliberately
	// represented by a separate library folder.
	write(t, filepath.Join(pluginDir(aliasName), "package.json"), read(t, filepath.Join(source, "package.json")))
	ok(t)(changeProject(project, 0, func(l *Library) error {
		l.Plugins = append(l.Plugins, &Plugin{Name: aliasName, Source: source})
		return nil
	}))
	ids := []string{"pi"}
	ok(t)(ProjectBundle(project, piBundleFixture, ids))
	ok(t)(ProjectBundle(project, aliasName, ids))
	if got := piList(t, project, "packages"); !slices.Equal(got, []string{pluginDir(piBundleFixture)}) {
		t.Fatalf("a second bundle duplicated the Pi package: %v", got)
	}
	ok(t)(RemoveProjectBundle(project, piBundleFixture, ids))
	if got := piList(t, project, "packages"); !slices.Equal(got, []string{pluginDir(piBundleFixture)}) {
		t.Fatalf("the other bundle still uses this Pi package: %v", got)
	}
	ok(t)(RemoveProjectBundle(project, aliasName, ids))
	if got := piList(t, project, "packages"); len(got) != 0 {
		t.Fatalf("last owner left the Pi package behind: %v", got)
	}

	piPath := piProjectPath(project)
	native := fmt.Sprintf(`{"packages":[{"source":%q,"skills":[]}]}`, source)
	write(t, piPath, native)
	ok(t)(ProjectBundle(project, piBundleFixture, ids))
	ok(t)(RemoveProjectBundle(project, piBundleFixture, ids))
	if read(t, piPath) != native {
		t.Fatal("a Pi declaration that predated assignment was changed")
	}
}

func TestProjectBundleWithdrawRespectsWorktreesAndAgentScope(t *testing.T) {
	_, project, other, _ := projectBundleFixture(t)
	tree := filepath.Join(t.TempDir(), "worktree")
	projectWorktree(t, project, tree)
	ok(t)(ProjectDistribute(project, ProjectDistribution{Skills: true, Subagents: true}))
	ids := []string{"claude", "codex"}
	ok(t)(ProjectBundle(project, "workbench", ids))
	claudeSkill := ".claude/skills/triage/SKILL.md"
	codexRole := ".codex/agents/analyst.toml"
	if read(t, filepath.Join(tree, claudeSkill)) == "" || read(t, filepath.Join(tree, codexRole)) == "" {
		t.Fatal("the opted-in worktree must receive these two resources")
	}
	ok(t)(ProjectDistribute(project, ProjectDistribution{}))
	ok(t)(RemoveProjectBundle(project, "workbench", []string{"claude"}))
	for _, dir := range []string{project, tree, other} {
		gone(t, filepath.Join(dir, claudeSkill))
	}
	if read(t, filepath.Join(project, codexRole)) == "" || read(t, filepath.Join(tree, codexRole)) == "" {
		t.Fatal("withdrawing Claude changed the Codex role")
	}
	ok(t)(RemoveProjectBundle(project, "workbench", []string{"codex"}))
	for _, dir := range []string{project, tree, other} {
		gone(t, filepath.Join(dir, codexRole))
	}
}

func TestProjectBundleWithdrawKeepsEditedRouterEntry(t *testing.T) {
	h, project, _, _ := projectBundleFixture(t)
	write(t, filepath.Join(h, ".claude.json"), "{}")
	ok(t)(SetMCPHub(true))
	ids := []string{"claude", "codex"}
	if res := ok(t)(ProjectBundle(project, "workbench", ids)); len(res.Problems) != 0 {
		t.Fatalf("enable with router: %+v", res)
	}
	codex := filepath.Join(project, ProjectMCPFile("codex"))
	config := read(t, codex)
	if !strings.Contains(config, "[mcp_servers.magpie]") {
		t.Fatal("bundle did not use the project router")
	}
	modified := strings.Replace(config, "/mcp/", "/my-router/", 1)
	if modified == config {
		modified = config + "enabled = false\n"
	}
	write(t, codex, modified)
	res := ok(t)(RemoveProjectBundle(project, "workbench", ids))
	if len(res.Problems) != 0 || read(t, codex) != modified {
		t.Fatalf("withdraw changed the user's router entry: %+v", res)
	}
	record := claudeRecord(t, h, claudeRoot(project))
	if entries, _ := record["mcpServers"].(map[string]any); entries[HubEntry] != nil {
		t.Fatal("withdraw left its unedited Claude router entry")
	}
}
