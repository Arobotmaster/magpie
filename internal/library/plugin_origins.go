package library

import (
	"encoding/json"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
)

// PluginStatus keeps different kinds of evidence independent. A missing
// value means the corresponding evidence was unavailable.
type PluginStatus struct {
	Installed  *bool `json:"installed"`
	Enabled    *bool `json:"enabled"`
	Discovered *bool `json:"discovered"`
}

type PluginPartState struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Agent string `json:"agent,omitempty"`
	PluginStatus
}

// PluginOrigin is one configuration or installation source. A library
// bundle, a native install and an independently configured MCP server or
// Pi package can coexist without sharing ownership or loaded state.
type PluginOrigin struct {
	Name    string            `json:"name"`
	Origin  string            `json:"origin"`
	Agent   string            `json:"agent,omitempty"`
	Scope   string            `json:"scope"`
	Source  string            `json:"source,omitempty"`
	Version string            `json:"version,omitempty"`
	Parts   []PluginPartState `json:"parts,omitempty"`
	PluginStatus
}

// ReadPluginOrigins inspects declarations and native records. It neither
// syncs the library nor starts tools to compensate for missing evidence.
func ReadPluginOrigins(dir string) ([]PluginOrigin, error) {
	if dir != "" {
		var err error
		if dir, err = ProjectPath(dir); err != nil {
			return nil, err
		}
	}
	mu.Lock()
	defer mu.Unlock()
	l, err := load()
	if err != nil {
		return nil, err
	}
	var project *Project
	if dir != "" {
		project = l.project(dir)
		if project == nil {
			for _, candidate := range l.Projects {
				if sameDir(candidate.Dir, dir) {
					project = candidate
					break
				}
			}
		}
		if project == nil {
			project = &Project{Dir: dir}
		}
	}
	rows := l.bundleOrigins(project)
	rows = append(rows, claudePluginOrigins(project)...)
	rows = append(rows, codexMCPOrigins(project)...)
	rows = append(rows, piPluginOrigins(project)...)
	return rows, nil
}

type bundleComponent struct {
	kind, name string
	agents     []string
}

func (l *Library) bundleOrigins(project *Project) []PluginOrigin {
	out := []PluginOrigin{}
	for _, b := range l.pluginsView() {
		have := !b.Pending && isDir(b.Dir)
		row := PluginOrigin{Name: b.Name, Origin: "library", Scope: "library", Source: pluginDisplaySource(b.Source), Version: b.Version,
			PluginStatus: PluginStatus{Installed: new(have), Enabled: new(!b.Off)}}
		var parts []bundleComponent
		for _, n := range b.Skills {
			parts = append(parts, bundleComponent{"skill", n, l.skill(n).Agents})
		}
		for _, n := range b.Hooks {
			parts = append(parts, bundleComponent{"hook", n, l.hook(n).Agents})
		}
		for _, n := range b.Subagents {
			parts = append(parts, bundleComponent{"subagent", n, l.subagent(n).Agents})
		}
		for _, n := range b.MCP {
			parts = append(parts, bundleComponent{"mcp", n, l.server(n).Agents})
		}
		for _, part := range parts {
			row.Parts = append(row.Parts, PluginPartState{Kind: part.kind, Name: part.name,
				PluginStatus: PluginStatus{Installed: new(have)}})
			agents := slices.Concat([]string{"claude", "codex", "pi"}, part.agents)
			if project != nil {
				agents = append(agents, project.bundlePartAgents(part)...)
			}
			slices.Sort(agents)
			for _, id := range slices.Compact(agents) {
				on := slices.Contains(part.agents, id)
				state := PluginStatus{Enabled: new(on)}
				if project != nil {
					state.Enabled = new(project.bundlePartEnabled(part, id, on))
					state.Installed = project.bundlePartWritten(part, id)
				}
				row.Parts = append(row.Parts, PluginPartState{Kind: part.kind, Name: part.name, Agent: id,
					PluginStatus: state})
			}
		}
		out = append(out, row)
	}
	return out
}

func pluginDisplaySource(source string) string {
	if u, err := url.Parse(source); err == nil && u.Host != "" {
		u.User, u.RawQuery, u.Fragment = nil, "", ""
		return u.String()
	}
	return source
}

func (p *Project) bundlePartAgents(part bundleComponent) []string {
	switch part.kind {
	case "skill":
		return p.Skills[part.name]
	case "hook":
		return p.Hooks[part.name]
	case "subagent":
		return p.Subagents[part.name]
	case "mcp":
		return p.Servers[part.name]
	}
	return nil
}

func (p *Project) bundlePartEnabled(part bundleComponent, id string, global bool) bool {
	if slices.Contains(p.bundlePartAgents(part), id) {
		return true
	}
	switch part.kind {
	case "skill":
		return global && !slices.Contains(p.SkillsOff[part.name], id)
	case "mcp":
		return global && !slices.Contains(p.ServersOff[part.name], id)
	}
	return global
}

func (p *Project) bundlePartWritten(part bundleComponent, id string) *bool {
	switch part.kind {
	case "skill":
		rel := ProjectSkillsDir(id) + "/" + part.name
		if slices.Contains(p.Placed, rel) {
			return new(ours(filepath.Join(p.Dir, filepath.FromSlash(rel)), part.name))
		}
	case "subagent":
		sp := projectSubagentSpec(p, id)
		if sp != nil {
			abs := sp.path(part.name)
			if slices.ContainsFunc(p.AgentFiles, func(f SubagentFile) bool { return f.Path == p.rel(abs) && f.Name == part.name }) {
				return pathPresent(abs)
			}
		}
	case "hook":
		for _, record := range p.HookFiles {
			if record.Name != part.name || ProjectHooksFile(id) != record.Path {
				continue
			}
			abs := filepath.Join(p.Dir, filepath.FromSlash(record.Path))
			if p.inside(abs) != nil {
				return nil
			}
			doc, err := readHooksFile(abs)
			if err != nil {
				return nil
			}
			entries, err := hookList(doc, abs, record.Event)
			if err != nil {
				return nil
			}
			return new(slices.ContainsFunc(entries, func(raw json.RawMessage) bool { return projectHookHash(raw) == record.Hash }))
		}
	case "mcp":
		rel, format := p.mcpRel(id)
		if slices.Contains(p.Wrote[rel], part.name) {
			abs := filepath.Join(p.Dir, filepath.FromSlash(rel))
			if p.inside(abs) != nil {
				return nil
			}
			entries, err := (&mcpFile{Path: abs, Format: format}).entries()
			if err == nil {
				_, present := entries[part.name]
				return new(present)
			}
		}
	}
	return nil
}

func claudePluginOrigins(p *Project) []PluginOrigin {
	records, recordErr := readClaudeInstallRecords()
	var enabled map[string]bool
	if doc, err := readClaudeJSON(claudeSettings()); err == nil {
		enabled = claudePluginFlags(doc)
	}
	var out []PluginOrigin
	if p != nil {
		for _, rel := range []string{".claude/settings.json", ProjectClaudeSettings("claude")} {
			abs := filepath.Join(p.Dir, filepath.FromSlash(rel))
			if p.inside(abs) != nil {
				enabled = nil
				continue
			}
			doc, err := readClaudeJSON(abs)
			if err != nil {
				enabled = nil
				continue
			}
			flags := claudePluginFlags(doc)
			for _, id := range slices.Sorted(maps.Keys(flags)) {
				on := flags[id]
				if enabled == nil {
					enabled = map[string]bool{}
				}
				enabled[id] = on
				origin := "project"
				if wrote, ok := p.ClaudeWritten[id]; rel == ProjectClaudeSettings("claude") && ok && wrote == on {
					origin = "library"
				}
				out = append(out, PluginOrigin{Name: id, Origin: origin, Agent: "claude", Scope: "project", Source: rel,
					PluginStatus: PluginStatus{Enabled: new(on)}})
			}
		}
		if manifest := filepath.Join(p.Dir, ".claude-plugin", "plugin.json"); p.inside(manifest) == nil && exists(manifest) {
			if plug, err := readPlugin(p.Dir, filepath.Base(p.Dir), "", nil); err == nil {
				out = append(out, PluginOrigin{Name: plug.name, Origin: "project", Agent: "claude", Scope: "project",
					Source: ".claude-plugin/plugin.json", Version: plug.version, Parts: claudeParts(plug)})
			}
		}
	}
	found := map[string]bool{}
	for _, id := range slices.Sorted(maps.Keys(records)) {
		for _, record := range records[id] {
			if record.Scope != "user" && (p == nil || !sameDir(record.ProjectPath, p.Dir)) {
				continue
			}
			found[id] = true
			row := PluginOrigin{Name: id, Origin: "claude-native", Agent: "claude", Scope: record.Scope,
				Source: record.InstallPath, Version: record.Version}
			if on, ok := enabled[id]; ok {
				row.Enabled = new(on)
			}
			if record.InstallPath != "" {
				row.Installed = pathPresent(record.InstallPath)
				if plug, err := readPlugin(record.InstallPath, id, "", nil); err == nil {
					row.Parts = claudeParts(plug)
				}
			}
			out = append(out, row)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(enabled)) {
		if found[id] {
			continue
		}
		row := PluginOrigin{Name: id, Origin: "claude-native", Agent: "claude", Scope: "unknown",
			PluginStatus: PluginStatus{Enabled: new(enabled[id])}}
		if recordErr == nil {
			row.Installed = new(false)
		}
		out = append(out, row)
	}
	return out
}

func pathPresent(path string) *bool {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return new(true)
	case os.IsNotExist(err):
		return new(false)
	default:
		return nil
	}
}

func claudePluginFlags(doc []byte) map[string]bool {
	out := map[string]bool{}
	for id, value := range gjson.GetBytes(doc, "enabledPlugins").Map() {
		if value.IsBool() {
			out[id] = value.Bool()
		}
	}
	return out
}

func claudeParts(p *plLoaded) []PluginPartState {
	var out []PluginPartState
	add := func(kind, name string) {
		out = append(out, PluginPartState{Kind: kind, Name: name, Agent: "claude", PluginStatus: PluginStatus{Installed: new(true)}})
	}
	for _, s := range p.skills {
		add("skill", s.name)
	}
	for _, h := range p.hooks {
		if !strings.HasPrefix(h.Event, "codex:") {
			add("hook", h.Name)
		}
	}
	for _, a := range p.agents {
		add("subagent", a.Name)
	}
	for _, m := range p.servers {
		add("mcp", m.Name)
	}
	return out
}

func codexMCPOrigins(p *Project) []PluginOrigin {
	rel := ProjectMCPFile("codex")
	scope, path := "user", filepath.Join(codexDir(), "config.toml")
	if p != nil {
		scope, path = "project", filepath.Join(p.Dir, filepath.FromSlash(rel))
		if p.inside(path) != nil {
			return nil
		}
	}
	entries, err := (&mcpFile{Path: path, Format: fmtCodex}).entries()
	if err != nil {
		return nil
	}
	var rows []PluginOrigin
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		value, set := entries[name]["enabled"].(bool)
		on := new(!set || value)
		rows = append(rows, PluginOrigin{Name: name, Origin: "codex-mcp", Agent: "codex", Scope: scope, Source: path,
			PluginStatus: PluginStatus{Enabled: on},
			Parts:        []PluginPartState{{Kind: "mcp", Name: name, Agent: "codex", PluginStatus: PluginStatus{Enabled: on}}}})
	}
	return rows
}

func piPluginOrigins(p *Project) []PluginOrigin {
	user, err := userPiPath()
	if err != nil {
		return nil
	}
	var out []PluginOrigin
	for _, scope := range []string{"user", "project"} {
		path := user
		if scope == "project" {
			if p == nil || p.inside(piProjectPath(p.Dir)) != nil {
				continue
			}
			path = piProjectPath(p.Dir)
		}
		settings, err := readPiSettings(path)
		if err != nil {
			continue
		}
		place := piPlace{dir: filepath.Dir(path), home: home()}
		if scope == "user" {
			place.roots = piGlobalRoots()
		}
		for _, raw := range settings.pkgs {
			src, ok := piSourceOf(raw)
			if !ok {
				continue
			}
			pkg, ok := piPackageOf(raw)
			if !ok {
				continue
			}
			pkg.Source = src
			installed, version := place.installed(pkg)
			state := PluginStatus{Installed: new(installed), Enabled: new(piEntryOn("package", raw))}
			out = append(out, PluginOrigin{Name: src, Origin: "pi-package", Agent: "pi", Scope: scope, Source: path, Version: version,
				PluginStatus: state, Parts: []PluginPartState{{Kind: "package", Name: src, Agent: "pi", PluginStatus: state}}})
		}
	}
	return out
}
