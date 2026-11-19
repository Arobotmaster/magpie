package cli

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/yetone/magpie/internal/library"
)

// libraryProjectHookCmd is magpie library project hook …: the project's own
// hooks as the page's Projects card gives and takes them (#6874).
func libraryProjectHookCmd(rest []string, usage func() error, options library.ProjectWriteOptions) error {
	if len(rest) == 0 {
		return usage()
	}
	verb, args := rest[0], rest[1:]
	switch verb {
	case "help", "-h", "--help":
		fmt.Print("  " + libraryProjectUsage)
		return nil
	case "add", "rm", "remove", "agents":
	default:
		return fmt.Errorf("no library project hook command %q\n  %s", verb, libraryProjectUsage)
	}
	if len(args) < 2 || strings.HasPrefix(args[0], "-") {
		return usage()
	}
	dir, err := projectDir(args[0])
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
	if verb == "agents" {
		if len(args) != 3 || strings.HasPrefix(args[2], "-") {
			return usage()
		}
		name := args[1]
		if _, ok := p.Hooks[name]; !ok {
			return fmt.Errorf("%s has no hook %s", dir, name)
		}
		agents, err := hookProjectAgents(args[2])
		if err != nil {
			return err
		}
		res, err := library.ProjectHooks(dir, []string{name}, agents, options)
		if err != nil || options.Preview {
			return projectCommandResult(res, err, options.Preview)
		}
		if err := projectOutcome(dir, nil, res); err != nil {
			return err
		}
		if len(agents) == 0 {
			fmt.Println(green.Render("✓"), name, "taken out of", dir)
		} else if !projectSkipped(res, dir, "hook:"+name) || len(agents) > 1 {
			fmt.Println(green.Render("✓"), name, "→", strings.Join(agents, ", "))
		}
		return nil
	}
	var names, agents []string
	given := false
	for _, a := range args[1:] {
		if s, ok := strings.CutPrefix(a, "agents="); ok {
			if agents, err = hookProjectAgents(s); err != nil {
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
	if verb == "add" && !given {
		agents = defaultProjectHookAgents(v, p)
	}
	if verb == "add" && len(agents) == 0 {
		return fmt.Errorf("no agent here reads a project's hooks: name them with agents=a,b…")
	}
	if verb != "add" {
		for _, n := range names {
			if _, ok := p.Hooks[n]; !ok {
				return fmt.Errorf("%s has no hook %s", dir, n)
			}
		}
		res, err := library.ProjectHooks(dir, names, nil, options)
		if err != nil || options.Preview {
			return projectCommandResult(res, err, options.Preview)
		}
		if err := projectOutcome(dir, nil, res); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), strings.Join(names, ", "), "taken out of", dir)
		return nil
	}
	res, err := library.ProjectHooks(dir, names, agents, options)
	if err != nil || options.Preview {
		return projectCommandResult(res, err, options.Preview)
	}
	if err := projectOutcome(dir, nil, res); err != nil {
		return err
	}
	// p was read before the write above, so its agent list and entries are the
	// ones from before this command. Confirming from it told the user "route →"
	// on a first grant and the previous agent list on a second, which reads as
	// a failure right after a write that worked (#WSL 8.2). Re-read the view the
	// way projectOutcome does, so the line describes what is there now.
	v, err = library.Read(res.Problems)
	if err != nil {
		return err
	}
	p = projectView(v, dir)
	if p == nil {
		return fmt.Errorf("%s isn't a project", dir)
	}
	for _, n := range names {
		if projectSkipped(res, dir, "hook:"+n) && len(hookPlaces(p, n)) == 0 {
			continue
		}
		fmt.Println(green.Render("✓"), n, "→", strings.Join(p.Hooks[n], ", "), muted.Render(strings.Join(hookPlaces(p, n), " ")))
	}
	return nil
}

// hookProjectAgents is a comma list of agents each of which reads a
// project's hooks.
func hookProjectAgents(s string) ([]string, error) {
	ids, err := libraryAgents(s, "hooks")
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if library.ProjectHooksFile(id) == "" {
			return nil, fmt.Errorf("magpie knows of no folder %s reads a project's hooks from", id)
		}
	}
	return ids, nil
}

// defaultProjectHookAgents is who a hook added with no agents= is for: the
// agents the project's hooks are for already, or, in a project with none
// yet, every agent here that reads a project's hooks.
func defaultProjectHookAgents(v *library.View, p *library.ProjectView) []string {
	var all []string
	for _, id := range library.ProjectHookAgents() {
		for _, a := range v.Agents {
			if a.ID == id && !a.Gone && !a.Unseen {
				all = append(all, id)
			}
		}
	}
	var used []string
	for _, ids := range p.Hooks {
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
	sort.Strings(all)
	return all
}

// hookPlaces are the project's files magpie wrote an entry of this hook in.
func hookPlaces(p *library.ProjectView, name string) []string {
	var out []string
	for _, f := range p.HookFiles {
		if f.Name != name {
			continue
		}
		out = append(out, f.Path+":"+f.Event)
	}
	sort.Strings(out)
	return out
}

// listProjectHooks prints one project's hooks, for which agents, and the
// entries magpie wrote for each.
func listProjectHooks(dir string) error {
	p, err := library.ReadProject(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(p.Hooks))
	for n := range p.Hooks {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Println(bold.Render(p.Name) + " " + muted.Render(p.Dir) + " " + muted.Render("hooks"))
	printProjectNative(p, "hooks")
	if len(names) == 0 && len(p.Native["hooks"]) == 0 {
		fmt.Println(" ", muted.Render("no hooks yet · magpie library project hook add "+quoteArg(p.Dir)+" <hook…>"))
		return nil
	}
	for _, n := range names {
		places := hookPlaces(p, n)
		line := fmt.Sprintf("  %s → %s", bold.Render(n), strings.Join(p.Hooks[n], ", "))
		if why := p.Problems["hook:"+n]; why != "" {
			fmt.Printf("%s %s\n", amber.Render("!"), bold.Render(line))
			fmt.Println("   ", muted.Render(why))
			continue
		}
		fmt.Printf("%s %s\n", line, muted.Render(strings.Join(places, " ")))
	}
	return nil
}
