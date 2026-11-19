package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/tidwall/gjson"
)

// ReadProject inspects even an unenrolled folder, without adding it or
// writing to its configuration.
func ReadProject(dir string) (*ProjectView, error) {
	p, _, err := readProject(dir, "")
	return p, err
}

// ReadProjectDescriptions reads the project and the relevant descriptions
// from the same library snapshot, without syncing or enrolling the folder.
func ReadProjectDescriptions(dir, kind string) (*ProjectView, map[string]string, error) {
	return readProject(dir, kind)
}

func readProject(dir, kind string) (*ProjectView, map[string]string, error) {
	dir, err := ProjectPath(dir)
	if err != nil {
		return nil, nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	l, err := load()
	if err != nil {
		return nil, nil, err
	}
	p := l.project(dir)
	if p == nil {
		if !isDir(dir) {
			return nil, nil, fmt.Errorf("%s isn't a project folder", dir)
		}
		key := realDir(dir)
		for _, candidate := range l.Projects {
			if realDir(candidate.Dir) == key {
				p = candidate
				break
			}
			if tree := candidate.tree(dir); tree != nil {
				p = tree
				break
			}
		}
	}
	if p == nil {
		p = &Project{Dir: dir}
	}
	l.Projects = []*Project{p}
	views := projectViews(l, nil)
	return &views[0], l.projectDescriptions(p, kind), nil
}

func (l *Library) projectDescriptions(p *Project, kind string) map[string]string {
	d := map[string]string{}
	switch kind {
	case "skills":
		for n := range p.Skills {
			if m, ok := readMeta(skillDir(n)); ok {
				d[n] = m.Description
			}
		}
		for n := range p.SkillsOff {
			if _, found := d[n]; !found {
				if m, ok := readMeta(skillDir(n)); ok {
					d[n] = m.Description
				}
			}
		}
	case "hooks":
		for n := range p.Hooks {
			if h := l.hook(n); h != nil {
				d[n] = h.Description
				if d[n] == "" {
					d[n] = h.Event
				}
			}
		}
	case "servers":
		for _, s := range l.MCP {
			if len(p.Servers[s.Name])+len(p.ServersOff[s.Name]) > 0 {
				d[s.Name] = s.Transport
			}
		}
	case "subagents":
		for n := range p.Subagents {
			if s := l.subagent(n); s != nil {
				d[n] = s.Description
			}
		}
	}
	return d
}

// ProjectResource describes an entry already supplied by the project.
type ProjectResource struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Agent       string   `json:"agent,omitempty"`
	Event       string   `json:"event,omitempty"`
	Description string   `json:"description,omitempty"`
	Agents      []string `json:"agents,omitempty"`
	Transport   string   `json:"transport,omitempty"`
	Off         bool     `json:"off,omitempty"`
}

func (p *Project) nativeSkills(rel string) []ProjectResource {
	var out []ProjectResource
	dir := filepath.Join(p.Dir, filepath.FromSlash(rel))
	if p.inside(dir) != nil {
		return out
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		path := rel + "/" + name
		abs := filepath.Join(dir, name)
		if slices.Contains(p.Placed, path) && ours(abs, name) || p.ownsSkillLink(path) {
			continue
		}
		m, ok := readMeta(abs)
		if ok {
			if m.Name != "" {
				name = m.Name
			}
			out = append(out, ProjectResource{Name: name, Path: path, Description: m.Description})
		}
	}
	return out
}

func nativeSkill(rows []ProjectResource, name string) bool {
	return slices.ContainsFunc(rows, func(r ProjectResource) bool {
		return strings.EqualFold(r.Name, name) || strings.EqualFold(filepath.Base(r.Path), name)
	})
}

func (p *Project) nativeSubagents(id string) []ProjectResource {
	sp := projectSubagentSpec(p, id)
	if sp == nil || p.inside(sp.dir) != nil {
		return nil
	}
	var out []ProjectResource
	entries, _ := os.ReadDir(sp.dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), sp.ext) {
			continue
		}
		abs := filepath.Join(sp.dir, e.Name())
		rel := p.rel(abs)
		body, err := os.ReadFile(abs)
		if err != nil || slices.ContainsFunc(p.AgentFiles, func(f SubagentFile) bool {
			return f.Path == rel && f.Hash == subagentHash(body)
		}) {
			continue
		}
		meta := parseMeta(body)
		if sp.ext == ".toml" {
			var role struct{ Name, Description string }
			if toml.Unmarshal(body, &role) == nil {
				meta.Name, meta.Description = role.Name, role.Description
			}
		}
		if meta.Name == "" {
			meta.Name = strings.TrimSuffix(e.Name(), sp.ext)
		}
		out = append(out, ProjectResource{Name: meta.Name, Path: rel, Agent: id, Description: meta.Description})
	}
	return out
}

func projectHookReadPaths(id string) []string {
	if id == "claude" {
		return []string{".claude/settings.json", projectHookFiles[id]}
	}
	return []string{projectHookFiles[id]}
}

type projectHookCommand struct {
	Command string `json:"command"`
	Type    string `json:"type"`
}

func projectHookCommands(raw json.RawMessage) []projectHookCommand {
	var e struct {
		projectHookCommand
		Hooks []projectHookCommand `json:"hooks"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return nil
	}
	if e.Command != "" {
		return []projectHookCommand{e.projectHookCommand}
	}
	return e.Hooks
}

func (p *Project) nativeHooks(id string) []ProjectResource {
	var out []ProjectResource
	for _, path := range projectHookReadPaths(id) {
		abs := filepath.Join(p.Dir, filepath.FromSlash(path))
		if p.inside(abs) != nil {
			continue
		}
		doc, err := readHooksFile(abs)
		if err != nil {
			continue
		}
		for event := range gjson.GetBytes(doc, "hooks").Map() {
			entries, err := hookList(doc, abs, event)
			if err != nil {
				continue
			}
			owned := map[string]bool{}
			for _, f := range p.HookFiles {
				if f.Path == path && f.Event == event {
					owned[f.Hash] = true
				}
			}
			for _, raw := range entries {
				hash := projectHookHash(raw)
				if owned[hash] {
					delete(owned, hash)
					continue
				}
				for _, h := range projectHookCommands(raw) {
					name := h.Command
					if name == "" {
						name = h.Type
					}
					if name != "" {
						out = append(out, ProjectResource{Name: name, Path: path, Agent: id, Event: event})
					}
				}
			}
		}
	}
	return out
}

func (p *Project) nativeResources() map[string][]ProjectResource {
	out := map[string][]ProjectResource{}
	for _, rel := range slices.Compact(slices.Sorted(maps.Values(projectSkillsDirs))) {
		out["skills"] = append(out["skills"], p.nativeSkills(rel)...)
	}
	for _, id := range ProjectHookAgents() {
		out["hooks"] = append(out["hooks"], p.nativeHooks(id)...)
	}
	for _, id := range slices.Sorted(maps.Keys(projectSubagentDirs)) {
		out["subagents"] = append(out["subagents"], p.nativeSubagents(id)...)
	}
	for _, id := range slices.Sorted(maps.Keys(projectMCPFiles)) {
		rel, format := p.mcpRel(id)
		abs := filepath.Join(p.Dir, filepath.FromSlash(rel))
		if p.inside(abs) != nil {
			continue
		}
		file := &mcpFile{Path: abs, Format: format}
		entries, err := file.entries()
		if err != nil {
			continue
		}
		for name, entry := range entries {
			if !slices.Contains(p.Wrote[rel], name) && !(id == "codex" && slices.Contains(p.CodexOff, name)) {
				row := ProjectResource{Name: name, Path: rel, Agent: id}
				// Only expose the transport. Configured URLs, arguments and
				// environment values can carry credentials.
				if server, ok := file.decode(name, entry); ok {
					row.Transport = server.Transport
				}
				if enabled, ok := entry["enabled"].(bool); ok && (format == fmtCodex || format == fmtOpenCode) {
					row.Off = !enabled
				}
				out["servers"] = append(out["servers"], row)
			}
		}
	}
	if p.inside(piProjectPath(p.Dir)) == nil {
		if config, err := readPiProject(p.Dir); err == nil {
			for _, kind := range []string{"package", "extension"} {
				for _, raw := range config.entries(kind) {
					if s, ok := piSourceOf(raw); ok && !p.ownsPiDeclaration(kind, s, raw) {
						out["pi"] = append(out["pi"], ProjectResource{Name: s, Path: piSettingsRel, Agent: "pi", Event: kind, Off: !piEntryOn(kind, raw)})
					}
				}
			}
		}
	}
	for _, rel := range p.nativeInstructionPaths() {
		abs := filepath.Join(p.Dir, filepath.FromSlash(rel))
		if p.inside(abs) != nil {
			continue
		}
		body, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		if slices.ContainsFunc(p.InstrFiles, func(f InstrFile) bool { return f.Path == rel }) {
			if before, _, after, ok := split(string(body)); ok && strings.TrimSpace(before+after) == "" {
				continue
			}
		}
		row := ProjectResource{Name: rel, Path: rel}
		for _, id := range slices.Sorted(maps.Keys(projectInstrNames)) {
			if rel == projectInstrRel(p.Dir, id, "") {
				row.Agents = append(row.Agents, id)
			}
		}
		out["instructions"] = append(out["instructions"], row)
	}
	for kind, rows := range out {
		if len(rows) == 0 {
			delete(out, kind)
			continue
		}
		slices.SortStableFunc(rows, compareProjectResources)
		out[kind] = slices.CompactFunc(rows, func(a, b ProjectResource) bool { return compareProjectResources(a, b) == 0 })
	}
	return out
}

func (p *Project) ownsPiDeclaration(kind, source string, raw json.RawMessage) bool {
	return slices.ContainsFunc(p.PiEntries, func(e PiEntry) bool {
		return e.Kind == kind && e.Source == source && (e.Raw == "" || e.Raw == piRawOf(raw))
	})
}

func compareProjectResources(a, b ProjectResource) int {
	return strings.Compare(a.Path+"\x00"+a.Agent+"\x00"+a.Event+"\x00"+a.Name, b.Path+"\x00"+b.Agent+"\x00"+b.Event+"\x00"+b.Name)
}

func (p *Project) nativeInstructionPaths() []string {
	names := map[string]bool{}
	for _, paths := range projectInstrNames {
		for _, path := range paths {
			names[path] = true
		}
	}
	// Use actual directory entries so a file appears once on
	// case-insensitive volumes too.
	var out []string
	for _, dir := range []string{".", ".claude"} {
		abs := filepath.Join(p.Dir, dir)
		if p.inside(abs) != nil {
			continue
		}
		entries, _ := os.ReadDir(abs)
		for _, e := range entries {
			rel := filepath.ToSlash(filepath.Join(dir, e.Name()))
			if !e.IsDir() && names[rel] {
				out = append(out, rel)
			}
		}
	}
	return out
}

var errProjectAlreadyHas = errors.New("project already has")

func (p *Project) nativeSkillAt(native map[string][]ProjectResource, dir, name string) bool {
	if p.ownsSkillLink(dir+"/"+name) || nativeSkill(native[dir], name) {
		return true
	}
	return dir == ".claude/skills" && nativeSkill(native[".agents/skills"], name)
}

func projectSkip(p *Project, what, name, target string) Problem {
	return Problem{Agent: p.Dir, What: "project:" + what, Error: fmt.Sprintf("%s %s (%s)", errProjectAlreadyHas, name, target)}
}

func withProjectSkips(res *Result, skipped []Problem) *Result {
	if res != nil {
		res.Skipped = append(res.Skipped, skipped...)
	}
	return res
}
