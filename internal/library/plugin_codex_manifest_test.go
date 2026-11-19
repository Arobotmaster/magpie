package library

import (
	"path/filepath"
	"testing"
)

// A plugin that ships only Codex's manifest is a plugin: its skills and its
// hooks file are read from `.codex-plugin/plugin.json`. Before this, the
// folder named nothing and the hooks file beside it was never found.
func TestPluginFromCodexManifestAlone(t *testing.T) {
	h := sandbox(t)
	pluginAgents(t, h)
	src := filepath.Join(t.TempDir(), "cx-only")
	write(t, filepath.Join(src, ".codex-plugin/plugin.json"),
		`{"name":"cx-only","version":"1.0.0","description":"codex only","skills":"./skills/","hooks":"./hooks/codex-hooks.json"}`)
	write(t, filepath.Join(src, "skills/cx-skill/SKILL.md"), "---\nname: cx-skill\ndescription: A codex skill\n---\nbody\n")
	write(t, filepath.Join(src, "hooks/codex-hooks.json"),
		`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo cxdone"}]}]}}`)

	probe, err := ProbePlugins(src)
	if err != nil {
		t.Fatalf("ProbePlugins: %v", err)
	}
	if len(probe.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1: %+v", len(probe.Candidates), probe.Candidates)
	}
	if _, err := InstallPlugins(src, []string{"cx-only"}, nil); err != nil {
		t.Fatal(err)
	}
	v, err := Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	var skills, hooks int
	for _, s := range v.Skills {
		if s.Name == "cx-skill" && s.Bundle == "cx-only" {
			skills++
		}
	}
	for _, k := range v.Hooks {
		if k.Bundle == "cx-only" && k.Command == "echo cxdone" {
			hooks++
		}
	}
	if skills != 1 {
		t.Errorf("skills carried from .codex-plugin = %d, want 1", skills)
	}
	if hooks != 1 {
		t.Errorf("hooks carried from .codex-plugin = %d, want 1", hooks)
	}
}

// A plugin that ships both manifests, naming the same parts in each, carries
// each part once: `plugin.json#hooks` is a path to the same file the Claude
// side reads as hooks/, and reading both would write the hook twice.
func TestPluginBothManifestsCarryEachPartOnce(t *testing.T) {
	h := sandbox(t)
	pluginAgents(t, h)
	src := filepath.Join(t.TempDir(), "both")
	write(t, filepath.Join(src, ".claude-plugin/plugin.json"), `{"name":"both","version":"1.0.0"}`)
	write(t, filepath.Join(src, ".codex-plugin/plugin.json"),
		`{"name":"both","version":"1.0.0","skills":"./skills/","hooks":"./hooks/codex-hooks.json"}`)
	write(t, filepath.Join(src, "skills/shared/SKILL.md"), "---\nname: shared\ndescription: Shared\n---\nb\n")
	write(t, filepath.Join(src, "hooks/codex-hooks.json"),
		`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo oncetoday"}]}]}}`)

	if _, err := InstallPlugins(src, []string{"both"}, nil); err != nil {
		t.Fatal(err)
	}
	v, err := Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	var skills, hooks int
	for _, s := range v.Skills {
		if s.Name == "shared" {
			skills++
		}
	}
	for _, k := range v.Hooks {
		if k.Command == "echo oncetoday" {
			hooks++
		}
	}
	if skills != 1 {
		t.Errorf("skills = %d, want 1 (the same folder named by both manifests)", skills)
	}
	if hooks != 1 {
		t.Errorf("hooks = %d, want 1 (the same entry named by both manifests)", hooks)
	}
}
