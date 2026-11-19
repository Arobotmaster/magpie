package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func claudePluginProjects(t *testing.T) (h, proj string) {
	h = sandbox(t)
	proj = filepath.Join(t.TempDir(), "site")
	gitInit(t, proj)
	ok(t)(AddProject(proj))
	return h, proj
}

func readClaudeLocal(t *testing.T, proj string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(proj, ".claude", "settings.local.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func enabledPluginsIn(t *testing.T, proj string) map[string]bool {
	t.Helper()
	m := readClaudeLocal(t, proj)
	out := map[string]bool{}
	if m == nil {
		return out
	}
	raw, ok := m["enabledPlugins"]
	if !ok {
		return out
	}
	for k, v := range raw.(map[string]any) {
		if b, ok := v.(bool); ok {
			out[k] = b
		}
	}
	return out
}

// A project turns a Claude plugin on in its own settings.local.json, and
// the user's settings.json is not touched.
func TestProjectClaudePluginOn(t *testing.T) {
	h, proj := claudePluginProjects(t)
	ok(t)(ProjectClaudePlugin(proj, "example-kit@example-kit", true))

	got := enabledPluginsIn(t, proj)
	if !got["example-kit@example-kit"] {
		t.Fatalf("the project doesn't turn it on: %v", got)
	}
	// the user's settings.json is untouched
	user := filepath.Join(h, ".claude", "settings.json")
	if raw, err := os.ReadFile(user); err == nil && string(raw) != "" {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			if ep, ok := m["enabledPlugins"].(map[string]any); ok && len(ep) > 0 {
				t.Errorf("the user's enabledPlugins was written: %v", ep)
			}
		}
	}
}

// Everything else in the project's settings.local.json stays: its hooks,
// permissions, and any other key.
func TestProjectClaudePluginKeepsTheRest(t *testing.T) {
	_, proj := claudePluginProjects(t)
	write(t, filepath.Join(proj, ".claude", "settings.local.json"), `{
  "hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "echo hi"}]}]},
  "permissions": {"allow": ["Bash(ls:*)"]},
  "enabledPlugins": {"eli5@claude-community": true}
}`)
	ok(t)(ProjectClaudePlugin(proj, "example-kit@example-kit", true))

	m := readClaudeLocal(t, proj)
	if m["hooks"] == nil {
		t.Errorf("the user's hooks were dropped: %v", m)
	}
	if m["permissions"] == nil {
		t.Errorf("the user's permissions were dropped: %v", m)
	}
	got := enabledPluginsIn(t, proj)
	if !got["eli5@claude-community"] {
		t.Errorf("an enabledPlugins entry magpie didn't write was lost: %v", got)
	}
	if !got["example-kit@example-kit"] {
		t.Errorf("magpie's entry is missing: %v", got)
	}
}

// A value the user set themselves is never overwritten nor taken away.
func TestProjectClaudePluginLeaveTheUsersValue(t *testing.T) {
	_, proj := claudePluginProjects(t)
	write(t, filepath.Join(proj, ".claude", "settings.local.json"), `{"enabledPlugins":{"example-kit@example-kit":false}}`)
	ok(t)(ProjectClaudePlugin(proj, "example-kit@example-kit", true))
	if got := enabledPluginsIn(t, proj); got["example-kit@example-kit"] {
		t.Errorf("a value the user set was overwritten: %v", got)
	}
}

func TestProjectClaudePluginDoesNotClaimExistingKeys(t *testing.T) {
	_, proj := claudePluginProjects(t)
	path := filepath.Join(proj, ".claude", "settings.local.json")
	write(t, path, `{"enabledPlugins":{"mine@market":true,"opaque@market":"keep"}}`)
	ok(t)(ProjectClaudePlugin(proj, "mine@market", true))
	ok(t)(ProjectClaudePlugin(proj, "ours@market", true))
	ok(t)(ProjectClaudePlugin(proj, "mine@market", false))
	ok(t)(RemoveProject(proj, false))
	enabled := jsonOf(t, path)["enabledPlugins"].(map[string]any)
	if len(enabled) != 2 || enabled["mine@market"] != true || enabled["opaque@market"] != "keep" {
		t.Errorf("changed Claude's own entries: %v", enabled)
	}
}

// Turning it off in the project is an explicit false, which Claude Code
// reads as the project overriding the user's setting.
func TestProjectClaudePluginOff(t *testing.T) {
	_, proj := claudePluginProjects(t)
	ok(t)(ProjectClaudePlugin(proj, "example-kit@example-kit", true))
	ok(t)(ProjectClaudePlugin(proj, "example-kit@example-kit", false))
	got := enabledPluginsIn(t, proj)
	v, present := got["example-kit@example-kit"]
	if !present || v {
		t.Errorf("the project doesn't turn it off: present=%v value=%v", present, v)
	}
}

// A file magpie made and then emptied is taken away; a file holding
// anything else is written back.
func TestProjectClaudePluginRemoveTheirOwnFileOnly(t *testing.T) {
	_, proj := claudePluginProjects(t)
	ok(t)(ProjectClaudePlugin(proj, "a@b", true))
	path := filepath.Join(proj, ".claude", "settings.local.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("magpie didn't write it: %v", err)
	}
	// drop the record and the want, as a project removal does
	ok(t)(changeProject(proj, projectPlugins, func(l *Library) error {
		l.project(proj).ClaudePlugins = nil
		return nil
	}))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a file magpie made and emptied was left behind")
	}

	write(t, filepath.Join(proj, ".claude", "settings.local.json"), `{"hooks":{}}`)
	ok(t)(ProjectClaudePlugin(proj, "a@b", true))
	ok(t)(changeProject(proj, projectPlugins, func(l *Library) error {
		l.project(proj).ClaudePlugins = nil
		return nil
	}))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a file holding the user's own keys was removed: %v", err)
	}
}

// A plugin id must look like name@marketplace.
func TestProjectClaudePluginIdChecked(t *testing.T) {
	_, proj := claudePluginProjects(t)
	if _, err := ProjectClaudePlugin(proj, "example-kit", true); err == nil {
		t.Error("an id without a marketplace was accepted")
	}
}

// Only Claude Code has a project file this writes.
func TestProjectClaudeSettingsAgents(t *testing.T) {
	if ProjectClaudeSettings("claude") == "" {
		t.Error("claude names no project settings file")
	}
	for _, id := range []string{"codex", "pi", "gemini"} {
		if ProjectClaudeSettings(id) != "" {
			t.Errorf("%s names a project Claude settings file it doesn't have", id)
		}
	}
}
