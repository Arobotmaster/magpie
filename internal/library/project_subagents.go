package library

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/edit"
)

// A project can be given library subagents (jinpwng on X), each for some
// agents, as files of the project's own: the folder each agent reads a
// project's subagents from, in the shape its user-wide ones have
// (subagents.go). Only an agent whose docs name a project folder is
// listed:
//
//	Claude Code   .claude/agents/<name>.md        code.claude.com/docs/en/sub-agents
//	Codex         .codex/agents/<name>.toml       "standalone TOML files under .codex/agents/ for project-scoped agents" (Codex docs, subagents)
//	OpenCode      .opencode/agents/<name>.md      opencode.ai/docs/agents
//	Gemini CLI    .gemini/agents/<name>.md        "Project-level: .gemini/agents/*.md" (Gemini CLI docs, subagents)
//	Qwen Code     .qwen/agents/<name>.md          Qwen Code docs, sub-agents
//	Factory Droid .factory/droids/<name>.md       docs.factory.com/cli/configuration/custom-droids
//	Cursor        .cursor/agents/<name>.md        cursor.com/docs/subagents
//	Copilot CLI   .github/agents/<name>.agent.md  "Repository-level custom agents are stored in the .github/agents directory"
//
// A file is recorded by its hash (Project.AgentFiles), as the user-wide
// ones are: magpie writes over or takes away only one still exactly as it
// wrote it, and a file of the user's by the same name is never touched.
// Nothing is written outside the project's folder, so the agents' own
// (global) subagents stay as they are.
var projectSubagentDirs = map[string]string{
	"claude":   ".claude/agents",
	"codex":    ".codex/agents",
	"opencode": ".opencode/agents",
	"gemini":   ".gemini/agents",
	"qwen":     ".qwen/agents",
	"droid":    ".factory/droids",
	"cursor":   ".cursor/agents",
	"copilot":  ".github/agents",
}

// ProjectSubagentsDir is the folder, in a project, an agent reads its
// subagents from: "" for one magpie can't give a project's subagents to.
func ProjectSubagentsDir(agentID string) string { return projectSubagentDirs[agentID] }

// projectSubagentSpec is the shape of agent id's subagent files, kept in
// the project's folder in place of the agent's own.
func projectSubagentSpec(p *Project, id string) *subagentSpec {
	sp, _ := subagentSpecOf(&Target{Agent: &agent.Agent{ID: id}})
	if sp == nil {
		return nil
	}
	cp := *sp
	cp.dir = filepath.Join(p.Dir, filepath.FromSlash(projectSubagentDirs[id]))
	return &cp
}

func (p *Project) rel(abs string) string {
	r, _ := filepath.Rel(p.Dir, abs)
	return filepath.ToSlash(r)
}

// syncProjectSubagents writes the library's subagents the project has
// into each agent's folder there, and takes away those it wrote that it
// no longer has.
func (l *Library) syncProjectSubagents(p *Project, fail func(what string, err error)) {
	type want struct {
		s  *Subagent
		sp *subagentSpec
		id string
	}
	wanted := map[string]want{} // file (project-relative) → what goes there
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for n := range p.Subagents {
			if !yield(n) {
				return
			}
		}
	}) {
		s := l.subagent(name)
		if s == nil {
			continue
		}
		for _, id := range p.Subagents[name] {
			if sp := projectSubagentSpec(p, id); sp != nil {
				wanted[p.rel(sp.path(s.Name))] = want{s, sp, id}
			}
		}
	}
	var kept []SubagentFile
	done := map[string]bool{}
	for _, f := range p.AgentFiles {
		abs := filepath.Join(p.Dir, filepath.FromSlash(f.Path))
		dst, err := p.file(abs)
		if err != nil {
			fail("subagent:"+f.Name, err)
			kept = append(kept, f)
			done[f.Path] = true
			continue
		}
		cur, err := os.ReadFile(dst)
		switch {
		case os.IsNotExist(err):
			continue // gone: written again below if it is still wanted
		case err != nil:
			fail("subagent:"+f.Name, err)
			kept = append(kept, f)
			done[f.Path] = true
			continue
		case subagentHash(cur) != f.Hash:
			fail("subagent:"+f.Name, fmt.Errorf("%s changed outside magpie", f.Path))
			done[f.Path] = true
			continue
		}
		w, ok := wanted[f.Path]
		if !ok {
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				fail("subagent:"+f.Name, err)
				kept = append(kept, f)
			}
			continue
		}
		done[f.Path] = true
		body := l.renderSubagent(w.sp, w.s)
		if !bytes.Equal(cur, body) {
			if err := edit.WriteAtomic(dst, body); err != nil {
				fail("subagent:"+f.Name, err)
				kept = append(kept, f)
				continue
			}
		}
		kept = append(kept, SubagentFile{Name: w.s.Name, Path: f.Path, Hash: subagentHash(body)})
	}
	for _, rel := range slices.Sorted(func(yield func(string) bool) {
		for r := range wanted {
			if !yield(r) {
				return
			}
		}
	}) {
		if done[rel] {
			continue
		}
		w := wanted[rel]
		abs := filepath.Join(p.Dir, filepath.FromSlash(rel))
		dst, err := p.file(abs)
		if err != nil {
			fail("subagent:"+w.s.Name, err)
			continue
		}
		_, err = os.Lstat(dst)
		if slices.ContainsFunc(p.nativeSubagents(w.id), func(r ProjectResource) bool {
			return strings.EqualFold(r.Name, w.s.Name) || r.Path == rel
		}) {
			fail("subagent:"+w.s.Name, fmt.Errorf("%w %s (%s)", errProjectAlreadyHas, w.s.Name, w.id))
			continue
		}
		if err == nil {
			fail("subagent:"+w.s.Name, fmt.Errorf("%s is in the project already and isn't magpie's: it's left as it is", rel))
			continue
		}
		if !os.IsNotExist(err) {
			fail("subagent:"+w.s.Name, err)
			continue
		}
		if err := p.mkdirs(filepath.Dir(abs)); err != nil {
			fail("subagent:"+w.s.Name, err)
			continue
		}
		body := l.renderSubagent(w.sp, w.s)
		if err := edit.WriteAtomic(dst, body); err != nil {
			fail("subagent:"+w.s.Name, err)
			continue
		}
		kept = append(kept, SubagentFile{Name: w.s.Name, Path: rel, Hash: subagentHash(body)})
	}
	slices.SortFunc(kept, func(x, y SubagentFile) int { return strings.Compare(x.Path, y.Path) })
	p.AgentFiles = kept
}

// ProjectSubagents sets which agents get each of some library subagents
// in a project; none takes them out of it.
func ProjectSubagents(dir string, names, agents []string, options ...ProjectWriteOptions) (*Result, error) {
	var skipped []Problem
	res, err := changeProject(dir, projectRoles, func(l *Library) error {
		p := l.project(dir)
		if p == nil {
			return fmt.Errorf("no project %s", dir)
		}
		if len(names) == 0 {
			return errors.New("no subagents given")
		}
		for _, n := range names {
			if l.subagent(n) == nil {
				return fmt.Errorf("no subagent called %s", n)
			}
		}
		var ids []string
		for _, id := range agents {
			if ProjectSubagentsDir(id) == "" {
				return fmt.Errorf("magpie knows of no folder %s reads a project's subagents from", id)
			}
			ids = set(ids, id, true)
		}
		for _, n := range names {
			keep := slices.DeleteFunc(slices.Clone(ids), func(id string) bool {
				sp := projectSubagentSpec(p, id)
				if sp == nil {
					return false
				}
				if slices.ContainsFunc(p.nativeSubagents(id), func(r ProjectResource) bool {
					return strings.EqualFold(r.Name, n) || r.Path == p.rel(sp.path(n))
				}) {
					skipped = append(skipped, projectSkip(p, "subagent:"+n, n, id))
					return true
				}
				return false
			})
			if len(keep) == 0 {
				delete(p.Subagents, n)
				continue
			}
			if p.Subagents == nil {
				p.Subagents = map[string][]string{}
			}
			p.Subagents[n] = keep
		}
		return nil
	}, options...)
	return withProjectSkips(res, skipped), err
}

// renameProjectSubagents has the projects give a subagent its new name.
func (l *Library) renameProjectSubagents(old, name string) {
	for _, p := range l.Projects {
		if a, ok := p.Subagents[old]; ok {
			delete(p.Subagents, old)
			p.Subagents[name] = a
		}
	}
}
