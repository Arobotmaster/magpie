package library

import (
	"fmt"
	"slices"
)

// ScopeProject writes a server only to the selected project directory.
// A project's set combines Library assignments with project entries
// and per-project overrides. Each related Git worktree gets that set.
// Other worktrees omit it from their files and use their own router
// address for the remaining servers (project_copy.go).
//
// The user selects this scope for a directory. The worktree's name,
// location and main-worktree status do not determine its scope.
const ScopeProject = "project"

// scoped is whether s is the project's own rather than given wherever the
// project is checked out again.
func (s *Server) scoped() bool { return s != nil && s.Scope == ScopeProject }

// scopeNamed is a scope as the page and the command line name it: "" for
// the project's set.
func scopeNamed(scope string) (string, error) {
	switch scope {
	case "", "anywhere":
		return "", nil
	case ScopeProject:
		return ScopeProject, nil
	}
	return "", fmt.Errorf("scope %q: use anywhere (the project's set, given in each of its worktrees) or project (the project's own folder alone)", scope)
}

// ServerScope sets where a server given to a project belongs: anywhere, or
// the project's own folder alone. A server given to no project keeps the
// setting for when a project is given it.
//
// A server given to the agents themselves can't be scoped: it is written in
// each agent's own config, which reads for every folder on the machine, so
// a project scope would promise less than it kept. Taking it from the
// agents first is the user's call.
func ServerScope(name, scope string) (*Result, error) {
	want, err := scopeNamed(scope)
	if err != nil {
		return nil, err
	}
	return change(func(l *Library) error {
		s := l.server(name)
		if s == nil {
			return fmt.Errorf("no server called %s", name)
		}
		if want == ScopeProject {
			if s.Plugin != "" {
				return fmt.Errorf("%s belongs to a plugin. Its tools go to every project that enables the plugin", name)
			}
			if len(s.Agents) > 0 {
				return fmt.Errorf("%s is given to %v in their configuration for every folder. To assign it to a project folder, take it from them first (magpie library mcp agents %s none)", name, s.Agents, name)
			}
		}
		s.Scope = want
		return nil
	})
}

// copiesServers are the servers of a project's set that its copies get: the
// same map with the ones the project's own folder alone has left out, nil
// when nothing is left.
func (l *Library) copiesServers(named map[string][]string) map[string][]string {
	if len(named) == 0 {
		return named
	}
	out := map[string][]string{}
	for name, agents := range named {
		if l.server(name).scoped() {
			continue
		}
		out[name] = agents
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// scopedServers are the servers of a project's set that its own folder
// alone is given, sorted: the ones the page says a copy doesn't get.
func (l *Library) scopedServers(p *Project) []string {
	var out []string
	for name, agents := range p.Servers {
		if len(agents) > 0 && l.server(name).scoped() {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// ---- a project's Pi entries ------------------------------------------------

// piSettingsRel is Pi's file in a project (project_pi.go).
const piSettingsRel = ".pi/settings.json"

// piAllScoped is whether every entry magpie wrote into the project's Pi
// settings is the project's own folder alone: the file then belongs to that
// folder and is kept out of git, so git can't carry it into a worktree where
// an ordinary copy would load it. With any entry of the project's set the
// file stays the project's own configuration, left to its .gitignore as
// .mcp.json and the hooks files are, because a copy is meant to load that
// entry.
func piAllScoped(entries []PiEntry) bool {
	if len(entries) == 0 {
		return false
	}
	return !slices.ContainsFunc(entries, func(e PiEntry) bool { return e.Scope != ScopeProject })
}

// piScopeOf is the scope magpie recorded for one entry of the project's Pi
// settings, "" for one of the project's set.
func (p *Project) piScopeOf(kind, source string) string {
	for _, e := range p.PiEntries {
		if e.Kind == kind && e.Source == source {
			return e.Scope
		}
	}
	return ""
}

// ProjectPiScope sets where a Pi package or extension of a project belongs:
// anywhere, or the project's own folder alone. With every entry scoped that
// way, the project's .pi/settings.json is kept out of git by magpie when
// magpie made it, so a worktree of the repository doesn't load it.
//
// A file the user already had is left alone: git's info/exclude keeps a file
// from being added, it does not take back one already committed, and
// magpie never writes the user's .gitignore. So a repository that has
// already committed that file keeps giving it to its copies, and magpie
// cannot stop that from here — the scope then describes the intent, not an
// achieved boundary. It is reported rather than passed off as done.
func ProjectPiScope(dir, kind, source, scope string, options ...ProjectWriteOptions) (*Result, error) {
	want, err := scopeNamed(scope)
	if err != nil {
		return nil, err
	}
	if kind != "package" && kind != "extension" {
		return nil, fmt.Errorf("no Pi kind %q: package or extension", kind)
	}
	return changeProject(dir, projectPi, func(l *Library) error {
		p := l.project(dir)
		if p == nil {
			return fmt.Errorf("no project %s", dir)
		}
		if !slices.Contains(p.Pi[kind], source) {
			return fmt.Errorf("%s has no Pi %s %s", dir, kind, source)
		}
		found := false
		for i := range p.PiEntries {
			if p.PiEntries[i].Kind == kind && p.PiEntries[i].Source == source {
				p.PiEntries[i].Scope = want
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s has no Pi %s %s that magpie wrote yet: sync first (magpie library sync)", dir, kind, source)
		}
		return nil
	}, options...)
}

// piScopedSources are the sources of a kind that the project's own folder
// alone has, for the page to say a copy doesn't load them.
func (p *Project) piScopedSources(kind string) []string {
	var out []string
	for _, e := range p.PiEntries {
		if e.Kind == kind && e.Scope == ScopeProject {
			out = append(out, e.Source)
		}
	}
	slices.Sort(out)
	return out
}

// keepPiOutOfGit keeps a project's .pi/settings.json out of git while every
// entry magpie wrote there is the project's own folder alone, and only for a
// file magpie made (Project.PiMade). git's info/exclude is what keeps a file
// magpie makes from being added, so without this a commit would carry the
// scoped entries into every worktree of the repository, where Pi given the
// project's trust loads them for an ordinary copy: measured, not inferred —
// a committed .pi/settings.json reaches a worktree and `pi --approve` there
// reports the package as scope=project, origin=package.
//
// An entry of the project's set puts the file back to being the project's own
// configuration, left to its .gitignore as .mcp.json and the hooks files are.
//
// A file the user already had is never claimed: taking a committed file out of
// git is the user's change to make, and magpie does not write their
// .gitignore. When every entry is scoped there anyway, the intent can't be
// reached from here, so it is said rather than passed off as done.
func (p *Project) keepPiOutOfGit(kept []PiEntry, fail func(what string, err error)) {
	if !piAllScoped(kept) {
		if slices.Contains(p.MadeFiles, piSettingsRel) {
			// scoped entries were taken out, or one of the set came back
			p.dropMadeFile(piSettingsRel)
		}
		return
	}
	if p.PiMade {
		if !slices.Contains(p.MadeFiles, piSettingsRel) {
			p.MadeFiles = append(p.MadeFiles, piSettingsRel)
		}
		return
	}
	fail("pi", fmt.Errorf("%s was the project's own file before magpie wrote it, so magpie can't take it out of git: a repository that has committed it still gives it to its worktrees. Add it to the project's .gitignore, or run `git rm --cached %s`, for entries scoped to this folder to stay in this folder", piSettingsRel, piSettingsRel))
}
