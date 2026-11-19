package library

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// A plugin's parts being written is not the plugin being installed: Claude Code
// installs it itself and records that in installed_plugins.json. The view has
// to be able to tell the two apart, or "the CLI wrote 67 skills" reads as
// "the plugin loads", which it does not (measured: five cache-miss errors and
// no plugin until the record exists).
func TestPluginInstallReadsTheAgentsRecord(t *testing.T) {
	h := sandbox(t)
	pluginAgents(t, h)

	if st := PluginInstall("acme"); st.Installed {
		t.Errorf("installed with no record: %+v", st)
	}

	write(t, filepath.Join(h, ".claude/plugins/installed_plugins.json"),
		`{"version":2,"plugins":{"acme@acme-market":[{"scope":"local","version":"1.2.3","installPath":"/tmp/a"}]}}`)
	st := PluginInstall("acme")
	if !st.Installed || st.Version != "1.2.3" || st.Scope != "local" || st.Agent != "claude" {
		t.Errorf("record not read: %+v", st)
	}

	// another plugin's record is not this one's
	if o := PluginInstall("other"); o.Installed {
		t.Errorf("another plugin's record was taken: %+v", o)
	}

	// an unreadable record says not installed rather than promising a load
	write(t, filepath.Join(h, ".claude/plugins/installed_plugins.json"), `{"plugins": `)
	if st := PluginInstall("acme"); st.Installed {
		t.Errorf("unreadable record read as installed: %+v", st)
	}
}

// A local install belongs to the project it was made in. A record for another
// project must not report this one as installed: Claude Code stores the folder
// in projectPath, so the answer is per project, not per plugin.
func TestPluginInstallIsPerProject(t *testing.T) {
	h := sandbox(t)
	pluginAgents(t, h)
	a, b := t.TempDir(), t.TempDir()

	write(t, filepath.Join(h, ".claude/plugins/installed_plugins.json"),
		`{"version":2,"plugins":{"acme@m":[{"scope":"local","version":"1.0.0","projectPath":`+jsonQuote(a)+`}]}}`)

	if st := PluginInstallFor("acme", "claude", a); !st.Installed {
		t.Errorf("the project it was installed for is not installed: %+v", st)
	}
	if st := PluginInstallFor("acme", "claude", b); st.Installed {
		t.Errorf("another project read the record as its own: %+v", st)
	}

	// a user-scope record is the user's, so every project has it
	write(t, filepath.Join(h, ".claude/plugins/installed_plugins.json"),
		`{"version":2,"plugins":{"acme@m":[{"scope":"user","version":"1.0.0"}]}}`)
	if st := PluginInstallFor("acme", "claude", b); !st.Installed {
		t.Errorf("a user-scope install was not seen by a project: %+v", st)
	}
}

// jsonQuote is s as a JSON string, for the record above.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
