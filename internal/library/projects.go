package library

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/yetone/magpie/internal/edit"
)

// Project is a folder of the user's whose agents get some of the library's
// skills there, as the project's own: each is placed in the folder the
// agent reads a project's skills from, and kept out of git.
type Project struct {
	Dir string `json:"dir"`
	// Distribution is opt-in for each category in this project's worktrees.
	Distribution ProjectDistribution `json:"distribution,omitzero"`
	writes       *projectWrites
	removeAfter  bool
	// Copy puts copies of the skills there, not links to the library's
	// (for Windows, or a folder synced or shared where a link won't do);
	// an unchanged copy is refreshed during project or full sync
	Copy bool `json:"copy,omitempty"`
	// Skills are the library's skills the project has, and for which agents
	Skills map[string][]string `json:"skills,omitempty"`
	// Placed are the entries magpie put in the folder (.claude/skills/pdf),
	// the only ones it ever takes away
	Placed []string `json:"placed,omitempty"`
	// SkillLinks records the links magpie made for Claude to read the
	// project's own shared skills, by path and link target.
	SkillLinks map[string]string `json:"skillLinks,omitempty"`
	// Made are the folders magpie made to place them in, taken away again
	// once they're empty; Gitignore is whether it made the .gitignore
	Made      []string `json:"made,omitempty"`
	Gitignore bool     `json:"gitignore,omitempty"`
	// Servers are the library's MCP servers the project has, and for which
	// agents (project_mcp.go)
	Servers map[string][]string `json:"servers,omitempty"`
	// Wrote are, by the project's file (.mcp.json), the servers magpie
	// wrote in it, the only entries it ever takes out
	Wrote map[string][]string `json:"wrote,omitempty"`
	// Startup are, by the project's file, the startup timeout magpie
	// wrote in each server's entry there (Codex's startup_timeout_sec):
	// one other than this is the user's
	Startup map[string]map[string]float64 `json:"startup,omitempty"`
	// MadeFiles are the files magpie made to write servers in, listed in
	// the .gitignore and taken away again once nothing's left in them
	MadeFiles []string `json:"madeFiles,omitempty"`
	// ServersOff and SkillsOff are global servers and skills (given to
	// the agents themselves) the project turns off, and for which agents
	// (project_scope.go)
	ServersOff map[string][]string `json:"serversOff,omitempty"`
	SkillsOff  map[string][]string `json:"skillsOff,omitempty"`
	// Claude is what magpie wrote in Claude Code's own record of the
	// project (~/.claude.json): the router's entry, servers turned off
	Claude *claudeLocal `json:"claude,omitempty"`
	// CodexOff are the servers magpie turned off in the project's
	// .codex/config.toml, Overrides the skills it turned off in
	// .claude/settings.local.json
	CodexOff  []string `json:"codexOff,omitempty"`
	Overrides []string `json:"overrides,omitempty"`
	// Trees are the project's folder in each other worktree of its git
	// repository, given the project's set, with what magpie put in each
	Trees []*Project `json:"trees,omitempty"`
	// Subagents are the library's subagents the project has, and for which
	// agents; AgentFiles the files magpie wrote for them, by their hash
	// (project_subagents.go)
	Subagents  map[string][]string `json:"subagents,omitempty"`
	AgentFiles []SubagentFile      `json:"agentFiles,omitempty"`
	// Instr is the project's own instructions, InstrAgents the agents
	// whose project file they're written into, InstrFiles the files
	// magpie wrote them in (project_instructions.go)
	Instr       string      `json:"instr,omitempty"`
	InstrAgents []string    `json:"instrAgents,omitempty"`
	InstrFiles  []InstrFile `json:"instrFiles,omitempty"`
	// PiOff are the Pi sources the project keeps but has turned off, written
	// as Pi's own "-<source>" entries so turning one back on needs no re-entry
	PiOff []string `json:"piOff,omitempty"`
	// Hooks are the library's hooks the project has, and for which agents;
	// HookFiles the entries magpie wrote for them, by their canonical form
	// (project_hooks.go, #6874)
	Hooks     map[string][]string `json:"hooks,omitempty"`
	HookFiles []HookFile          `json:"hookFiles,omitempty"`
	// Pi are the Pi packages and extensions the project has, by kind
	// ("package", "extension"); PiEntries are the ones magpie wrote into
	// its own .pi/settings.json, by source and kind (project_pi.go)
	Pi        map[string][]string `json:"pi,omitempty"`
	PiEntries []PiEntry           `json:"piEntries,omitempty"`
	// PiMade is whether magpie made the project's .pi/settings.json itself.
	// Only a file magpie made may be kept out of git for its scoped entries
	// (project_role.go): one the user already had is left to their own
	// .gitignore as every project file is.
	PiMade bool `json:"piMade,omitempty"`
	// ClaudePlugins are the Claude Code plugins the project turns on or off
	// in its own settings.local.json; ClaudeWritten is the value magpie
	// last wrote for each, so only that is ever taken away again
	// (project_claude_plugins.go)
	ClaudePlugins map[string]bool `json:"claudePlugins,omitempty"`
	ClaudeWritten map[string]bool `json:"claudeWritten,omitempty"`
	ClaudeMade    bool            `json:"claudeMade,omitempty"`
}

// HookFile is one entry magpie wrote for a project's hook: the hook it is
// of, the project's file and event it is under, and the entry's canonical
// form, which is what it is compared by when it is taken away again.
type HookFile struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Event string `json:"event"`
	Hash  string `json:"hash"`
}

// projectSkillsDirs is where, in a project, each agent reads skills of its
// own from — only an agent that does: Claude Code its .claude/skills, and
// those that read the shared .agents/skills (Codex from where it runs to
// the repository's root, Gemini CLI, OpenCode, Pi, Cursor, Copilot, Antigravity,
// Kimi Code, Goose and Grok Build, Droid — docs.factory.com/cli/configuration/
// skills — and Cline's CLI, which reads .clinerules, .cline and .agents
// skills folders in the workspace, @cline/shared 3.0.70) that one, so that
// one folder serves them all. Qoder (and Qoder CN) read .qoder/skills
// (getProjectSkillsDir, @qoder-ai/qodercli 1.1.69, whose .agents/skills
// a setting can turn off), CodeBuddy Code .codebuddy/skills
// (getProjectSkillsDir, @tencent-ai/codebuddy-code 2.164.0) and Command
// Code .commandcode/skills (commandcode.ai/docs/skills). DeepSeek Harness
// reads <projectRoot>/.dsh/skills and <projectRoot>/.agents/skills, the
// project root being the nearest folder up from where it runs with a .git
// in it, else that folder itself (the provider's roots and findProjectRoot,
// @deepseek-ai/dsh-skill-filesystem 0.2.1-rc.5, mounted by dsh-base's
// cordis.patch.yml in every profile; the same since 0.0.1-rc.3), so it is
// given the shared one. A project folder inside a repository but not at
// its root isn't read by dsh: run there, it looks in the repository's
// root (#5172). MiMo Code, Crush,
// ZCode and oh-my-pi say of no project folder magpie can rely on.
var projectSkillsDirs = map[string]string{
	"claude":      ".claude/skills",
	"codex":       ".agents/skills",
	"gemini":      ".agents/skills",
	"opencode":    ".agents/skills",
	"pi":          ".agents/skills",
	"cursor":      ".agents/skills",
	"copilot":     ".agents/skills",
	"agy":         ".agents/skills",
	"antigravity": ".agents/skills",
	"kimi":        ".agents/skills",
	"goose":       ".agents/skills",
	"grok":        ".agents/skills",
	"droid":       ".agents/skills",
	"cline":       ".agents/skills",
	"dsh":         ".agents/skills",
	"ds-bot":      ".agents/skills",
	"qoder":       ".qoder/skills",
	"qoder-cn":    ".qoder/skills",
	"codebuddy":   ".codebuddy/skills",
	"commandcode": ".commandcode/skills",
}

// ProjectSkillsDir is the folder, in a project, an agent reads its skills
// from: "" for one magpie can't give a project's skills to.
func ProjectSkillsDir(agent string) string { return projectSkillsDirs[agent] }

func (l *Library) project(dir string) *Project {
	for _, p := range l.Projects {
		if p.Dir == dir {
			return p
		}
	}
	return nil
}

// The .gitignore lines magpie keeps between these two, and only there.
const (
	ignoreBegin = "# >>> magpie: library skills placed here (magpie adds and removes these lines) >>>"
	ignoreEnd   = "# <<< magpie <<<"
)

// syncProject reconciles only the selected project's resources.
func (l *Library) syncProject(p *Project, res *Result, parts projectParts) {
	if fi, err := os.Stat(p.Dir); err != nil || !fi.IsDir() {
		res.fail(p.Dir, "project:", fmt.Errorf("the folder is gone or inaccessible"))
		return
	}
	var hubs map[string]*Server
	if parts&projectServers != 0 {
		hubs = l.projectHubs(p)
	}
	if parts&projectSkills != 0 {
		p.syncSkillLinks(!p.removeAfter, res)
	}
	l.syncFolder(p, res, hubs, parts)
	if parts&projectServers != 0 {
		l.syncClaudeLocal(p, hubs["claude"], func(what string, err error) {
			res.fail(p.Dir, "project:"+what, err)
		})
	}
	l.syncTrees(p, res, parts)
	p.planTidy()
	if err := p.keepOutOfGit(); err != nil {
		res.fail(p.Dir, "project:", fmt.Errorf("keeping it out of git: %w", err))
	}
}

func (l *Library) syncFolder(p *Project, res *Result, hubs map[string]*Server, parts projectParts) {
	fail := func(name string, err error) {
		if errors.Is(err, errProjectAlreadyHas) {
			res.Skipped = append(res.Skipped, Problem{Agent: p.Dir, What: "project:" + name, Error: err.Error()})
		} else {
			res.fail(p.Dir, "project:"+name, err)
		}
	}
	if parts&projectSkills != 0 {
		l.syncProjectSkills(p, fail)
		l.syncSkillsOff(p, fail)
	}
	if parts&projectServers != 0 {
		l.syncProjectMCP(p, hubs, fail)
	}
	if parts&projectRoles != 0 {
		l.syncProjectSubagents(p, fail)
	}
	if parts&projectInstructions != 0 {
		l.syncProjectInstr(p, fail)
	}
	if parts&projectHooks != 0 {
		l.syncProjectHooks(p, fail)
	}
	if parts&projectPi != 0 {
		l.syncProjectPi(p, fail)
	}
	if parts&projectPlugins != 0 {
		l.syncProjectClaudePlugins(p, fail)
	}
}

func (l *Library) syncProjectSkills(p *Project, fail func(string, error)) {
	want := map[string]string{} // entry → the skill
	// the entries given as copies: all of them in a project that takes
	// copies, else those an agent reads that skips a junction
	// (skipsJunctions), which is magpie's link on Windows without the
	// right to make a symlink
	copies := map[string]bool{}
	for name, agents := range p.Skills {
		if l.skill(name) == nil {
			continue
		}
		for _, id := range agents {
			if d := ProjectSkillsDir(id); d != "" {
				e := d + "/" + name
				want[e] = name
				copies[e] = copies[e] || p.Copy || skipsJunctions(id)
			}
		}
	}
	var placed []string
	for _, e := range p.Placed {
		if _, ok := want[e]; ok {
			continue
		}
		name, abs := filepath.Base(filepath.FromSlash(e)), filepath.Join(p.Dir, filepath.FromSlash(e))
		if !ours(abs, name) {
			continue // gone, or the user's own by now: forgotten, left as it is
		}
		if err := p.planSkill(abs, name, false, true); err != nil {
			fail(name, err)
			placed = append(placed, e)
		}
	}
	entries := slices.Sorted(func(yield func(string) bool) {
		for e := range want {
			if !yield(e) {
				return
			}
		}
	})
	native := map[string][]ProjectResource{}
	for _, e := range entries {
		dir := filepath.ToSlash(filepath.Dir(e))
		if _, ok := native[dir]; !ok {
			native[dir] = p.nativeSkills(dir)
		}
	}
	for _, e := range entries {
		name := want[e]
		dir := filepath.ToSlash(filepath.Dir(e))
		if p.SkillLinks[e] != "" || nativeSkill(native[dir], name) {
			fail(name, fmt.Errorf("%w %s (%s)", errProjectAlreadyHas, name, dir))
			continue
		}
		switch err := p.planSkill(filepath.Join(p.Dir, filepath.FromSlash(e)), name, copies[e], false); {
		case err == nil:
			placed = append(placed, e)
		case errors.Is(err, errForeign):
			fail(name, fmt.Errorf("%s is in the project already and isn't magpie's: it's left as it is", e))
		default:
			fail(name, err)
			if slices.Contains(p.Placed, e) {
				placed = append(placed, e)
			}
		}
	}
	sort.Strings(placed)
	p.Placed = placed
}

// keepOutOfGit keeps what magpie placed in the project, and in its folder
// in each other worktree, out of git: in the repository's info/exclude,
// never in a file of the project's (project_git.go). The lines an older
// magpie wrote in the project's .gitignore come out of it, and a
// .gitignore it made goes; a folder outside git gets nothing.
func (p *Project) keepOutOfGit() error {
	if err := p.writeIgnore(); err != nil {
		return fmt.Errorf(".gitignore: %w", err)
	}
	g := p.writes.repository(realDir(p.Dir))
	if g.err != nil {
		return g.err
	}
	if g.where.Top == "" {
		return nil
	}
	entries := p.ignored()
	for _, t := range p.Trees {
		entries = append(entries, t.ignored()...)
	}
	return p.writeExclude(g.where, entries)
}

var errForeign = errors.New("not magpie's")

// inside is an error when abs, links followed, isn't in the project's
// folder. A project is often a repository someone else wrote, and a link
// in it (CLAUDE.md to ~/.zshrc, .claude to a folder anyone can read)
// would have magpie write the user's instructions, servers and keys
// wherever the link's author chose. A link to nothing is refused too:
// where a write through it lands can't be told before it is made.
func (p *Project) inside(abs string) error {
	root, err := filepath.EvalSymlinks(p.Dir)
	if err != nil {
		return err
	}
	q, rest := filepath.Clean(abs), ""
	for {
		if _, err := os.Lstat(q); err == nil {
			break
		}
		up := filepath.Dir(q)
		if up == q {
			break
		}
		rest = filepath.Join(filepath.Base(q), rest)
		q = up
	}
	real, err := filepath.EvalSymlinks(q)
	if err != nil {
		return fmt.Errorf("%s is a link to nothing: magpie leaves it as it is", p.rel(abs))
	}
	if real = filepath.Join(real, rest); !within(real, root) {
		return fmt.Errorf("%s leads out of the project, to %s: magpie leaves it as it is", p.rel(abs), real)
	}
	return nil
}

// mkdirs makes dir and the folders above it the project hasn't, noting
// which magpie made.
func (p *Project) mkdirs(dir string) error {
	var made []string
	for d := dir; d != p.Dir && strings.HasPrefix(d, p.Dir+string(filepath.Separator)); d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil {
			break
		}
		made = append(made, d)
	}
	if p.writes == nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	for _, d := range made {
		rel, _ := filepath.Rel(p.Dir, d)
		if rel = filepath.ToSlash(rel); !slices.Contains(p.Made, rel) {
			p.Made = append(p.Made, rel)
		}
	}
	return nil
}

// writeIgnore takes the lines an older magpie kept in the project's
// .gitignore, between its two lines, out of it; the rest of the file is
// the user's and stays. A .gitignore magpie made is taken away once
// nothing's left in it.
func (p *Project) writeIgnore() error {
	f := filepath.Join(p.Dir, ".gitignore")
	if _, err := os.Lstat(f); err == nil {
		if err := p.inside(f); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(f); errors.Is(err, os.ErrNotExist) {
		p.Gitignore = false
		return nil
	}
	f, err := p.file(f)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(f)
	if os.IsNotExist(err) {
		p.Gitignore = false
		return nil
	}
	if err != nil {
		return err
	}
	nl := "\n"
	if strings.Contains(string(b), "\r\n") {
		nl = "\r\n"
	}
	if !strings.Contains(strings.ReplaceAll(string(b), "\r\n", "\n"), ignoreBegin+"\n") {
		return nil
	}
	out := strings.ReplaceAll(stripIgnore(strings.ReplaceAll(string(b), "\r\n", "\n")), "\n", nl)
	if out == string(b) {
		return nil
	}
	if strings.TrimSpace(out) == "" && p.Gitignore {
		p.Gitignore = false
		return os.Remove(f)
	}
	return edit.WriteAtomic(f, []byte(out))
}

// stripIgnore is a .gitignore without magpie's lines, and the blank line
// left before them.
func stripIgnore(s string) string { return stripBlock(s, ignoreBegin) }

// ---- changing -------------------------------------------------------------

// AddProject adds a folder whose agents can be given library skills.
func AddProject(dir string, options ...ProjectWriteOptions) (*Result, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("choose a project's folder")
	}
	dir = filepath.Clean(expand(dir))
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("%s isn't a folder's full path", dir)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s isn't a folder", dir)
	}
	// Keep the registered root stable when the selection was through a link.
	if fi, err := os.Lstat(dir); err == nil && linkEntry(fi) {
		dir = realDir(dir)
	}
	if err := notGlobal(dir); err != nil {
		return nil, err
	}
	return changeProject(dir, 0, func(l *Library) error {
		for _, p := range l.Projects {
			if realDir(p.Dir) == realDir(dir) {
				return fmt.Errorf("%s is a project already", p.Dir)
			}
		}
		l.Projects = append(l.Projects, &Project{Dir: dir})
		return nil
	}, options...)
}

// notGlobal refuses a folder whose skills folders are an agent's own for
// every project: the home folder, where .claude/skills is Claude Code's.
func notGlobal(dir string) error {
	if realDir(dir) == realDir(home()) {
		return fmt.Errorf("the home folder's skills are every project's: give skills to the agents themselves instead")
	}
	for _, t := range everyTarget() {
		if t.Skills == "" {
			continue
		}
		for _, d := range projectSkillsDirs {
			if realDir(filepath.Join(dir, filepath.FromSlash(d))) == realDir(t.Skills) {
				return fmt.Errorf("%s in there is %s's own skills folder", d, t.Agent.Name)
			}
		}
	}
	for _, t := range everyTarget() {
		if t.MCP == nil {
			continue
		}
		for _, f := range projectMCPFiles {
			if realDir(filepath.Join(dir, filepath.FromSlash(f.rel))) == realDir(t.MCP.Path) {
				return fmt.Errorf("%s in there is %s's own MCP file", f.rel, t.Agent.Name)
			}
		}
	}
	return nil
}

// ProjectPath is a folder as typed on a command line (~, or relative to
// where magpie runs) made a full path, as AddProject takes it.
func ProjectPath(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", fmt.Errorf("choose a project's folder")
	}
	return filepath.Abs(expand(dir))
}

// ProjectAt is the listed folder of the project dir is (ProjectPath's
// way, or the same folder through a link): "" for a folder that isn't one.
func ProjectAt(dir string) (string, error) {
	abs, err := ProjectPath(dir)
	if err != nil {
		return "", err
	}
	mu.Lock()
	defer mu.Unlock()
	l, err := load()
	if err != nil {
		return "", err
	}
	if p := l.project(abs); p != nil {
		return p.Dir, nil
	}
	for _, p := range l.Projects {
		if realDir(p.Dir) == realDir(abs) {
			return p.Dir, nil
		}
	}
	return "", nil
}

// RemoveProject takes every skill magpie placed out of a project, and the
// project off the list; what's left, it couldn't take away and says why.
// keep takes the project off the list alone (#514: a project given a few
// skills or servers once, to keep them): everything magpie put in the
// folder — skills, servers in the agents' files, its lines in the
// .gitignore — stays as it is, the user's from then on.
func RemoveProject(dir string, keep bool, options ...ProjectWriteOptions) (*Result, error) {
	return changeProject(dir, projectAll, func(l *Library) error {
		p := l.project(dir)
		if p == nil {
			return fmt.Errorf("no project %s", dir)
		}
		if keep {
			l.Projects = slices.DeleteFunc(l.Projects, func(x *Project) bool { return x == p })
			return nil
		}
		// a folder that's gone has nothing of magpie's on disk to take
		// back: the records of what it placed and wrote go with the
		// entry, as a skill's do when its folder is gone already (#3510).
		// A folder that couldn't be looked at (no permission, a drive
		// that answers with an error) isn't known to be gone: its records
		// stay, and the removal goes as before.
		if fi, err := os.Stat(p.Dir); errors.Is(err, os.ErrNotExist) || err == nil && !fi.IsDir() {
			// Claude Code's record of it isn't in the folder
			p.ServersOff = nil
			l.Projects = slices.DeleteFunc(l.Projects, func(x *Project) bool { return x == p })
			return nil
		}
		// PiEntries stays for the sync below: it is what tells sync which
		// entries in the project's own .pi/settings.json are magpie's to
		// take away. Sync clears the records it acted on.
		p.Skills, p.Servers, p.ServersOff, p.SkillsOff, p.Subagents, p.Hooks, p.Pi, p.ClaudePlugins, p.InstrAgents = nil, nil, nil, nil, nil, nil, nil, nil, nil
		p.PiOff = nil
		p.removeAfter = true
		return nil
	}, options...)
}

// ProjectSkill sets which agents get a library skill in a project; none
// takes it out of the project.
func ProjectSkill(dir, name string, agents []string, options ...ProjectWriteOptions) (*Result, error) {
	return ProjectSkills(dir, []string{name}, agents, options...)
}

// ProjectSkills sets which agents get each of some library skills in a
// project, in one write: the skills a project's card picks from its menu
// (POM_JUMPING on X). The project's other skills stay as they are; none
// takes these out of it.
func ProjectSkills(dir string, names, agents []string, options ...ProjectWriteOptions) (*Result, error) {
	var skipped []Problem
	res, err := changeProject(dir, projectSkills, func(l *Library) error {
		p := l.project(dir)
		if p == nil {
			return fmt.Errorf("no project %s", dir)
		}
		if len(names) == 0 {
			return fmt.Errorf("no skills given")
		}
		for _, name := range names {
			if l.skill(name) == nil {
				return fmt.Errorf("no skill called %s", name)
			}
		}
		var ids []string
		native := map[string][]ProjectResource{}
		for _, id := range agents {
			dir := ProjectSkillsDir(id)
			if dir == "" {
				return fmt.Errorf("magpie knows of no folder %s reads a project's skills from", id)
			}
			if _, ok := native[dir]; !ok {
				native[dir] = p.nativeSkills(dir)
			}
			ids = set(ids, id, true)
		}
		if _, claude := native[".claude/skills"]; claude {
			if _, shared := native[".agents/skills"]; !shared {
				native[".agents/skills"] = p.nativeSkills(".agents/skills")
			}
		}
		for _, name := range names {
			keep := slices.DeleteFunc(slices.Clone(ids), func(id string) bool {
				if path := ProjectSkillsDir(id); p.nativeSkillAt(native, path, name) {
					skipped = append(skipped, projectSkip(p, name, name, path))
					return true
				}
				return false
			})
			if len(keep) == 0 {
				delete(p.Skills, name)
				continue
			}
			if p.Skills == nil {
				p.Skills = map[string][]string{}
			}
			p.Skills[name] = keep
		}
		return nil
	}, options...)
	return withProjectSkips(res, skipped), err
}

// ProjectCopy sets whether a project gets copies of its skills rather than
// links; those there are made again the other way.
func ProjectCopy(dir string, copy bool, options ...ProjectWriteOptions) (*Result, error) {
	return changeProject(dir, projectSkills, func(l *Library) error {
		p := l.project(dir)
		if p == nil {
			return fmt.Errorf("no project %s", dir)
		}
		p.Copy = copy
		return nil
	}, options...)
}

// ProjectView is a project as the page shows it.
type ProjectView struct {
	Native       map[string][]ProjectResource `json:"native,omitempty"`
	SkillLinks   map[string]string            `json:"skillLinks,omitempty"`
	Distribution ProjectDistribution          `json:"distribution"`
	Dir          string                       `json:"dir"`
	Name         string                       `json:"name"`
	Copy         bool                         `json:"copy"`
	Skills       map[string][]string          `json:"skills"`
	Placed       []string                     `json:"placed"`
	Servers      map[string][]string          `json:"servers"`
	// Scoped are the servers of the project's set its own folder alone gets:
	// the ones a copy in another worktree of the repository doesn't
	// (project_role.go)
	Scoped []string `json:"scoped"`
	// Wrote are, by the project's file, the servers magpie wrote there
	Wrote map[string][]string `json:"wrote"`
	// ServersOff and SkillsOff are the Library's servers and skills
	// turned off in this project, each for the agents named
	ServersOff map[string][]string `json:"serversOff"`
	SkillsOff  map[string][]string `json:"skillsOff"`
	// Trees are the project's folder in the repository's other git
	// worktrees, which follow it
	Trees []string `json:"trees,omitempty"`
	// Subagents are the library's subagents the project has, for the
	// agents named
	Subagents map[string][]string `json:"subagents"`
	// AgentFiles are the project's files magpie wrote for them
	AgentFiles []string `json:"agentFiles"`
	// Instr is the project's own instructions, InstrAgents the agents
	// they're for, InstrFiles the project's files magpie wrote them in
	Instr       string   `json:"instr"`
	InstrAgents []string `json:"instrAgents"`
	InstrFiles  []string `json:"instrFiles"`
	// Hooks are the library's hooks the project has, for the agents named,
	// and HookFiles the project's entries magpie wrote for them (#6874)
	Hooks     map[string][]string `json:"hooks"`
	HookFiles []HookFile          `json:"hookFiles"`
	// Pi are the Pi packages and extensions the project has, and PiEntries
	// the ones magpie wrote into its own .pi/settings.json
	Pi        map[string][]string `json:"pi"`
	PiEntries []PiEntry           `json:"piEntries"`
	// ClaudePlugins are the Claude Code plugins the project turns on or off
	ClaudePlugins map[string]bool `json:"claudePlugins"`
	// PiScoped are, by kind, the Pi entries the project's own folder alone
	// has: a worktree of its repository doesn't load them (project_role.go).
	// PiKeptOutOfGit is whether magpie keeps the project's .pi/settings.json
	// out of git for them.
	PiScoped       map[string][]string `json:"piScoped,omitempty"`
	PiKeptOutOfGit bool                `json:"piKeptOutOfGit,omitempty"`
	Missing        bool                `json:"missing,omitempty"` // the folder is gone
	// Problems are, by skill ("" for the project's own), what couldn't be done
	Problems map[string]string `json:"problems,omitempty"`
}

func projectViews(l *Library, problems []Problem) []ProjectView {
	out := []ProjectView{}
	for _, p := range l.Projects {
		v := ProjectView{Dir: p.Dir, Name: filepath.Base(p.Dir), Copy: p.Copy, Distribution: p.Distribution, Skills: map[string][]string{}, Placed: append([]string{}, p.Placed...)}
		v.Native = p.nativeResources()
		v.SkillLinks = maps.Clone(p.SkillLinks)
		for k, a := range p.Skills {
			v.Skills[k] = append([]string{}, a...)
		}
		v.Servers, v.Wrote = map[string][]string{}, map[string][]string{}
		for k, a := range p.Servers {
			v.Servers[k] = append([]string{}, a...)
		}
		// the ones its copies don't get, for the page to say so
		v.Scoped = l.scopedServers(p)
		for k, a := range p.Wrote {
			v.Wrote[k] = append([]string{}, a...)
		}
		v.ServersOff, v.SkillsOff = map[string][]string{}, map[string][]string{}
		for k, a := range p.ServersOff {
			v.ServersOff[k] = append([]string{}, a...)
		}
		for k, a := range p.SkillsOff {
			v.SkillsOff[k] = append([]string{}, a...)
		}
		v.AgentFiles = []string{}
		for _, f := range p.AgentFiles {
			v.AgentFiles = append(v.AgentFiles, f.Path)
		}
		v.Instr, v.InstrAgents, v.InstrFiles = p.Instr, append([]string{}, p.InstrAgents...), []string{}
		for _, f := range p.InstrFiles {
			v.InstrFiles = append(v.InstrFiles, f.Path)
		}
		v.Subagents = map[string][]string{}
		for k, a := range p.Subagents {
			v.Subagents[k] = append([]string{}, a...)
		}
		v.Hooks = map[string][]string{}
		for k, a := range p.Hooks {
			v.Hooks[k] = append([]string{}, a...)
		}
		v.HookFiles = []HookFile{}
		v.HookFiles = append(v.HookFiles, p.HookFiles...)
		v.PiEntries = []PiEntry{}
		v.PiEntries = append(v.PiEntries, p.PiEntries...)
		for i := range v.PiEntries {
			e := &v.PiEntries[i]
			if e.Raw != "" {
				e.Off = !piEntryOn(e.Kind, []byte(e.Raw))
			}
		}
		v.Pi = map[string][]string{}
		for k, a := range p.Pi {
			v.Pi[k] = append([]string{}, a...)
		}
		v.PiScoped = map[string][]string{}
		for _, kind := range []string{"package", "extension"} {
			if scoped := p.piScopedSources(kind); len(scoped) > 0 {
				v.PiScoped[kind] = scoped
			}
		}
		if len(v.PiScoped) == 0 {
			v.PiScoped = nil
		}
		v.PiKeptOutOfGit = slices.Contains(p.MadeFiles, piSettingsRel)
		v.ClaudePlugins = map[string]bool{}
		for k, b := range p.ClaudePlugins {
			v.ClaudePlugins[k] = b
		}
		for _, t := range p.Trees {
			v.Trees = append(v.Trees, t.Dir)
		}
		if fi, err := os.Stat(p.Dir); err != nil || !fi.IsDir() {
			v.Missing = true
		}
		for _, x := range problems {
			if x.Agent == p.Dir {
				if v.Problems == nil {
					v.Problems = map[string]string{}
				}
				v.Problems[strings.TrimPrefix(x.What, "project:")] = x.Error
			}
		}
		out = append(out, v)
	}
	return out
}
