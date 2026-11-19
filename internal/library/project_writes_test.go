package library

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func writesPath(r *Result, path string) bool {
	return slices.ContainsFunc(r.Writes, func(w ProjectWrite) bool { return w.Path == realBelow(path) && w.Action != "rmdir" })
}

func TestProjectWritesPreviewScopeAndNoop(t *testing.T) {
	h, main := projectLib(t)
	other, copyDir := filepath.Join(t.TempDir(), "other"), filepath.Join(t.TempDir(), "copy")
	gitInit(t, other)
	projectWorktree(t, main, copyDir)
	ok(t)(AddProject(other))

	pdf := filepath.Join(main, ".claude", "skills", "pdf")
	otherPDF := filepath.Join(other, ".claude", "skills", "pdf")
	copyPDF := filepath.Join(copyDir, ".claude", "skills", "pdf")
	global := filepath.Join(h, ".claude", "skills", "pdf")
	ok(t)(SkillAgents("pdf", []string{"claude"}))
	if err := os.Remove(global); err != nil {
		t.Fatal(err)
	}
	ok(t)(ProjectSkills(other, []string{"pdf"}, []string{"claude"}))
	if err := os.Remove(otherPDF); err != nil {
		t.Fatal(err)
	}
	gone(t, global)

	before := read(t, path())
	plan := ok(t)(ProjectSkills(main, []string{"pdf"}, []string{"claude"}, ProjectWriteOptions{Preview: true}))
	if !writesPath(plan, pdf) || writesPath(plan, otherPDF) || writesPath(plan, copyPDF) || writesPath(plan, global) {
		t.Fatalf("project preview: %+v", plan.Writes)
	}
	for _, file := range []string{pdf, otherPDF, copyPDF, global} {
		gone(t, file)
	}
	if got := read(t, path()); got != before {
		t.Fatal("preview saved library state")
	}
	if _, err := Read(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Glance(nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path()); got != before {
		t.Fatal("reading saved library state")
	}
	ok(t)(ProjectSkills(main, []string{"pdf"}, []string{"claude"}, ProjectWriteOptions{PlanID: plan.PlanID}))
	if !isLink(t, pdf) {
		t.Fatal("the selected project did not get its skill")
	}
	for _, file := range []string{otherPDF, copyPDF, global} {
		gone(t, file)
	}

	ok(t)(ProjectCopy(main, true))
	installed := filepath.Join(pdf, "SKILL.md")
	old := time.Unix(1600000000, 0)
	if err := os.Chtimes(installed, old, old); err != nil {
		t.Fatal(err)
	}
	if r := ok(t)(ProjectApply(main)); len(r.Writes) != 0 {
		t.Fatalf("identical project apply planned: %+v", r.Writes)
	}
	if st, err := os.Stat(installed); err != nil || !st.ModTime().Equal(old) {
		t.Fatalf("identical copied skill was rewritten: %v %v", st, err)
	}
	ok(t)(Sync())
	if !isLink(t, otherPDF) || !isLink(t, global) {
		t.Fatal("explicit full sync did not reconcile both scopes")
	}
	gone(t, copyPDF)
}

func TestProjectWritesDistributionIndependent(t *testing.T) {
	_, main := projectLib(t)
	copyDir := filepath.Join(t.TempDir(), "copy")
	projectWorktree(t, main, copyDir)
	role := reviewer
	role.Agents = nil
	ok(t)(SaveSubagent("", role))
	ok(t)(ProjectSkills(main, []string{"pdf"}, []string{"claude"}))
	ok(t)(ProjectSubagents(main, []string{role.Name}, []string{"claude"}))
	setInstr(t, main, "project guide", "claude")

	skill := filepath.Join(copyDir, ".claude", "skills", "pdf")
	agent := filepath.Join(copyDir, ".claude", "agents", role.Name+".md")
	instr := filepath.Join(copyDir, ProjectInstrFile("claude"))
	for _, f := range []string{skill, agent, instr} {
		gone(t, f)
	}
	plan := ok(t)(ProjectDistribute(main, ProjectDistribution{Subagents: true}, ProjectWriteOptions{Preview: true}))
	if !writesPath(plan, agent) || writesPath(plan, skill) || writesPath(plan, instr) {
		t.Fatalf("subagents-only plan: %+v", plan.Writes)
	}
	ok(t)(ProjectDistribute(main, ProjectDistribution{Subagents: true}, ProjectWriteOptions{PlanID: plan.PlanID}))
	if !strings.Contains(read(t, agent), "You review code.") {
		t.Fatal("worktree did not receive its opted-in subagent")
	}
	gone(t, skill)
	gone(t, instr)
	ok(t)(ProjectDistributeCategory(main, "instructions", true))
	if got := read(t, instr); !strings.Contains(got, "project guide") {
		t.Fatalf("worktree instructions: %q", got)
	}
	ok(t)(ProjectDistributeCategory(main, "skills", true))
	if !isLink(t, skill) {
		t.Fatal("worktree did not receive its opted-in skill")
	}
	ok(t)(ProjectDistributeCategory(main, "skills", false))
	ok(t)(ProjectSkills(main, []string{"pdf"}, nil))
	if !isLink(t, skill) {
		t.Fatal("disabling distribution changed the related worktree")
	}
}

func TestProjectWritesProtectTrackedAndForeign(t *testing.T) {
	_, dir := projectLib(t)
	rel := ProjectInstrFile("claude")
	f := filepath.Join(dir, rel)
	write(t, f, "tracked\n")
	projectGitCommand(t, dir, "add", "--", rel)
	projectGitCommand(t, dir, "commit", "-qm", "tracked")
	text, agents := "incoming", []string{"claude"}
	change := ProjectInstr{Text: &text, Agents: &agents}
	foreign, err := ProjectInstructions(dir, change)
	if err != nil || !problemAbout(foreign, "project:instructions") || read(t, f) != "tracked\n" {
		t.Fatal("user-maintained instructions were taken over")
	}
	for _, staged := range []bool{false, true} {
		if staged {
			projectGitCommand(t, dir, "add", "-u", "--", rel)
		} else if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
		plan, err := ProjectInstructions(dir, change, ProjectWriteOptions{Preview: true})
		if err != nil || len(plan.Problems) != 1 || !strings.Contains(plan.Problems[0].Error, "deleted Git-tracked") || writesPath(plan, f) {
			t.Fatalf("staged=%t: %+v", staged, plan)
		}
		if r, err := ProjectInstructions(dir, change); err != nil || !problemAbout(r, "project:instructions") {
			t.Fatalf("applying deletion guard: %+v %v", r, err)
		}
		gone(t, f)
	}

	skillDir := filepath.Join(dir, ".claude", "skills", "pdf")
	gitInit(t, skillDir)
	r, err := ProjectSkill(dir, "pdf", []string{"claude"})
	if err != nil || !problemAbout(r, "project:pdf") || isLink(t, skillDir) {
		t.Fatal("nested Git repository was treated as a skill destination")
	}

	ok(t)(ProjectSkill(dir, "pdf", nil))
	plan := ok(t)(ProjectSkill(dir, "docx", []string{"codex"}, ProjectWriteOptions{Preview: true}))
	target := filepath.Join(dir, ".agents", "skills", "docx")
	write(t, filepath.Join(target, "SKILL.md"), "theirs\n")
	if _, err := ProjectSkill(dir, "docx", []string{"codex"}, ProjectWriteOptions{PlanID: plan.PlanID}); err == nil {
		t.Fatal("an outdated preview was applied")
	}
	if read(t, filepath.Join(target, "SKILL.md")) != "theirs\n" {
		t.Fatal("foreign skill was changed")
	}
}
