package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/yetone/magpie/internal/library"
)

const libraryProjectUsage = `magpie library project [--json]      the projects (Library › Projects) and the skills each has
  magpie library project --plan <mutation…>    preview the same project command without saving it
  magpie library project --plan-id=<id> <mutation…>   apply the command after checking its preview
  magpie library project plan <dir> [--json]      preview the project's pending writes
  magpie library project apply <dir> [--plan-id=<id>]   write the project's saved assignments
  magpie library project distribute <dir> skills|subagents|instructions on|off [--plan]
                                     choose which category goes to related Git worktrees
  magpie library project add <dir>         add a folder whose agents can be given library skills
  magpie library project rm <dir> [--keep-files]   take it off; --keep-files leaves what magpie put there, yours from then on
  magpie library project skills <dir> [--json]     its skills, for which agents, and where each is placed
  magpie library project skill add <dir> <skill…> [agents=a,b…]   give it skills: by default for the agents its skills
                                     are for already, or, with none yet, every agent here that reads a project's skills
  magpie library project skill rm <dir> <skill…>   take skills out of it
  magpie library project skill agents <dir> <skill> <a,b…|none>   the agents it has the skill for
  magpie library project copy <dir> on|off copies of its skills in place of links to the library's
  magpie library project instructions <dir> [--json]   its own instructions, the agents they're for and the files
  magpie library project instructions <dir> text <text…>|--file <f>|-   set them ("" takes them out); written, between
                                     magpie's two marker lines, into the project file each agent reads: by default for
                                     the agents the shared instructions are on for, else every agent here that reads one
  magpie library project instructions <dir> agents <a,b…|none>   the agents whose project file they're written into
  magpie library project hook add <dir> <hook…> [agents=a,b…]   give it hooks: by default for the agents its
                                     hooks are for already, or, with none yet, every agent here that reads a
                                     project's hooks (.claude/settings.local.json, .codex/hooks.json)
  magpie library project hook rm <dir> <hook…>   take hooks out of it
  magpie library project hook agents <dir> <hook> <a,b…|none>   the agents it has the hook for
  magpie library project hooks <dir> [--json]   its hooks, for which agents, and the entries magpie wrote
  magpie library project subagents <dir> [--json]   its subagents, for which agents, and the files magpie wrote
  magpie library project subagents add <dir> <name…> [agents=a,b…]   give it subagents: by default for the agents
                                     its subagents are for already, or, with none yet, every agent here that reads a
                                     project's subagents (.claude/agents, .codex/agents…)
  magpie library project subagents rm <dir> <name…>   take subagents out of it
  magpie library project subagents agents <dir> <name> <a,b…|none>   the agents it has the subagent for
  magpie library project server <dir> [--json]  its MCP servers, and which agents have each
  magpie library project server add <dir> <name> [agents=a,b…]   give it a server the library keeps:
                                     written into the file that agent reads a project's servers from
                                     (.mcp.json, .codex/config.toml, .pi/mcp.json…)
  magpie library project server rm <dir> <name…>   take servers out of it
  magpie library project server agents <dir> <name> <a,b…|none>   the agents it has the server for
  magpie library project plugin <dir> [--json]  the Claude Code plugins it turns on or off itself
  magpie library project plugin on|off <dir> <id>   turn one on or off in the project's own
                                     .claude/settings.local.json (the plugin is installed by Claude Code)
  magpie library project pi <dir> [--json]      its Pi packages and extensions, and what magpie wrote there
  magpie library project pi add <dir> package|extension <source…>   give the project Pi packages or
                                     extensions of its own, written into its own .pi/settings.json
  magpie library project pi off <dir> package|extension <source…>   stop loading them, KEEPING the entry (Pi's "-<source>")
  magpie library project pi on <dir> package|extension <source…>    load them again (same entry, no re-entry needed)
  magpie library project pi rm <dir> package|extension <source…>    take them out of the project for real
  magpie library project pi scope <dir> package|extension <source> anywhere|project
  magpie library project pi adopt <dir> package|extension <source> [--scope=project|anywhere]
                                     where a Pi entry belongs: anywhere is the project's set, loaded in each worktree of
                                     its repository too; project keeps it to the project's own folder, and with every
                                     entry so scoped magpie keeps the .pi/settings.json it made out of git
                                     <dir> may be relative, or "." for the folder you're in. A skill that couldn't be
                                     placed (a folder there that isn't magpie's is left as it is) is named, and the
                                     command exits with status 1.
`

// libraryProjectCmd is magpie library project …: the window's Library ›
// Projects page from a terminal (#5193), through the same library calls
// its page makes.
func libraryProjectCmd(args []string) error {
	usage := func() error { return fmt.Errorf("usage:\n  %s", libraryProjectUsage) }
	var options library.ProjectWriteOptions
	if len(args) != 0 {
		if args[0] == "--plan" {
			options.Preview = true
			args = args[1:]
		} else if id, ok := strings.CutPrefix(args[0], "--plan-id="); ok {
			if id == "" {
				return usage()
			}
			options.PlanID = id
			args = args[1:]
		}
	}
	if len(args) == 0 && (options.Preview || options.PlanID != "") {
		return usage()
	}
	if len(args) == 0 || len(args) == 1 && (args[0] == "list" || args[0] == "--json") || len(args) == 2 && args[0] == "list" && args[1] == "--json" {
		return listProjects("", slices.Contains(args, "--json"))
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "help", "-h", "--help":
		fmt.Print("  " + libraryProjectUsage)
		return nil
	case "plan", "apply", "distribute":
		return projectWritesCmd(sub, rest, usage, options)
	case "add":
		if len(rest) != 1 || strings.HasPrefix(rest[0], "-") {
			return usage()
		}
		dir, err := library.ProjectPath(rest[0])
		if err != nil {
			return err
		}
		res, err := library.AddProject(dir, options)
		if err != nil || options.Preview {
			return projectCommandResult(res, err, options.Preview)
		}
		fmt.Println(green.Render("✓"), dir, "is a project")
		fmt.Println(muted.Render("  magpie library project skill add " + quoteArg(dir) + " <skill…> gives it skills"))
		return nil
	case "rm", "remove":
		keep := false
		var dirs []string
		for _, a := range rest {
			switch {
			case a == "--keep-files":
				keep = true
			case strings.HasPrefix(a, "-"):
				return usage()
			default:
				dirs = append(dirs, a)
			}
		}
		if len(dirs) != 1 {
			return usage()
		}
		dir, err := projectDir(dirs[0])
		if err != nil {
			return err
		}
		res, err := library.RemoveProject(dir, keep, options)
		if err != nil || options.Preview {
			return projectCommandResult(res, err, options.Preview)
		}
		// what couldn't be taken out keeps the project listed
		if p, _ := library.ProjectAt(dir); p != "" {
			return fmt.Errorf("%s stays a project: magpie couldn't take everything it placed out of it (magpie library project skills %s)", dir, quoteArg(dir))
		}
		if keep {
			fmt.Println(green.Render("✓"), dir, "is no project now; what magpie put there is left as it is")
		} else {
			fmt.Println(green.Render("✓"), dir, "is no project now; what magpie put there is taken out")
		}
		return nil
	case "skills":
		asJSON := slices.Contains(rest, "--json")
		rest = slices.DeleteFunc(slices.Clone(rest), func(a string) bool { return a == "--json" })
		if len(rest) != 1 || strings.HasPrefix(rest[0], "-") {
			return usage()
		}
		return listProjects(rest[0], asJSON)
	case "hooks":
		asJSON := slices.Contains(rest, "--json")
		rest = slices.DeleteFunc(slices.Clone(rest), func(a string) bool { return a == "--json" })
		if len(rest) != 1 || strings.HasPrefix(rest[0], "-") {
			return usage()
		}
		if asJSON {
			return listProjects(rest[0], true)
		}
		return listProjectHooks(rest[0])
	case "hook":
		return libraryProjectHookCmd(rest, usage, options)
	case "subagents":
		return libraryProjectSubagentCmd(rest, usage, options)
	case "server", "servers":
		return libraryProjectServerCmd(rest, usage, options)
	case "pi":
		return libraryProjectPiCmd(rest, usage, options)
	case "plugin", "plugins":
		return libraryProjectPluginCmd(rest, usage, options)
	case "copy":
		if len(rest) != 2 || strings.HasPrefix(rest[0], "-") || rest[1] != "on" && rest[1] != "off" {
			return usage()
		}
		dir, err := projectDir(rest[0])
		if err != nil {
			return err
		}
		res, err := library.ProjectCopy(dir, rest[1] == "on", options)
		if err != nil || options.Preview {
			return projectCommandResult(res, err, options.Preview)
		}
		if err := projectOutcome(dir, nil, res); err != nil {
			return err
		}
		if rest[1] == "on" {
			fmt.Println(green.Render("✓"), dir, "gets copies of its skills, refreshed on project or full sync")
		} else {
			fmt.Println(green.Render("✓"), dir, "gets links to the library's skills")
		}
		return nil
	case "skill":
		if len(rest) == 0 {
			return usage()
		}
		return projectSkillCmd(rest[0], rest[1:], usage, options)
	case "instructions", "instr":
		return projectInstrCmd(rest, usage, options)
	}
	return fmt.Errorf("no library project command %q\n  %s", args[0], libraryProjectUsage)
}

func projectWritesCmd(action string, args []string, usage func() error, options library.ProjectWriteOptions) error {
	if len(args) == 0 {
		return usage()
	}
	dir, err := projectDir(args[0])
	if err != nil {
		return err
	}
	args = args[1:]
	var res *library.Result
	switch action {
	case "plan":
		if len(args) > 1 || len(args) == 1 && args[0] != "--json" {
			return usage()
		}
		res, err = library.ProjectPlan(dir)
		if err == nil && len(args) == 1 {
			return json.NewEncoder(os.Stdout).Encode(res)
		}
	case "apply":
		if len(args) == 1 {
			id, ok := strings.CutPrefix(args[0], "--plan-id=")
			if !ok || id == "" {
				return usage()
			}
			options.PlanID = id
		} else if len(args) > 1 {
			return usage()
		}
		res, err = library.ProjectApply(dir, options)
	case "distribute":
		if len(args) < 2 || len(args) > 3 || args[1] != "on" && args[1] != "off" || len(args) == 3 && args[2] != "--plan" {
			return usage()
		}
		options.Preview = options.Preview || len(args) == 3
		res, err = library.ProjectDistributeCategory(dir, args[0], args[1] == "on", options)
	}
	return projectCommandResult(res, err, options.Preview || action == "plan")
}

func projectCommandResult(res *library.Result, err error, preview bool) error {
	if err != nil {
		return err
	}
	for _, w := range res.Writes {
		fmt.Printf("%s %s\n", w.Action, w.Path)
		for _, f := range w.Files {
			fmt.Printf("  %s\n", f)
		}
	}
	for _, problem := range res.Problems {
		fmt.Fprintf(os.Stderr, "%s %s: %s\n", problem.Agent, problem.What, problem.Error)
	}
	if preview {
		fmt.Printf("Plan ID %s\n", res.PlanID)
	}
	if len(res.Problems) > 0 {
		return fmt.Errorf("%d project paths or resources could not be reconciled", len(res.Problems))
	}
	return nil
}

func projectSkillCmd(verb string, rest []string, usage func() error, options library.ProjectWriteOptions) error {
	if len(rest) < 2 || strings.HasPrefix(rest[0], "-") {
		return usage()
	}
	dir, err := projectDir(rest[0])
	if err != nil {
		return err
	}
	v, err := library.Inspect(nil)
	if err != nil {
		return err
	}
	p := projectView(v, dir)
	if p == nil {
		return fmt.Errorf("%s isn't a project", dir)
	}
	switch verb {
	case "add":
		var names, agents []string
		given := false
		for _, a := range rest[1:] {
			if s, ok := strings.CutPrefix(a, "agents="); ok {
				if agents, err = projectAgents(s); err != nil {
					return err
				}
				given = true
				continue
			}
			if strings.HasPrefix(a, "-") {
				return usage()
			}
			names = addOnce(names, a)
		}
		if len(names) == 0 {
			return usage()
		}
		if !given {
			agents = defaultProjectAgents(v, p)
		}
		if len(agents) == 0 {
			return fmt.Errorf("no agent here reads a project's skills: name them with agents=a,b…")
		}
		res, err := library.ProjectSkills(dir, names, agents, options)
		if err != nil || options.Preview {
			return projectCommandResult(res, err, options.Preview)
		}
		return projectOutcome(dir, names, res)
	case "rm", "remove":
		var names []string
		for _, a := range rest[1:] {
			if strings.HasPrefix(a, "-") {
				return usage()
			}
			if _, ok := p.Skills[a]; !ok {
				return fmt.Errorf("%s has no skill %s", dir, a)
			}
			names = addOnce(names, a)
		}
		res, err := library.ProjectSkills(dir, names, nil, options)
		if err != nil || options.Preview {
			return projectCommandResult(res, err, options.Preview)
		}
		if err := projectOutcome(dir, nil, res); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), strings.Join(names, ", "), "taken out of", dir)
		return nil
	case "agents":
		if len(rest) != 3 || strings.HasPrefix(rest[1], "-") || strings.HasPrefix(rest[2], "-") {
			return usage()
		}
		agents, err := projectAgents(rest[2])
		if err != nil {
			return err
		}
		res, err := library.ProjectSkill(dir, rest[1], agents, options)
		if err != nil || options.Preview {
			return projectCommandResult(res, err, options.Preview)
		}
		if len(agents) == 0 {
			if err := projectOutcome(dir, nil, res); err != nil {
				return err
			}
			fmt.Println(green.Render("✓"), rest[1], "taken out of", dir)
			return nil
		}
		return projectOutcome(dir, []string{rest[1]}, res)
	}
	return fmt.Errorf("no library project skill command %q\n  %s", verb, libraryProjectUsage)
}

// addOnce adds s to a list once, in the order given.
func addOnce(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// projectDir is the listed folder of the project typed, or why there's none.
func projectDir(typed string) (string, error) {
	dir, err := library.ProjectAt(typed)
	if err != nil {
		return "", err
	}
	if dir == "" {
		abs, _ := library.ProjectPath(typed)
		return "", fmt.Errorf("%s isn't a project: magpie library project add %s adds it", abs, quoteArg(abs))
	}
	return dir, nil
}

// projectAgents is a comma list of agents (none for no agent), each by its
// id, refused when magpie knows of no folder it reads a project's skills
// from.
func projectAgents(s string) ([]string, error) {
	ids, err := libraryAgents(s, "skills")
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if library.ProjectSkillsDir(id) == "" {
			return nil, fmt.Errorf("magpie knows of no folder %s reads a project's skills from", id)
		}
	}
	return ids, nil
}

// defaultProjectAgents is who a skill added with no agents= is for, as on
// the page's Skills menu: the agents the project's skills are for already,
// or, in a project with none yet, every agent here that reads a project's
// skills.
func defaultProjectAgents(v *library.View, p *library.ProjectView) []string {
	var all []string
	for _, a := range v.Agents {
		if a.ProjectSkills != "" && !a.Gone && !a.Unseen {
			all = append(all, a.ID)
		}
	}
	var used []string
	for _, ids := range p.Skills {
		for _, id := range ids {
			if slices.Contains(all, id) && !slices.Contains(used, id) {
				used = append(used, id)
			}
		}
	}
	if len(used) > 0 {
		sort.Strings(used)
		return used
	}
	var out []string
	for _, a := range v.Agents {
		if slices.Contains(all, a.ID) && a.Asleep == "" {
			out = append(out, a.ID)
		}
	}
	return out
}

func projectView(v *library.View, dir string) *library.ProjectView {
	for i := range v.Projects {
		if v.Projects[i].Dir == dir {
			return &v.Projects[i]
		}
	}
	return nil
}

// projectOutcome says, for each skill named, where it was placed or why
// it wasn't, and the project's own problem; one not placed is an error,
// for a script to tell "installed" from "left as it is" by the status.
func projectOutcome(dir string, names []string, res *library.Result) error {
	v, err := library.Read(res.Problems)
	if err != nil {
		return err
	}
	p := projectView(v, dir)
	if p == nil {
		return fmt.Errorf("%s isn't a project", dir)
	}
	if why := p.Problems[""]; why != "" {
		fmt.Println(amber.Render("!"), dir+":", why)
	}
	for _, problem := range res.Skipped {
		if problem.Agent == dir && strings.HasPrefix(problem.What, "project:") {
			fmt.Println(muted.Render("  " + problem.Error + " · skipped"))
		}
	}
	var unplaced []string
	for _, n := range names {
		placed, why := skillPlaces(p, n)
		if len(placed) == 0 && p.Problems[n] == "" && projectSkipped(res, dir, n) {
			continue
		}
		if why != "" {
			if p.Problems[n] == "" && projectSkipped(res, dir, n) {
				continue
			}
			unplaced = append(unplaced, n)
			fmt.Println(amber.Render("!"), n, muted.Render("not placed:"), why)
			continue
		}
		fmt.Println(green.Render("✓"), n, "→", strings.Join(p.Skills[n], ", "), muted.Render(strings.Join(placed, " ")))
	}
	if len(unplaced) > 0 {
		return fmt.Errorf("%s not placed in %s", strings.Join(unplaced, ", "), dir)
	}
	return nil
}

func projectSkipped(res *library.Result, dir, what string) bool {
	return slices.ContainsFunc(res.Skipped, func(row library.Problem) bool {
		return row.Agent == dir && row.What == "project:"+what
	})
}

// skillPlaces are the entries of a project's skill magpie placed, and,
// for one its agents should have and don't, why.
func skillPlaces(p *library.ProjectView, name string) (placed []string, why string) {
	want := map[string]bool{}
	for _, id := range p.Skills[name] {
		if d := library.ProjectSkillsDir(id); d != "" {
			want[d+"/"+name] = true
		}
	}
	for e := range want {
		if slices.Contains(p.Placed, e) {
			placed = append(placed, e)
		}
	}
	sort.Strings(placed)
	if why = p.Problems[name]; why == "" && len(placed) < len(want) {
		why = "not in the project (magpie library sync says why)"
	}
	return placed, why
}

// listProjects prints the projects, or one, with their skills.
func listProjects(dir string, asJSON bool) error {
	var ps []library.ProjectView
	if dir != "" {
		p, err := library.ReadProject(dir)
		if err != nil {
			return err
		}
		ps = []library.ProjectView{*p}
	} else {
		v, err := library.Inspect(nil)
		if err != nil {
			return err
		}
		ps = v.Projects
	}
	if asJSON {
		e := json.NewEncoder(os.Stdout)
		e.SetIndent("", "  ")
		if dir != "" {
			return e.Encode(ps[0])
		}
		return e.Encode(ps)
	}
	if len(ps) == 0 {
		fmt.Println(muted.Render("No projects yet · magpie library project add <dir>"))
		return nil
	}
	for _, p := range ps {
		head := bold.Render(p.Name) + " " + muted.Render(p.Dir)
		if p.Missing {
			head += " " + amber.Render("(the folder is gone)")
		} else if p.Copy {
			head += " " + muted.Render("(copies)")
		}
		fmt.Println(head)
		printProjectNative(&p, "skills")
		for _, rel := range slices.Sorted(maps.Keys(p.SkillLinks)) {
			fmt.Println(" ", rel, muted.Render("(MagPie link to project skill)"))
		}
		names := make([]string, 0, len(p.Skills))
		for n := range p.Skills {
			names = append(names, n)
		}
		sort.Strings(names)
		if len(names) == 0 && len(p.Native["skills"]) == 0 {
			fmt.Println(" ", muted.Render("no skills yet · magpie library project skill add "+quoteArg(p.Dir)+" <skill…>"))
		}
		for _, n := range names {
			placed, why := skillPlaces(&p, n)
			if why != "" {
				fmt.Printf("  %s %s → %s %s\n", amber.Render("!"), bold.Render(n), strings.Join(p.Skills[n], ", "), muted.Render(why))
				continue
			}
			fmt.Printf("  %s → %s %s\n", bold.Render(n), strings.Join(p.Skills[n], ", "), muted.Render(strings.Join(placed, " ")))
		}
	}
	return nil
}

func printProjectNative(p *library.ProjectView, kind string) {
	for _, entry := range p.Native[kind] {
		detail := strings.TrimSpace(strings.Join([]string{entry.Agent, entry.Event, entry.Path}, " "))
		fmt.Printf("  %s  %s %s\n", entry.Name, muted.Render(detail), muted.Render("(project-provided)"))
	}
}

// quoteArg is a path as a shell takes it back.
func quoteArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\!*?[]{}()&;|<>#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// projectInstrCmd is magpie library project instructions …: the project's
// own instructions, as the Instructions page's project card sets them.
func projectInstrCmd(rest []string, usage func() error, options library.ProjectWriteOptions) error {
	asJSON := slices.Contains(rest, "--json")
	rest = slices.DeleteFunc(slices.Clone(rest), func(a string) bool { return a == "--json" })
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
		return usage()
	}
	if len(rest) == 1 {
		return showProjectInstr(rest[0], asJSON)
	}
	dir, err := projectDir(rest[0])
	if err != nil {
		return err
	}
	var c library.ProjectInstr
	switch rest[1] {
	case "text":
		var text string
		switch args := rest[2:]; {
		case len(args) == 1 && args[0] == "-":
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			text = string(b)
		case len(args) == 2 && args[0] == "--file":
			b, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			text = string(b)
		case len(args) > 0 && !strings.HasPrefix(args[0], "-"):
			text = strings.Join(args, " ")
		default:
			return usage()
		}
		c.Text = &text
		// the first text a project gets goes to the agents the shared
		// instructions are on for, as the page's card does it
		v, err := library.Inspect(nil)
		if err != nil {
			return err
		}
		if p := projectView(v, dir); p != nil && len(p.InstrAgents) == 0 && strings.TrimSpace(text) != "" {
			ids := defaultInstrAgents(v)
			if len(ids) == 0 {
				return fmt.Errorf("no agent here reads a project's instructions file magpie knows of")
			}
			c.Agents = &ids
		}
	case "agents":
		if len(rest) != 3 {
			return usage()
		}
		ids, err := projectInstrAgents(rest[2])
		if err != nil {
			return err
		}
		c.Agents = &ids
	default:
		return usage()
	}
	res, err := library.ProjectInstructions(dir, c, options)
	if err != nil || options.Preview {
		return projectCommandResult(res, err, options.Preview)
	}
	v, err := library.Read(res.Problems)
	if err != nil {
		return err
	}
	p := projectView(v, dir)
	if p == nil {
		return fmt.Errorf("%s isn't a project", dir)
	}
	if why := p.Problems["instructions"]; why != "" {
		return fmt.Errorf("%s: %s", dir, why)
	}
	if p.Instr == "" || len(p.InstrAgents) == 0 {
		fmt.Println(green.Render("✓"), dir, "has no instructions of its own in its files now")
		return nil
	}
	fmt.Println(green.Render("✓"), dir, "→", strings.Join(p.InstrAgents, ", "), muted.Render(strings.Join(p.InstrFiles, " ")))
	return nil
}

func showProjectInstr(dir string, asJSON bool) error {
	p, err := library.ReadProject(dir)
	if err != nil {
		return err
	}
	if asJSON {
		e := json.NewEncoder(os.Stdout)
		e.SetIndent("", "  ")
		return e.Encode(map[string]any{"dir": p.Dir, "text": p.Instr, "agents": p.InstrAgents, "files": p.InstrFiles, "native": p.Native["instructions"]})
	}
	printProjectNative(p, "instructions")
	if p.Instr == "" {
		if len(p.Native["instructions"]) == 0 {
			fmt.Println(muted.Render("No instructions of its own · magpie library project instructions " + quoteArg(dir) + " text <text…>"))
		}
		return nil
	}
	if len(p.InstrAgents) == 0 {
		fmt.Println(muted.Render("for no agent · magpie library project instructions " + quoteArg(dir) + " agents <a,b…>"))
	} else {
		fmt.Println(bold.Render(strings.Join(p.InstrAgents, ", ")), muted.Render(strings.Join(p.InstrFiles, " ")))
	}
	if why := p.Problems["instructions"]; why != "" {
		fmt.Println(amber.Render("!"), why)
	}
	fmt.Println(p.Instr)
	return nil
}

// projectInstrAgents is a comma list of agents (none for no agent), each
// by its id or name, refused when magpie knows of no file it reads a
// project's instructions from.
func projectInstrAgents(s string) ([]string, error) {
	out := []string{}
	for _, a := range agentList(s) {
		id := strings.ToLower(a)
		if library.ProjectInstrFile(id) == "" {
			if t, err := library.Takes(a, "skills"); err == nil {
				id = t
			}
		}
		if library.ProjectInstrFile(id) == "" {
			return nil, fmt.Errorf("magpie knows of no file %s reads a project's instructions from", a)
		}
		out = addOnce(out, id)
	}
	return out, nil
}

// defaultInstrAgents is who a project's first instructions are for: the
// agents here that read a project's instructions and have the shared ones
// on, else every one of them here.
func defaultInstrAgents(v *library.View) []string {
	var all, on []string
	for _, a := range v.Agents {
		if a.ProjectInstructions != "" && !a.Gone && !a.Unseen && a.Asleep == "" {
			all = append(all, a.ID)
		}
	}
	if v.Instructions != nil {
		for _, a := range v.Instructions.Agents {
			if a.On && slices.Contains(all, a.Agent) {
				on = append(on, a.Agent)
			}
		}
	}
	if len(on) > 0 {
		return on
	}
	return all
}
