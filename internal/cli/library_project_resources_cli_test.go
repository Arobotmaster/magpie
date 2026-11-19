package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLibraryProjectResourceSources(t *testing.T) {
	dirs := projectResourceFixtures(t)
	tests := []struct {
		kind, native, assigned, legacy string
	}{
		{"skills", "Native skill description", "pdf", "skills"},
		{"hook", "verify.sh · PreToolUse", "Managed hook description", "hooks"},
		{"server", "duplicate-server", "duplicate-server", "servers"},
		{"subagent", "Native role description", "Managed role description", "subagents"},
		{"pi", "npm:native-fixture", "npm:managed-fixture", "piEntries"},
		{"plugin", "fixture-manifest", "assigned@fixture", "claudePlugins"},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			for _, locale := range []string{"en_US.UTF-8", "zh_CN.UTF-8"} {
				t.Run(locale, func(t *testing.T) {
					t.Setenv("LC_ALL", locale)
					nativeLabel, nativeDetail := "Project-provided · ", "From this project's own files. Read-only in MagPie."
					assignedLabel, assignedDetail := "Assigned by MagPie · ", "Configured for this project through MagPie."
					if strings.HasPrefix(locale, "zh") {
						nativeLabel, nativeDetail = "项目自带 · ", "来自项目自己的文件，在 MagPie 中只读。"
						assignedLabel, assignedDetail = "MagPie 分配 · ", "通过 MagPie 为此项目配置。"
					}
					for _, mode := range []string{"mixed", "native", "assigned", "empty"} {
						dir := dirs[mode]
						native := mode == "mixed" || mode == "native"
						assigned := mode == "mixed" || mode == "assigned"
						nativeCount, assignedCount := 1, 1
						if tt.kind == "plugin" {
							nativeCount, assignedCount = 3, 2 // also its Codex MCP and Pi package declarations
						}
						if !native {
							nativeCount = 0
						}
						if !assigned {
							assignedCount = 0
						}
						out, err := projectOut(t, tt.kind, dir)
						if err != nil {
							t.Fatal(err)
						}
						for _, check := range []struct {
							present bool
							text    string
						}{{native, nativeLabel + strconv.Itoa(nativeCount)}, {native, nativeDetail}, {assigned, assignedLabel + strconv.Itoa(assignedCount)}, {assigned, assignedDetail}} {
							if strings.Contains(out, check.text) != check.present {
								t.Errorf("%s: %q presence want %t in %q", mode, check.text, check.present, out)
							}
						}
						if native && !strings.Contains(out, tt.native) || assigned && !strings.Contains(out, tt.assigned) {
							t.Errorf("%s: missing row description or name in %q", mode, out)
						}
						if tt.kind == "hook" && strings.Contains(out, "fixture-argument") {
							t.Errorf("raw command leaked to text: %q", out)
						}
						if tt.kind == "server" && mode == "mixed" && strings.Count(out, "duplicate-server") != 2 {
							t.Errorf("same-named servers must each have a row: %q", out)
						}
						checkProjectResourcesJSON(t, tt.kind, tt.legacy, dir, nativeCount, assignedCount)
					}
				})
			}
		})
	}
}

func checkProjectResourcesJSON(t *testing.T, kind, legacy, dir string, nativeCount, assignedCount int) {
	t.Helper()
	out, err := projectOut(t, kind, dir, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if kind == "server" || kind == "subagent" {
		var docs []map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &docs); err != nil || len(docs) != 1 {
			t.Fatalf("expected one project in the legacy array: %v, %s", err, out)
		}
		doc = docs[0]
	} else if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc[legacy] == nil || doc["dir"] == nil || doc["name"] == nil {
		t.Fatalf("legacy keys are missing: %s", out)
	}
	type resource struct {
		Name          string
		Origin        string
		Scope         string
		ProjectOrigin string
		DiscoveryKey  string
		Managed       *bool
	}
	key := "resources"
	if kind == "plugin" {
		key = "pluginOrigins"
	}
	var rows []resource
	if err := json.Unmarshal(doc[key], &rows); err != nil || rows == nil {
		t.Fatalf("expected an array of attributed resources: %s", out)
	}
	counts := map[string]int{}
	for _, row := range rows {
		origin := row.Origin
		if kind == "plugin" {
			origin = row.ProjectOrigin
			if row.Scope == "user" && (origin != "other" || row.Origin != "claude-native") {
				t.Fatalf("user installation was recategorized: %+v", row)
			}
			if row.Scope == "library" && (origin != "other" || row.Origin != "library") {
				t.Fatalf("global library inventory was recategorized: %+v", row)
			}
			if row.Name == "fixture-manifest" && (row.Origin != "project" || row.DiscoveryKey == "") {
				t.Fatalf("plugin discovery identity lost: %+v", row)
			}
		}
		if row.Managed == nil || *row.Managed != (origin == "magpie") {
			t.Fatalf("invalid source attribution: %+v", row)
		}
		counts[origin]++
	}
	if counts["project"] != nativeCount || counts["magpie"] != assignedCount {
		t.Errorf("source counts from %s: %v", out, counts)
	}
	if kind == "plugin" && counts["other"] < 2 {
		t.Errorf("user installation or global inventory is missing: %s", out)
	}
}

func projectResourceFixtures(t *testing.T) map[string]string {
	t.Helper()
	root := projectSandbox(t)
	home := os.Getenv("HOME")
	put := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dirs := map[string]string{}
	var records []map[string]any
	for _, mode := range []string{"mixed", "native", "assigned", "empty"} {
		dir := filepath.Join(root, mode)
		dirs[mode] = dir
		put(filepath.Join(dir, ".gitignore"), "")
		var packages []string
		if mode == "mixed" || mode == "native" {
			put(filepath.Join(dir, ".agents/skills/native-fixture/SKILL.md"), "---\nname: native-fixture\ndescription: Native skill description\n---\n")
			put(filepath.Join(dir, ".claude/settings.json"), `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"sh ./verify.sh fixture-argument"}]}]}}`)
			put(filepath.Join(dir, ".codex/config.toml"), "[mcp_servers.duplicate-server]\ncommand = 'true'\n")
			put(filepath.Join(dir, ".claude/agents/native-fixture.md"), "---\nname: native-fixture\ndescription: Native role description\n---\nRole.\n")
			put(filepath.Join(dir, ".claude-plugin/plugin.json"), `{"name":"fixture-manifest","version":"1.0.0"}`)
			packages = append(packages, "npm:native-fixture")
		}
		if mode == "mixed" || mode == "assigned" {
			packages = append(packages, "npm:managed-fixture")
			put(filepath.Join(dir, ".claude/settings.local.json"), `{"enabledPlugins":{"assigned@fixture":true}}`)
			records = append(records, map[string]any{
				"dir": dir, "skills": map[string][]string{"pdf": {"claude"}},
				"hooks": map[string][]string{"assigned-hook": {"claude"}}, "servers": map[string][]string{"duplicate-server": {"claude"}},
				"subagents":     map[string][]string{"assigned-role": {"claude"}},
				"piEntries":     []map[string]any{{"kind": "package", "source": "npm:managed-fixture"}},
				"claudePlugins": map[string]bool{"assigned@fixture": true}, "claudeWritten": map[string]bool{"assigned@fixture": true},
			})
		}
		if len(packages) > 0 {
			raw, _ := json.Marshal(map[string][]string{"packages": packages})
			put(filepath.Join(dir, ".pi/settings.json"), string(raw))
		}
	}
	put(filepath.Join(home, ".claude/plugins/installed_plugins.json"), `{"version":2,"plugins":{"external@fixture":[{"scope":"user","version":"1.0.0"}]}}`)
	config := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "magpie/library.json")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["projects"] = records
	doc["plugins"] = []map[string]any{{"name": "global-fixture", "manifests": []string{"claude"}, "hooks": []string{"assigned-hook"}}}
	doc["hooks"] = []map[string]any{{"name": "assigned-hook", "event": "after-tool", "description": "Managed hook description", "command": "true"}}
	doc["mcp"] = []map[string]any{{"name": "duplicate-server", "command": "true", "transport": "stdio"}}
	doc["subagents"] = []map[string]any{{"name": "assigned-role", "description": "Managed role description", "prompt": "Role."}}
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	put(config, string(raw))
	return dirs
}
