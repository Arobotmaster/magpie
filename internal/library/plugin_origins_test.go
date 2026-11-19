package library

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
)

func TestPluginOriginsSeparateBundleNativeAndProjectResources(t *testing.T) {
	h := sandbox(t)
	project := filepath.Join(h, "project")
	install := filepath.Join(h, ".claude/plugins/cache/example-kit/1")
	const id = "example-kit@market"
	write(t, filepath.Join(install, ".claude-plugin/plugin.json"), `{"name":"example-kit"}`)
	write(t, filepath.Join(install, ".mcp.json"), `{"lookup":{"command":"example-kit"}}`)
	record, err := json.Marshal(map[string]any{"plugins": map[string][]claudeInstallRecord{
		id:                 {{Scope: "local", Version: "1", InstallPath: install, ProjectPath: project}},
		"elsewhere@market": {{Scope: "project", Version: "1", InstallPath: install, ProjectPath: filepath.Join(h, "other")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	write(t, claudeInstalled(), string(record))
	const flags = `{"enabledPlugins":{"example-kit@market":false}}`
	write(t, filepath.Join(project, ".claude/settings.json"), flags)
	write(t, filepath.Join(project, ".codex/config.toml"), "[mcp_servers.project-search]\ncommand = \"example-kit\"\n")
	write(t, filepath.Join(project, ".pi/settings.json"), `{"packages":["./lookup-package"]}`)
	write(t, filepath.Join(project, ".pi/lookup-package/package.json"), `{"version":"2"}`)
	l := &Library{
		Plugins: []*Plugin{{Name: "example-kit", Hooks: []string{"example-kit-notify"}}},
		Hooks:   []*Hook{{Name: "example-kit-notify", Bundle: "example-kit"}},
	}
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(pluginDir("example-kit"), ".claude-plugin/plugin.json"), `{"name":"example-kit"}`)

	rows, err := ReadPluginOrigins(project)
	if err != nil {
		t.Fatal(err)
	}
	byOrigin := map[string]PluginOrigin{}
	for _, row := range rows {
		if row.Name == "elsewhere@market" || row.Discovered != nil {
			t.Fatalf("another project's install or unmeasured discovery: %+v", row)
		}
		byOrigin[row.Origin] = row
	}
	bundle := byOrigin["library"]
	if bundle.Name != "example-kit" || !slices.ContainsFunc(bundle.Parts, func(p PluginPartState) bool { return p.Kind == "hook" }) ||
		slices.ContainsFunc(bundle.Parts, func(p PluginPartState) bool { return p.Kind == "mcp" }) {
		t.Fatalf("library parts: %+v", bundle)
	}
	claude := byOrigin["claude-native"]
	if claude.Name != id || claude.Scope != "local" || claude.Version != "1" || claude.Installed == nil || !*claude.Installed ||
		claude.Enabled == nil || *claude.Enabled || !slices.ContainsFunc(claude.Parts, func(p PluginPartState) bool {
		return p.Kind == "mcp" && p.Name == "lookup" && p.Agent == "claude"
	}) {
		t.Fatalf("native Claude state: %+v", claude)
	}
	codex, pi := byOrigin["codex-mcp"], byOrigin["pi-package"]
	if codex.Agent != "codex" || codex.Installed != nil || codex.Scope != "project" ||
		pi.Agent != "pi" || pi.Installed == nil || !*pi.Installed || pi.Version != "2" || pi.Scope != "project" {
		t.Fatalf("independent resources: codex=%+v pi=%+v", codex, pi)
	}
	if byOrigin["project"].Source != ".claude/settings.json" || read(t, filepath.Join(project, ".claude/settings.json")) != flags {
		t.Fatalf("project declaration was missing or changed: %+v", byOrigin["project"])
	}
}
