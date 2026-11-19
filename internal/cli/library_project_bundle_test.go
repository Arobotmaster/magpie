package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/library"
)

func TestLibraryProjectBundlePlanAndApply(t *testing.T) {
	project := projectSandbox(t)
	source := t.TempDir()
	for file, body := range map[string]string{
		".codex-plugin/plugin.json": `{"name":"operations","version":"1.0.0","hooks":"./hooks/codex.json"}`,
		"hooks/codex.json":          `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo fixture_done"}]}]}}`,
		"skills/runbook/SKILL.md":   "---\nname: runbook\ndescription: Read and follow a runbook\n---\nOpen the runbook.\n",
	} {
		p := filepath.Join(source, file)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := library.InstallPlugins(source, nil, nil); err != nil {
		t.Fatal(err)
	}
	if out, err := projectOut(t, "add", "."); err != nil {
		t.Fatalf("add project: %s %v", out, err)
	}
	args := []string{"bundle", "add", ".", "operations", "agents=codex", "--json"}
	out, err := projectOut(t, append([]string{"--plan"}, args...)...)
	if err != nil {
		t.Fatalf("preview: %s %v", out, err)
	}
	var plan library.Result
	if err := json.Unmarshal([]byte(out), &plan); err != nil || plan.PlanID == "" || len(plan.Writes) == 0 {
		t.Fatalf("preview result: %s %v", out, err)
	}
	for _, rel := range []string{".agents/skills/runbook", ".codex/hooks.json", ".claude/skills/runbook"} {
		if _, err := os.Lstat(filepath.Join(project, rel)); !os.IsNotExist(err) {
			t.Fatalf("preview created %s: %v", rel, err)
		}
	}
	if out, err := projectOut(t, append([]string{"--plan-id=" + plan.PlanID}, args...)...); err != nil {
		t.Fatalf("apply: %s %v", out, err)
	}
	if !linkTo(t, filepath.Join(project, ".agents/skills/runbook"), "runbook") {
		t.Fatal("the Codex project did not receive a skill")
	}
	raw, err := os.ReadFile(filepath.Join(project, ".codex/hooks.json"))
	if err != nil || strings.Count(string(raw), "fixture_done") != 1 {
		t.Fatalf("the Codex project hook: %s %v", raw, err)
	}
	if _, err := os.Lstat(filepath.Join(project, ".claude/skills/runbook")); !os.IsNotExist(err) {
		t.Fatalf("an unselected agent received the skill: %v", err)
	}
	if _, err := projectOut(t, append([]string{"--plan-id=" + plan.PlanID}, args...)...); err == nil || !strings.Contains(err.Error(), "plan changed") {
		t.Fatalf("the applied plan ID should now be stale: %v", err)
	}
	args[1] = "remove"
	out, err = projectOut(t, append([]string{"--plan"}, args...)...)
	if err != nil || json.Unmarshal([]byte(out), &plan) != nil || len(plan.Writes) == 0 ||
		!linkTo(t, filepath.Join(project, ".agents/skills/runbook"), "runbook") {
		t.Fatalf("withdraw preview: %s %v", out, err)
	}
	if out, err := projectOut(t, append([]string{"--plan-id=" + plan.PlanID}, args...)...); err != nil {
		t.Fatalf("withdraw: %s %v", out, err)
	}
	if _, err := os.Lstat(filepath.Join(project, ".agents/skills/runbook")); !os.IsNotExist(err) {
		t.Fatalf("the withdrawal left the assigned skill: %v", err)
	}
}
