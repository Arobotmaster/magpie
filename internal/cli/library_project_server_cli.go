package cli

import (
	"fmt"
	"strings"

	"github.com/yetone/magpie/internal/library"
)

// libraryProjectServerCmd uses the same project MCP server operations
// as the Library's Projects card (projects/server).
func libraryProjectServerCmd(rest []string, usage func() error, options library.ProjectWriteOptions) error {
	if len(rest) == 0 {
		return usage()
	}
	verb, args := rest[0], rest[1:]
	// with no verb, `… project server <dir>` lists it, as `… hooks <dir>` does
	if !slicesContains([]string{"add", "rm", "remove", "agents", "list", "help", "-h", "--help"}, verb) {
		verb, args = "list", rest
	}
	switch verb {
	case "help", "-h", "--help":
		fmt.Print("  " + libraryProjectUsage)
		return nil
	case "add", "rm", "remove", "agents", "list":
	default:
		return fmt.Errorf("no library project server command %q\n  %s", verb, libraryProjectUsage)
	}
	if verb == "list" {
		args = slicesDelete(args, "--json")
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
		return listProjectResources(args[0], "servers", slicesContains(rest, "--json"))
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
		name := args[1]
		if _, ok := p.Servers[name]; !ok {
			return fmt.Errorf("%s has no server %s", dir, name)
		}
		agents, err := projectServerAgents(args[2])
		if err != nil {
			return err
		}
		res, err := library.ProjectServer(dir, name, agents, options)
		if err != nil || options.Preview {
			return projectCommandResult(res, err, options.Preview)
		}
		if err := projectOutcome(dir, nil, res); err != nil {
			return err
		}
		if len(agents) == 0 {
			fmt.Println(green.Render("✓"), name, "taken out of", dir)
		} else {
			fmt.Println(green.Render("✓"), name, "→", strings.Join(agents, ", "))
		}
		return nil
	}
	if verb == "rm" || verb == "remove" {
		res, err := library.ProjectServer(dir, args[1], nil, options)
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
	for _, a := range args[1:] {
		if s, ok := strings.CutPrefix(a, "agents="); ok {
			if agents, err = projectServerAgents(s); err != nil {
				return err
			}
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
	res, err := library.ProjectServer(dir, names[0], agents, options)
	if err != nil || options.Preview {
		return projectCommandResult(res, err, options.Preview)
	}
	if err := projectOutcome(dir, nil, res); err != nil {
		return err
	}
	// re-read: the view above was read before the write (#WSL 8.2)
	v, err = library.Read(res.Problems)
	if err != nil {
		return err
	}
	p = projectView(v, dir)
	if p == nil {
		return fmt.Errorf("%s isn't a project", dir)
	}
	for _, n := range names {
		fmt.Println(green.Render("✓"), n, "→", strings.Join(p.Servers[n], ", "))
	}
	return nil
}

// projectServerAgents is a comma list of agents each of which reads a project's
// MCP servers (none for no agent).
func projectServerAgents(s string) ([]string, error) {
	if s == "" || s == "none" {
		return nil, nil
	}
	var out []string
	for _, a := range strings.Split(s, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out, nil
}
