package library

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginProvenanceSeparatesInstallAndStartup(t *testing.T) {
	h := sandbox(t)
	p := t.TempDir()
	write(t, filepath.Join(h, ".claude/plugins/installed_plugins.json"),
		`{"version":2,"plugins":{"demo@local":[{"scope":"user","version":"1.2.3"},{"scope":"local","projectPath":"/another/project","version":"0.9.0"}]}}`)
	matches := func(name string) bool { return name == "demo" }
	claude := toolProvenance("claude", claudeNativeVersions(matches, p))
	if len(claude.NativeInstalls) != 1 || claude.NativeInstalls[0].Version != "1.2.3" || claude.LoadStatus != "unknown" {
		t.Fatalf("native record was confused with startup: %+v", claude)
	}
	event := `{"type":"system","subtype":"init","cwd":` + jsonQuote(p) + `,"plugins":[{"name":"demo","source":"demo@local","version":"2.0.0"}]}`
	if err := readClaudeStartup(strings.NewReader(event), matches, p, &claude); err != nil {
		t.Fatal(err)
	}
	if len(claude.Loaded) != 1 || claude.Loaded[0].Version != "2.0.0" || claude.NativeInstalls[0].Version != "1.2.3" {
		t.Fatalf("startup evidence replaced the install record: %+v", claude)
	}
	if err := readClaudeStartup(strings.NewReader(event), matches, t.TempDir(), &claude); err == nil {
		t.Fatal("accepted another project's startup evidence")
	}
	write(t, filepath.Join(h, ".claude/plugins/installed_plugins.json"), "{")
	if row := toolProvenance("claude", claudeNativeVersions(matches, p)); row.NativeStatus != "unknown" {
		t.Fatalf("unreadable native state got reported as known: %+v", row)
	}
}
