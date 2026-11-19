package library

import (
	"path/filepath"
	"strings"
)

// PluginInstallState is whether an agent has a plugin the library keeps as a
// plugin, as that agent's own installer recorded it.
//
// The library decomposes a plugin into skills, MCP servers, hooks and
// subagents, and writes those. It does NOT install the plugin: Claude Code
// installs it from its own marketplace and records that in
// <claude dir>/plugins/installed_plugins.json, and a plugin with no record
// there does not load however many of its parts magpie wrote. Measured on a
// fresh config dir: `enabledPlugins` alone gives five cache-miss errors and no
// plugin; with the record installed the same session loads 4 agents and 67
// skills.
//
// So the parts and the install are two different facts, and the page has to be
// able to say which it has.
type PluginInstallState struct {
	// Agent is the harness whose record this is ("claude"; "" when unknown).
	Agent string `json:"agent"`
	// Installed is whether that agent has an install record for this plugin.
	Installed bool `json:"installed"`
	// Version and Scope are what the record says, when it says them.
	Version string `json:"version,omitempty"`
	Scope   string `json:"scope,omitempty"`
}

// PluginInstall reads whether claude has installed plugin name, by the record
// Claude Code keeps. A record magpie cannot read counts as not installed: the
// caller then says "not installed" rather than promising a load.
//
// scope "" is the whole answer: any record for this plugin counts. scope
// "local" with project dir takes only a record that belongs to THAT project:
// a local install is recorded against the folder it was made in, so one made
// in project A must not report project B as having the plugin. A user-scope
// record counts everywhere, because it is the user's, not a project's.
func PluginInstall(name string) PluginInstallState {
	return PluginInstallFor(name, "", "")
}

// PluginInstallFor is PluginInstall for one agent, scope and project.
func PluginInstallFor(name, agent, project string) PluginInstallState {
	if agent == "" {
		agent = "claude"
	}
	st := PluginInstallState{Agent: agent}
	if agent != "claude" {
		return st
	}
	doc := claudeRecords()
	if doc == nil {
		return st
	}
	for id, recs := range doc {
		// the record is keyed "<plugin>@<marketplace>"; the library keeps the
		// plugin's own name
		if base, _, ok := strings.Cut(id, "@"); !ok || base != name {
			continue
		}
		for _, r := range recs {
			if project != "" && r.Scope == "local" && !sameDir(r.ProjectPath, project) {
				continue // another project's local install is not this one's
			}
			st.Installed = true
			st.Version = r.Version
			st.Scope = r.Scope
			return st
		}
	}
	return st
}

// sameDir is whether two paths name one folder, through their real paths.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return realDirOf(a) == realDirOf(b)
}

func realDirOf(p string) string {
	abs, _ := filepath.Abs(p)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}
