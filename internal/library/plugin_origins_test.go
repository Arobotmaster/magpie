package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPluginProjectOriginsMarkManagedDeclarations(t *testing.T) {
	h := sandbox(t)
	project := filepath.Join(h, "project")
	write(t, filepath.Join(project, ".codex/config.toml"), "[mcp_servers.managed]\ncommand = 'true'\n[mcp_servers.native]\ncommand = 'true'\n")
	write(t, filepath.Join(project, ".pi/settings.json"), `{"packages":["./pkg",{"source":"./pkg","skills":["skills"]}]}`)
	l := &Library{Projects: []*Project{{
		Dir:       project,
		Wrote:     map[string][]string{".codex/config.toml": {"managed"}},
		PiEntries: []PiEntry{{Kind: "package", Source: "./pkg", Raw: `"./pkg"`}},
	}}}
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadPluginOrigins(project)
	if err != nil {
		t.Fatal(err)
	}
	var pi []bool
	codex := map[string]bool{}
	for _, row := range rows {
		if row.Scope != "project" || row.Discovered != nil {
			t.Fatalf("unexpected plugin origin: %+v", row)
		}
		switch row.Origin {
		case "pi-package":
			pi = append(pi, row.Managed)
		case "codex-mcp":
			codex[row.Name] = row.Managed
		}
	}
	if !slices.Equal(pi, []bool{true, false}) || len(codex) != 2 || !codex["managed"] || codex["native"] {
		t.Fatalf("ownership: Pi=%v, Codex=%v", pi, codex)
	}
}

func TestPluginOriginsSeparateBundleNativeAndProjectResources(t *testing.T) {
	h := sandbox(t)
	project := filepath.Join(h, "project")
	install := filepath.Join(h, ".claude/plugins/cache/example-kit/1")
	const id = "example-kit@market"
	write(t, filepath.Join(install, ".claude-plugin/plugin.json"), `{"name":"example-kit"}`)
	write(t, filepath.Join(install, ".mcp.json"), `{"lookup":{"command":"example-kit"},"other":{"command":"example-kit"}}`)
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
	write(t, filepath.Join(h, ".codex/config.toml"), "[mcp_servers.project-search]\ncommand = \"example-kit\"\n")
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
	var codexUser PluginOrigin
	for _, row := range rows {
		if row.Name == "elsewhere@market" || row.Discovered != nil {
			t.Fatalf("another project's install or unmeasured discovery: %+v", row)
		}
		if row.Origin == "codex-mcp" && row.Scope == "user" {
			codexUser = row
		} else {
			byOrigin[row.Origin] = row
		}
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
	part := func(row PluginOrigin, name, tool string) PluginPartState {
		t.Helper()
		for _, p := range row.Parts {
			if p.Name == name && p.Agent == tool {
				return p
			}
		}
		t.Fatalf("%s has no %s for %s", row.Name, name, tool)
		return PluginPartState{}
	}
	recordObservation := func(obs pluginDiscoveryObservation) error {
		t.Helper()
		b, err := json.Marshal(obs)
		if err != nil {
			t.Fatal(err)
		}
		return RecordPluginDiscovery(project, strings.NewReader(string(b)), false)
	}
	hook := part(bundle, "example-kit-notify", "claude")
	lookup := part(claude, "lookup", "claude")
	claudeObs := pluginDiscoveryObservation{Cwd: project, Tool: "claude",
		Seen: []string{claude.DiscoveryKey, lookup.DiscoveryKey}, Missing: []string{hook.DiscoveryKey}}
	if err := recordObservation(claudeObs); err == nil {
		t.Fatal("accepted absence from a partial observation")
	}
	claudeObs.Complete = []string{"hook"}
	wrongDir := claudeObs
	wrongDir.Cwd = h
	if err := recordObservation(wrongDir); err == nil {
		t.Fatal("accepted an observation from another directory")
	}
	wrongTool := claudeObs
	wrongTool.Tool = "codex"
	if err := recordObservation(wrongTool); err == nil {
		t.Fatal("accepted evidence under the wrong tool")
	}
	for _, obs := range []pluginDiscoveryObservation{
		claudeObs,
		{Cwd: project, Tool: "codex", Seen: []string{codexUser.Parts[0].DiscoveryKey}},
		{Cwd: project, Tool: "pi", Seen: []string{pi.Parts[0].DiscoveryKey}},
	} {
		if err := recordObservation(obs); err != nil {
			t.Fatal(err)
		}
	}
	check := func(state *bool, want string) {
		t.Helper()
		got := "unknown"
		if state != nil && *state {
			got = "yes"
		} else if state != nil {
			got = "no"
		}
		if got != want {
			t.Fatalf("discovery = %s, want %s", got, want)
		}
	}
	rows, err = ReadPluginOrigins(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		switch row.Origin {
		case "library":
			check(part(row, "example-kit-notify", "claude").Discovered, "no")
			check(part(row, "example-kit-notify", "codex").Discovered, "unknown")
		case "claude-native":
			check(row.Discovered, "yes")
			check(part(row, "lookup", "claude").Discovered, "yes")
			check(part(row, "other", "claude").Discovered, "unknown")
			if row.Enabled == nil || *row.Enabled {
				t.Fatalf("session evidence changed the enable state: %+v", row)
			}
		case "codex-mcp":
			if row.Scope == "user" {
				check(row.Discovered, "yes")
				check(row.Parts[0].Discovered, "yes")
			} else {
				check(row.Discovered, "unknown")
			}
		case "pi-package":
			check(row.Discovered, "yes")
		case "project":
			check(row.Discovered, "unknown")
		}
	}
	for _, dir := range []string{"", h} {
		rows, err := ReadPluginOrigins(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			check(row.Discovered, "unknown")
			for _, p := range row.Parts {
				check(p.Discovered, "unknown")
			}
		}
	}
	if err := recordObservation(pluginDiscoveryObservation{Cwd: project, Tool: "claude"}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(pluginDiscoveryPath(realDirOf(project), "claude")); err != nil || string(data) != "{}" {
		t.Fatalf("new partial observation did not replace the old one: %q, %v", data, err)
	}
	if read(t, filepath.Join(project, ".claude/settings.json")) != flags {
		t.Fatal("observation changed project configuration")
	}
}

func TestPluginDiscoveryClaudeStartupRequiresExactNativeSource(t *testing.T) {
	sandbox(t)
	project := t.TempDir()
	install := t.TempDir()
	const id = "example-kit@market"
	records := map[string][]claudeInstallRecord{
		id:                  {{Scope: "user", Version: "1", InstallPath: install}},
		"example-kit@other": {{Scope: "user", Version: "1", InstallPath: install}},
	}
	save := func() {
		t.Helper()
		record, err := json.Marshal(map[string]any{"plugins": records})
		if err != nil {
			t.Fatal(err)
		}
		write(t, claudeInstalled(), string(record))
	}
	save()
	event := `{"type":"system","subtype":"init","cwd":` + jsonQuote(project) +
		`,"plugins":[{"name":"example-kit","source":"example-kit@market","version":"1"}]}`
	record := func() {
		t.Helper()
		if err := RecordPluginDiscovery(project, strings.NewReader(event), true); err != nil {
			t.Fatal(err)
		}
	}
	check := func(found bool) {
		t.Helper()
		rows, err := ReadPluginOrigins(project)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, row := range rows {
			if row.Discovered != nil {
				if !*row.Discovered || row.Name != id || row.Scope != "user" {
					t.Fatalf("unattributed discovery: %+v", row)
				}
				count++
			}
		}
		if (count == 1) != found {
			t.Fatalf("found %d matching startup registrations, want found=%v", count, found)
		}
	}
	record()
	check(true)
	records[id] = append(records[id], claudeInstallRecord{Scope: "local", ProjectPath: project, Version: "1", InstallPath: install})
	save()
	record()
	check(false)
	if err := RecordPluginDiscovery(t.TempDir(), strings.NewReader(event), true); err == nil {
		t.Fatal("accepted startup from another project")
	}
}
