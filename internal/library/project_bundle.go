package library

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
)

// ProjectBundle adds the supported parts of a library bundle to one project.
// It uses the same assignment maps and writers as the single-item commands.
func ProjectBundle(dir, name string, agents []string, options ...ProjectWriteOptions) (*Result, error) {
	ids := slices.Compact(slices.Sorted(slices.Values(agents)))
	if len(ids) == 0 {
		return nil, errors.New("name the agents for the project bundle")
	}
	for _, id := range ids {
		if id != "claude" && id != "codex" && id != "pi" {
			return nil, fmt.Errorf("%s does not support project bundles", id)
		}
	}
	return changeProject(dir, 0, func(l *Library) error {
		p := l.project(dir)
		if p == nil {
			return fmt.Errorf("no project %s", dir)
		}
		b := l.plugin(name)
		if b == nil {
			return fmt.Errorf("no library bundle %q", name)
		}
		if b.Pending || !isDir(pluginDir(b.Name)) {
			return fmt.Errorf("library bundle %q is not available on this machine", name)
		}
		w := p.writes
		w.intent = fmt.Sprintf("bundle add %q %q %q %q %q %q", dir, name, ids, b.Source, b.Version, b.Installed)
		for _, left := range b.Left {
			w.skipped = append(w.skipped, Problem{Agent: dir, What: left.What, Error: left.Why})
		}
		checkNative := func() ([]string, error) {
			var native []string
			for _, id := range ids {
				match, err := projectNativeBundle(p, b, id)
				if err != nil {
					return nil, err
				}
				native = append(native, match)
			}
			return native, nil
		}
		native, err := checkNative()
		if err != nil {
			return err
		}
		for i, id := range ids {
			if id == "pi" {
				skipped, err := l.assignProjectPiBundle(p, b)
				if err != nil {
					return err
				}
				w.skipped = append(w.skipped, skipped...)
				w.parts |= projectPi
				continue
			}
			if native[i] != "" {
				w.skipped = append(w.skipped, Problem{
					Agent: id, What: "bundle:" + b.Name, Error: "already supplied by enabled native plugin " + native[i],
				})
			}
			if err := l.assignProjectBundle(p, b, id, native[i] != ""); err != nil {
				return err
			}
		}
		var distributed []string
		for i, id := range ids {
			if native[i] == "" && p.distributesBundleTo(b, id) {
				distributed = append(distributed, id)
			}
		}
		w.verify = func() error {
			current, err := checkNative()
			if err != nil {
				return err
			}
			if !slices.Equal(current, native) {
				return errors.New("native plugin state changed during planning; preview again")
			}
			return l.checkBundleWorktrees(p, b, distributed)
		}
		return l.checkBundleWorktrees(p, b, distributed)
	}, options...)
}

func (p *Project) distributesBundleTo(b *Plugin, id string) bool {
	return p.Distribution.Skills && p.writes.parts&projectSkills != 0 &&
		slices.ContainsFunc(b.Skills, func(n string) bool { return slices.Contains(p.Skills[n], id) }) ||
		p.Distribution.Subagents && p.writes.parts&projectRoles != 0 &&
			slices.ContainsFunc(b.Subagents, func(n string) bool { return slices.Contains(p.Subagents[n], id) })
}

func (l *Library) checkBundleWorktrees(p *Project, b *Plugin, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	for _, dir := range p.treeDirs() {
		if slices.ContainsFunc(l.Projects, func(other *Project) bool { return sameDir(other.Dir, dir) }) {
			continue // separately registered projects own their assignments
		}
		for _, id := range ids {
			match, err := projectNativeBundle(&Project{Dir: dir}, b, id)
			if err != nil {
				return err
			}
			if match != "" {
				return fmt.Errorf("%s already uses native plugin %s in worktree %s", id, match, dir)
			}
		}
	}
	return nil
}

func (l *Library) assignProjectBundle(p *Project, b *Plugin, id string, native bool) error {
	groups := []struct {
		kind  string
		names []string
		set   *map[string][]string
		part  projectParts
	}{
		{"skill", b.Skills, &p.Skills, projectSkills},
		{"subagent", b.Subagents, &p.Subagents, projectRoles},
		{"mcp", b.MCP, &p.Servers, projectServers},
		{"hook", b.Hooks, &p.Hooks, projectHooks},
	}
	for _, g := range groups {
		for _, name := range g.names {
			before := (*g.set)[name]
			if native {
				// Keep assignments for all other agents, including those
				// sharing this project with an enabled native plugin.
				if slices.Contains(before, id) {
					after := set(before, id, false)
					if len(after) == 0 {
						delete(*g.set, name)
					} else {
						(*g.set)[name] = after
					}
					p.writes.parts |= g.part
				}
				continue
			}
			why, err := l.projectBundlePart(p, b.Name, g.kind, name, id)
			if err != nil {
				return err
			}
			if why != "" {
				p.writes.skipped = append(p.writes.skipped, Problem{Agent: id, What: g.kind + ":" + name, Error: why})
				continue
			}
			if *g.set == nil {
				*g.set = map[string][]string{}
			}
			(*g.set)[name] = set(before, id, true)
			p.writes.parts |= g.part
		}
	}
	return nil
}

func (l *Library) projectBundlePart(p *Project, bundle, kind, name, id string) (string, error) {
	const already = "already supplied by the project"
	missing := fmt.Errorf("library bundle %q has no owned %s %q", bundle, kind, name)
	switch kind {
	case "skill":
		if s := l.skill(name); s == nil || s.Bundle != bundle {
			return "", missing
		}
		rel := ProjectSkillsDir(id)
		native := map[string][]ProjectResource{rel: p.nativeSkills(rel)}
		if id == "claude" {
			native[".agents/skills"] = p.nativeSkills(".agents/skills")
		}
		if p.nativeSkillAt(native, rel, name) {
			return already, nil
		}
	case "subagent":
		if s := l.subagent(name); s == nil || s.Bundle != bundle {
			return "", missing
		}
		if p.hasNativeSubagent(id, name) {
			return already, nil
		}
	case "mcp":
		s := l.server(name)
		if s == nil || s.Bundle != bundle {
			return "", missing
		}
		if s.Hidden {
			return "the library server is turned off", nil
		}
		rel, format := p.mcpRel(id)
		abs := filepath.Join(p.Dir, filepath.FromSlash(rel))
		f := &mcpFile{Path: abs, Format: format}
		if err := f.supports(s); err != nil {
			return fmt.Sprintf("cannot configure this project server: %v", err), nil
		}
		if err := p.inside(abs); err != nil {
			return "", err
		}
		entries, err := f.entries()
		if err != nil {
			return "", err
		}
		if entries[name] != nil && !slices.Contains(p.Wrote[rel], name) {
			return already, nil
		}
	case "hook":
		h := l.hook(name)
		if h == nil || h.Bundle != bundle {
			return "", missing
		}
		ev, err := projectHookEvent(projectHookSpec(p, id), id, h)
		if err != nil {
			return err.Error(), nil
		}
		if hasNativeHook(p.nativeHooks(id), ev, h.Command) {
			return already, nil
		}
	}
	return "", nil
}
