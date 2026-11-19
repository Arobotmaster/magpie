package library

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
)

// Keep bundle ownership separate from the existing single-item assignments.
// A missing record means the bundle has no right to withdraw the assignment.
type projectBundlePart struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Agent string `json:"agent"`
}

type projectBundleAssignment struct {
	Bundle string `json:"bundle"`
	projectBundlePart
	MCPHash string `json:"mcpHash,omitempty"`
}

func (part projectBundlePart) assignment(p *Project) (*map[string][]string, string, string, projectParts) {
	switch part.Kind {
	case "skill":
		return &p.Skills, part.Name, part.Agent, projectSkills
	case "subagent":
		return &p.Subagents, part.Name, part.Agent, projectRoles
	case "mcp":
		return &p.Servers, part.Name, part.Agent, projectServers
	case "hook":
		return &p.Hooks, part.Name, part.Agent, projectHooks
	case "package":
		return &p.Pi, "package", part.Name, projectPi
	default:
		return nil, "", "", 0
	}
}

func (p *Project) bundlePartAssigned(part projectBundlePart) bool {
	items, key, value, _ := part.assignment(p)
	return items != nil && slices.Contains((*items)[key], value)
}

func (p *Project) assignBundlePart(bundle string, part projectBundlePart) {
	items, key, value, mask := part.assignment(p)
	if *items == nil {
		*items = map[string][]string{}
	}
	(*items)[key] = set((*items)[key], value, true)
	p.BundleAssignments = append(p.BundleAssignments, projectBundleAssignment{Bundle: bundle, projectBundlePart: part})
	p.writes.parts |= mask
	p.writes.bundleParts[part] = true
}

func (p *Project) shareBundlePart(bundle string, part projectBundlePart) bool {
	for _, record := range p.BundleAssignments {
		if record.projectBundlePart != part {
			continue
		}
		if !slices.ContainsFunc(p.BundleAssignments, func(a projectBundleAssignment) bool {
			return a.Bundle == bundle && a.projectBundlePart == part
		}) {
			record.Bundle = bundle
			p.BundleAssignments = append(p.BundleAssignments, record)
		}
		return true
	}
	return false
}

func (p *Project) shareBundleHook(bundle string, part projectBundlePart, hook *Hook) bool {
	spec := projectHookSpec(p, part.Agent)
	event, err := projectHookEvent(spec, part.Agent, hook)
	if err != nil {
		return false
	}
	path := ProjectHooksFile(part.Agent)
	hash := projectHookHash(spec.entry(hook, event))
	for _, f := range p.HookFiles {
		if f.Path == path && f.Event == event && f.Hash == hash {
			stage, err := p.file(filepath.Join(p.Dir, filepath.FromSlash(path)))
			if err != nil {
				return false
			}
			doc, err := readHooksFile(stage)
			if err != nil {
				return false
			}
			list, err := hookList(doc, path, event)
			if err != nil || !slices.ContainsFunc(list, func(raw json.RawMessage) bool {
				return projectHookHash(raw) == hash
			}) {
				return false
			}
			part.Name = f.Name
			return p.bundlePartAssigned(part) && p.shareBundlePart(bundle, part)
		}
	}
	return false
}

func (p *Project) withdrawBundle(bundle string, agents []string, includes func(projectBundlePart) bool) error {
	var removed []projectBundleAssignment
	p.BundleAssignments = slices.DeleteFunc(p.BundleAssignments, func(a projectBundleAssignment) bool {
		if a.Bundle != bundle || !slices.Contains(agents, a.Agent) || includes != nil && !includes(a.projectBundlePart) {
			return false
		}
		removed = append(removed, a)
		return true
	})
	for _, a := range removed {
		part := a.projectBundlePart
		if !p.bundlePartAssigned(part) || slices.ContainsFunc(p.BundleAssignments, func(other projectBundleAssignment) bool {
			return other.projectBundlePart == part
		}) {
			continue
		}
		items, key, value, mask := part.assignment(p)
		if remaining := set((*items)[key], value, false); len(remaining) != 0 {
			(*items)[key] = remaining
		} else {
			delete(*items, key)
		}
		p.writes.parts |= mask
		p.writes.bundleParts[part] = false
		if part.Kind == "mcp" {
			if err := p.preserveBundleMCP(part, a.MCPHash); err != nil {
				return err
			}
		}
	}
	return nil
}

// The existing single-item editor takes precedence after an explicit edit.
func (p *Project) forgetBundlePart(kind, name string) {
	p.BundleAssignments = slices.DeleteFunc(p.BundleAssignments, func(a projectBundleAssignment) bool {
		return a.Kind == kind && a.Name == name
	})
}

// Match a changed assignment by the path its agent actually reads. Existing
// writers can keep a shared entry when another agent still wants that path.
func (p *Project) bundleChanges(kind, name, path string) bool {
	if p.writes == nil || p.writes.bundleParts == nil {
		return true
	}
	for part := range p.writes.bundleParts {
		if part.Kind != kind || part.Name != name {
			continue
		}
		var rel string
		switch kind {
		case "skill":
			rel = ProjectSkillsDir(part.Agent) + "/" + name
		case "subagent":
			if sp := projectSubagentSpec(p, part.Agent); sp != nil {
				rel = p.rel(sp.path(name))
			}
		case "hook":
			rel = ProjectHooksFile(part.Agent)
		case "mcp":
			rel, _ = p.mcpRel(part.Agent)
		}
		if path == rel {
			return true
		}
	}
	return false
}

func bundleMCPHash(entry map[string]any) string {
	if entry == nil {
		return ""
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (p *Project) setBundleHubHash(agent string, entry map[string]any) {
	if p.BundleHubHashes == nil {
		p.BundleHubHashes = map[string]string{}
	}
	p.BundleHubHashes[agent] = bundleMCPHash(entry)
}

func (p *Project) bundleMCPRecords(rel, name string, visit func(*projectBundleAssignment)) {
	for i := range p.BundleAssignments {
		a := &p.BundleAssignments[i]
		if a.Kind == "mcp" && a.Name == name {
			if path, _ := p.mcpRel(a.Agent); path == rel {
				visit(a)
			}
		}
	}
}

func (p *Project) preserveBundleMCP(part projectBundlePart, hash string) error {
	rel, format := p.mcpRel(part.Agent)
	if !slices.Contains(p.Wrote[rel], part.Name) || slices.ContainsFunc(p.Servers[part.Name], func(agent string) bool {
		other, _ := p.mcpRel(agent)
		return other == rel
	}) {
		return nil
	}
	stage, err := p.file(filepath.Join(p.Dir, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	entries, err := (&mcpFile{Path: stage, Format: format}).entries()
	if err != nil {
		return err
	}
	if hash == "" || bundleMCPHash(entries[part.Name]) != hash {
		p.Wrote[rel] = set(p.Wrote[rel], part.Name, false)
		delete(p.Startup[rel], part.Name)
		if entries[part.Name] != nil {
			p.writes.skipped = append(p.writes.skipped, Problem{Agent: part.Agent, What: "mcp:" + part.Name, Error: "project entry changed outside magpie"})
		}
	}
	return nil
}
