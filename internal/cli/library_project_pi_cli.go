package cli

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/yetone/magpie/internal/library"
)

// libraryProjectPiCmd is magpie library project pi …: the project's own Pi
// packages and extensions, written into its own .pi/settings.json.
func libraryProjectPiCmd(rest []string, usage func() error, options library.ProjectWriteOptions) error {
	if len(rest) == 0 {
		return usage()
	}
	if rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help" {
		fmt.Print("  " + libraryProjectUsage)
		return nil
	}
	if rest[0] == "adopt" {
		if len(rest) != 4 && len(rest) != 5 {
			return usage()
		}
		scope := library.ScopeProject
		if len(rest) == 5 {
			var ok bool
			scope, ok = strings.CutPrefix(rest[4], "--scope=")
			if !ok {
				return usage()
			}
		}
		dir, err := projectDir(rest[1])
		if err != nil {
			return err
		}
		res, err := library.AdoptProjectPi(dir, rest[2], rest[3], scope, options)
		if err := projectCommandResult(res, err, options.Preview); err != nil || options.Preview {
			return err
		}
		fmt.Println(green.Render("✓"), rest[3], "adopted in", dir, "(scope "+scope+")")
		return nil
	}
	// `project pi scope <dir> <kind> <source> anywhere|project`: where a Pi
	// entry belongs (project_role.go)
	if rest[0] == "scope" {
		if len(rest) != 5 || strings.HasPrefix(rest[1], "-") {
			return usage()
		}
		dir, err := projectDir(rest[1])
		if err != nil {
			return err
		}
		res, err := library.ProjectPiScope(dir, rest[2], rest[3], rest[4], options)
		if err != nil || options.Preview {
			return projectCommandResult(res, err, options.Preview)
		}
		if err := projectOutcome(dir, nil, res); err != nil {
			return err
		}
		if rest[4] == library.ScopeProject {
			fmt.Println(green.Render("✓"), rest[3], "belongs to", dir, "alone: a worktree of its repository doesn't load it")
		} else {
			fmt.Println(green.Render("✓"), rest[3], "is loaded in each worktree of", dir+"'s repository again")
		}
		return nil
	}
	// `project pi <dir> [--json]`, the project's entries
	if rest[0] != "add" && rest[0] != "off" && rest[0] != "on" && rest[0] != "rm" && rest[0] != "remove" {
		asJSON := slices.Contains(rest, "--json")
		rest = slices.DeleteFunc(slices.Clone(rest), func(a string) bool { return a == "--json" })
		if len(rest) != 1 || strings.HasPrefix(rest[0], "-") {
			return usage()
		}
		if asJSON {
			return listProjects(rest[0], true)
		}
		return listProjectPi(rest[0])
	}
	verb, args := rest[0], rest[1:]
	if len(args) < 2 || strings.HasPrefix(args[0], "-") {
		return usage()
	}
	dir, err := projectDir(args[0])
	if err != nil {
		return err
	}
	kind := args[1]
	if kind != "package" && kind != "extension" {
		return fmt.Errorf("%q isn't a Pi kind: package or extension", kind)
	}
	var sources []string
	for _, a := range args[2:] {
		if strings.HasPrefix(a, "-") {
			return usage()
		}
		sources = addOnce(sources, a)
	}
	if len(sources) == 0 {
		return fmt.Errorf("no %s given", kind)
	}
	// add/on: give it; off: keep the entry, stop loading it (Pi's "-<source>");
	// rm/remove: take it out of the project for real.
	mode := library.PiOn
	switch verb {
	case "add", "on":
		mode = library.PiOn
	case "off":
		mode = library.PiOff
	default:
		mode = library.PiRemove
	}
	if mode != library.PiOn {
		v, err := library.Inspect(nil)
		if err != nil {
			return err
		}
		p := projectView(v, dir)
		if p == nil {
			return fmt.Errorf("%s isn't a project", dir)
		}
		have := map[string]bool{}
		for _, e := range p.PiEntries {
			if e.Kind == kind {
				have[e.Source] = true
			}
		}
		for _, s := range sources {
			if !have[s] {
				return fmt.Errorf("%s has no %s %s", dir, kind, s)
			}
		}
	}
	res, err := library.ProjectPiChange(dir, kind, sources, mode, options)
	if err != nil || options.Preview {
		return projectCommandResult(res, err, options.Preview)
	}
	for _, p := range res.Problems {
		if p.Agent == dir && strings.HasPrefix(p.What, "project:pi:") {
			return fmt.Errorf("%s", p.Error)
		}
	}
	if err := projectOutcome(dir, nil, res); err != nil {
		return err
	}
	switch mode {
	case library.PiOn:
		fmt.Println(green.Render("✓"), strings.Join(sources, ", "), "→", dir, muted.Render("project's own .pi/settings.json"))
	case library.PiOff:
		fmt.Println(green.Render("✓"), strings.Join(sources, ", "), "turned off in", dir, muted.Render("the entry stays"))
	default:
		fmt.Println(green.Render("✓"), strings.Join(sources, ", "), "taken out of", dir)
	}
	return nil
}

// listProjectPi prints one project's Pi packages and extensions, and which
// magpie wrote into the project's own settings.
func listProjectPi(dir string) error {
	p, err := library.ReadProject(dir)
	if err != nil {
		return err
	}
	fmt.Println(bold.Render(p.Name) + " " + muted.Render(p.Dir) + " " + muted.Render("Pi"))
	printProjectNative(p, "pi")
	if len(p.PiEntries) == 0 && len(p.Native["pi"]) == 0 {
		fmt.Println(" ", muted.Render("no Pi packages or extensions yet · magpie library project pi add "+quoteArg(p.Dir)+" package|extension <source…>"))
		return nil
	}
	for _, kind := range []string{"package", "extension"} {
		var rows []string
		for _, e := range p.PiEntries {
			if e.Kind == kind {
				rows = append(rows, e.Source)
			}
		}
		if len(rows) == 0 {
			continue
		}
		sort.Strings(rows)
		fmt.Printf("  %s %s\n", bold.Render(kind+"s"), muted.Render(library.ProjectPiSettings("pi")))
		for _, s := range rows {
			if why := p.Problems["pi:"+s]; why != "" {
				fmt.Printf("    %s %s %s\n", amber.Render("!"), s, muted.Render(why))
				continue
			}
			scope := ""
			if slices.Contains(p.PiScoped[kind], s) {
				scope = muted.Render(" (project scope)")
			}
			fmt.Printf("    %s%s\n", s, scope)
		}
	}
	return nil
}

// libraryProjectPluginCmd is magpie library project plugin …: the Claude
// Code plugins a project turns on or off in its own settings.local.json.
func libraryProjectPluginCmd(rest []string, usage func() error, options library.ProjectWriteOptions) error {
	if len(rest) == 0 {
		return usage()
	}
	if rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help" {
		fmt.Print("  " + libraryProjectUsage)
		return nil
	}
	if rest[0] != "on" && rest[0] != "off" {
		asJSON := slices.Contains(rest, "--json")
		rest = slices.DeleteFunc(slices.Clone(rest), func(a string) bool { return a == "--json" })
		if len(rest) != 1 || strings.HasPrefix(rest[0], "-") {
			return usage()
		}
		return listProjectPlugins(rest[0], asJSON)
	}
	on := rest[0] == "on"
	if len(rest) != 3 {
		return usage()
	}
	dir, err := projectDir(rest[1])
	if err != nil {
		return err
	}
	id := rest[2]
	res, err := library.ProjectClaudePlugin(dir, id, on, options)
	if err != nil || options.Preview {
		return projectCommandResult(res, err, options.Preview)
	}
	if err := projectOutcome(dir, nil, res); err != nil {
		return err
	}
	state := "on"
	if !on {
		state = "off"
	}
	fmt.Println(green.Render("✓"), id, state, "in", dir, muted.Render("its own .claude/settings.local.json"))
	return nil
}

func listProjectPlugins(dir string, asJSON bool) error {
	p, err := library.ReadProject(dir)
	if err != nil {
		return err
	}
	rows, err := library.ReadPluginOrigins(p.Dir)
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(struct {
			*library.ProjectView
			PluginOrigins []library.PluginOrigin `json:"pluginOrigins"`
		}{p, rows})
	}
	fmt.Println(bold.Render(p.Name) + " " + muted.Render(p.Dir) + " " + muted.Render("plugin sources"))
	printPluginOrigins(rows)
	return nil
}
