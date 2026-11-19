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
	return changeProjectBundle(dir, name, agents, true, options...)
}

// RemoveProjectBundle releases the assignments introduced by this bundle.
func RemoveProjectBundle(dir, name string, agents []string, options ...ProjectWriteOptions) (*Result, error) {
	return changeProjectBundle(dir, name, agents, false, options...)
}

func changeProjectBundle(dir, name string, agents []string, on bool, options ...ProjectWriteOptions) (*Result, error) {
	ids := slices.Compact(slices.Sorted(slices.Values(agents)))
	if name == "" || len(ids) == 0 {
		return nil, errors.New("name the project bundle and its agents")
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
		w := p.writes
		w.bundleParts = map[projectBundlePart]bool{}
		w.intent = fmt.Sprintf("bundle %t %q %q %q", on, dir, name, ids)
		beforeHubs := l.projectHubs(p)
		defer func() {
			if w.parts&projectServers != 0 {
				afterHubs := l.projectHubs(p)
				w.bundleClaudeHub = (beforeHubs["claude"] == nil) != (afterHubs["claude"] == nil)
				w.bundleCodexHub = (beforeHubs["codex"] == nil) != (afterHubs["codex"] == nil)
			}
		}()
		if !on {
			return p.withdrawBundle(name, ids, nil)
		}
		b := l.plugin(name)
		if b == nil {
			return fmt.Errorf("no library bundle %q", name)
		}
		if b.Pending || !isDir(pluginDir(b.Name)) {
			return fmt.Errorf("library bundle %q is not available on this machine", name)
		}
		w.intent += fmt.Sprintf(" %q %q %q", b.Source, b.Version, b.Installed)
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
				continue
			}
			if native[i] != "" {
				w.skipped = append(w.skipped, Problem{
					Agent: id, What: "bundle:" + b.Name, Error: "already supplied by enabled native plugin " + native[i],
				})
				if err := p.withdrawBundle(name, []string{id}, func(part projectBundlePart) bool {
					return nativeBundleSupplies(id, part.Kind)
				}); err != nil {
					return err
				}
			}
			if err := l.assignProjectBundle(p, b, id, native[i] != ""); err != nil {
				return err
			}
		}
		var distributed []string
		for _, id := range ids {
			if p.distributesBundleTo(id) {
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

// Codex's plugin loader supplies skills, MCP and hooks. It does not load the
// bundle's Claude-format role, which the project writer converts to TOML.
func nativeBundleSupplies(id, kind string) bool {
	return id == "claude" || id == "codex" && kind != "subagent"
}

func (p *Project) distributesBundleTo(id string) bool {
	for part, add := range p.writes.bundleParts {
		if add && part.Agent == id && nativeBundleSupplies(id, part.Kind) &&
			(part.Kind == "skill" && p.Distribution.Skills || part.Kind == "subagent" && p.Distribution.Subagents) {
			return true
		}
	}
	return false
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
	}{
		{"skill", b.Skills}, {"subagent", b.Subagents}, {"mcp", b.MCP}, {"hook", b.Hooks},
	}
	for _, g := range groups {
		if native && nativeBundleSupplies(id, g.kind) {
			continue
		}
		for _, name := range g.names {
			part := projectBundlePart{Kind: g.kind, Name: name, Agent: id}
			if p.bundlePartAssigned(part) {
				p.shareBundlePart(b.Name, part)
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
			if g.kind == "hook" && p.shareBundleHook(b.Name, part, l.hook(name)) {
				continue
			}
			p.assignBundlePart(b.Name, part)
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
