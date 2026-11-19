package cli

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/yetone/magpie/internal/library"
)

// libraryProjectSubagentCmd is magpie library project subagents …: a project's
// own subagents as the page's Projects card gives and takes them, through the
// same library calls. The page had this and the CLI did not, so a script had
// to drive the GUI to give a project a role.
func libraryProjectSubagentCmd(rest []string, usage func() error, options library.ProjectWriteOptions) error {
	if len(rest) == 0 {
		return usage()
	}
	verb, args := rest[0], rest[1:]
	// With no verb, `… project subagents <dir>` lists it, as `… project hooks
	// <dir>` does: the folder alone is the query, not a command name.
	if !slices.Contains([]string{"add", "rm", "remove", "agents", "list", "help", "-h", "--help"}, verb) {
		verb, args = "list", rest
	}
	switch verb {
	case "help", "-h", "--help":
		fmt.Print("  " + libraryProjectUsage)
		return nil
	case "add", "rm", "remove", "agents", "list":
	default:
		return fmt.Errorf("no library project subagents command %q\n  %s", verb, libraryProjectUsage)
	}
	// `list` takes the folder alone (and --json) and no names; `add`/`rm` need
	// at least a name after it; `agents` needs the name and the agent list.
	if verb == "list" {
		args = slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--json" })
	}
	switch verb {
	case "list":
		if len(args) != 1 || strings.HasPrefix(args[0], "-") {
			return usage()
		}
	case "agents":
		if len(args) != 3 {
			return usage()
		}
	default:
		if len(args) < 2 || strings.HasPrefix(args[0], "-") {
			return usage()
		}
	}
	if verb == "list" {
		if slices.Contains(rest, "--json") {
			return listProjectSubagentsJSON(args[0])
		}
		return listProjectSubagents(args[0])
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
		if _, ok := p.Subagents[name]; !ok {
			return fmt.Errorf("%s has no subagent %s", dir, name)
		}
		agents, err := subagentProjectAgents(args[2])
		if err != nil {
			return err
		}
		valid := slices.ContainsFunc(agents, func(a string) bool { return library.ProjectSubagentsDir(a) != "" })
		if len(agents) > 0 && !valid {
			return fmt.Errorf("magpie knows of no folder %s reads a project's subagents from", strings.Join(agents, ", "))
		}
		res, err := library.ProjectSubagents(dir, []string{name}, agents, options)
		if err != nil || options.Preview {
			return projectCommandResult(res, err, options.Preview)
		}
		if err := projectOutcome(dir, nil, res); err != nil {
			return err
		}
		if len(agents) == 0 {
			fmt.Println(green.Render("✓"), name, "taken out of", dir)
		} else if !projectSkipped(res, dir, "subagent:"+name) || len(agents) > 1 {
			fmt.Println(green.Render("✓"), name, "→", strings.Join(agents, ", "))
		}
		return nil
	}
	if verb == "rm" || verb == "remove" {
		res, err := library.ProjectSubagents(dir, args[1:], nil, options)
		if err != nil || options.Preview {
			return projectCommandResult(res, err, options.Preview)
		}
		if err := projectOutcome(dir, nil, res); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), strings.Join(args[1:], ", "), "taken out of", dir)
		return nil
	}
	var names, agents []string
	given := false
	for _, a := range args[1:] {
		if s, ok := strings.CutPrefix(a, "agents="); ok {
			if agents, err = subagentProjectAgents(s); err != nil {
				return err
			}
			given = true
			continue
		}
		if strings.HasPrefix(a, "-") {
			return usage()
		}
		names = append(names, a)
	}
	if len(names) == 0 {
		return usage()
	}
	res, err := library.ProjectSubagents(dir, names, agents, options)
	if err != nil || options.Preview {
		return projectCommandResult(res, err, options.Preview)
	}
	if err := projectOutcome(dir, nil, res); err != nil {
		return err
	}
	if given && len(agents) == 0 {
		fmt.Println(green.Render("✓"), strings.Join(names, ", "), "taken out of", dir)
		return nil
	}
	// Read the view again: the one above was read before the write, so its
	// agent list is the one from before this command (#WSL 8.2).
	v, err = library.Read(res.Problems)
	if err != nil {
		return err
	}
	p = projectView(v, dir)
	if p == nil {
		return fmt.Errorf("%s isn't a project", dir)
	}
	for _, n := range names {
		if projectSkipped(res, dir, "subagent:"+n) && len(subagentPlaces(p, n)) == 0 {
			continue
		}
		fmt.Println(green.Render("✓"), n, "→", strings.Join(p.Subagents[n], ", "), muted.Render(strings.Join(subagentPlaces(p, n), " ")))
	}
	return nil
}

// subagentProjectAgents is a comma list of agents each of which reads a
// project's subagents (none for no agent).
func subagentProjectAgents(s string) ([]string, error) {
	if s == "" || s == "none" {
		return nil, nil
	}
	var out []string
	for _, a := range strings.Split(s, ",") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// subagentPlaces are the files magpie wrote for project p's subagents
// (ProjectView.AgentFiles is a list of paths, one per file written).
func subagentPlaces(p *library.ProjectView, n string) []string {
	out := make([]string, 0, len(p.AgentFiles))
	for _, f := range p.AgentFiles {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// listProjectSubagents prints a project's subagents: which agents each is for,
// and the file magpie wrote.
func listProjectSubagents(dir string) error {
	p, err := library.ReadProject(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(p.Subagents))
	for n := range p.Subagents {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Println(bold.Render(p.Name) + " " + muted.Render(p.Dir) + " " + muted.Render("subagents"))
	printProjectNative(p, "subagents")
	if len(names) == 0 && len(p.Native["subagents"]) == 0 {
		fmt.Println(" ", muted.Render("no subagents yet · magpie library project subagents add "+quoteArg(p.Dir)+" <name…>"))
		return nil
	}
	for _, n := range names {
		line := fmt.Sprintf("  %s → %s", bold.Render(n), strings.Join(p.Subagents[n], ", "))
		if why := p.Problems["subagent:"+n]; why != "" {
			fmt.Printf("%s %s\n", amber.Render("!"), bold.Render(line))
			fmt.Println("   ", muted.Render(why))
			continue
		}
		fmt.Printf("%s %s\n", line, muted.Render(strings.Join(subagentPlaces(p, n), " ")))
	}
	return nil
}

// listProjectSubagentsJSON prints a project the way `… projects --json` does,
// so a script reads its subagents and the files magpie wrote from one object.
func listProjectSubagentsJSON(dir string) error {
	p, err := library.ReadProject(dir)
	if err != nil {
		return err
	}
	return printJSON([]any{map[string]any{
		"native":     p.Native["subagents"],
		"dir":        p.Dir,
		"name":       p.Name,
		"subagents":  p.Subagents,
		"agentFiles": p.AgentFiles,
		"problems":   p.Problems,
	}})
}
