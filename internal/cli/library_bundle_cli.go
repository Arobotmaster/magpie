package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yetone/magpie/internal/library"
)

const libraryBundleUsage = `magpie library plugin [--json]      plugin sources, parts and agent status
  magpie library plugin project=<dir> [--json]   include that project's session discovery
  magpie library plugin observe <dir> [--claude-startup]   record session registration evidence from stdin
  magpie library plugin provenance <name> [project=<dir>] [pi-source=<spec>] [claude-startup=<jsonl>]
                                     this machine's library source, native installs and observed startup versions as JSON
  magpie library plugin probe <source>   what a folder, git address or marketplace.json holds; writes nothing
  magpie library plugin add <source> [name…] [agents=a,b…]   install it: its skills, MCP servers, hooks
                                     and subagents become ordinary library items, given to the agents named
                                     (none named: every candidate in a marketplace)
  magpie library plugin rm <name>         take it and its parts out of the library
  magpie library plugin on|off <name>     give its parts back to the agents they were for, or take them from every
                                     agent, keeping its settings
  magpie library plugin agents <name> <a,b…|none>   the agents its parts are given to
  magpie library plugin update <name>     read its source again
                                     A plugin is read from .claude-plugin/plugin.json and, when it ships one, from
                                     .codex-plugin/plugin.json; a part both name is carried once.
`

// libraryPluginCmd is magpie library plugin …: the window's Library › Plugins
// page from a terminal, through the same library calls the page makes. A
// plugin is kept as one item whose parts (skills, MCP servers, hooks,
// subagents) become ordinary library items, so the other tabs and their CLIs
// go on working.
//
// A plugin is read from Claude Code's manifest and from Codex's
// (`.codex-plugin/plugin.json`), so a plugin that ships only Codex's, or
// ships both, is carried the same way.
func libraryBundleCmd(rest []string, usage func() error) error {
	if len(rest) == 1 && (rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help") {
		fmt.Print("  " + libraryBundleUsage)
		return nil
	}
	// `--json` alone is the query, as `library project --json` is.
	if len(rest) == 1 && rest[0] == "--json" || len(rest) == 2 && rest[0] == "list" && rest[1] == "--json" {
		return listBundles(true)
	}
	if len(rest) == 0 || len(rest) == 1 && rest[0] == "list" {
		return listBundles(false)
	}
	if dir, ok := strings.CutPrefix(rest[0], "project="); ok {
		if dir == "" || len(rest) > 2 || len(rest) == 2 && rest[1] != "--json" {
			return usage()
		}
		return listProjectPlugins(dir, len(rest) == 2)
	}
	verb, args := rest[0], rest[1:]
	switch verb {
	case "help", "-h", "--help":
		fmt.Print("  " + libraryBundleUsage)
		return nil
	case "provenance":
		return libraryPluginProvenanceCmd(args)
	case "observe":
		return libraryPluginObserveCmd(args)
	case "probe", "add", "install", "rm", "remove", "on", "off", "agents", "update":
	default:
		return fmt.Errorf("no library plugin command %q\n  %s", verb, libraryBundleUsage)
	}
	if verb == "install" {
		return libraryPluginInstallCmd(args)
	}
	if verb == "probe" || verb == "add" {
		if len(args) < 1 || strings.HasPrefix(args[0], "-") {
			return usage()
		}
		return bundleInstallCmd(verb, args)
	}
	if verb == "agents" {
		if len(args) != 2 || strings.HasPrefix(args[0], "-") {
			return usage()
		}
	} else if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return usage()
	}
	name := args[0]
	switch verb {
	case "rm", "remove":
		if _, err := library.RemovePlugin(name); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), name, "is out of the library")
		return nil
	case "on", "off":
		if _, err := library.PluginOn(name, verb == "on"); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), name, "is", verb)
		return nil
	case "update":
		if _, err := library.UpdatePlugin(name); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), name, "read again from its source")
		return nil
	case "agents":
		var agents []string
		if s := args[1]; s != "" && s != "none" {
			for _, a := range strings.Split(s, ",") {
				if a = strings.TrimSpace(a); a != "" {
					agents = append(agents, a)
				}
			}
		}
		if _, err := library.PluginAgents(name, agents); err != nil {
			return err
		}
		if len(agents) == 0 {
			fmt.Println(green.Render("✓"), name, "is given to no agent")
		} else {
			fmt.Println(green.Render("✓"), name, "→", strings.Join(agents, ", "))
		}
		return nil
	}
	return usage()
}

// pluginInstallCmd probes a plugin's source and, for add/install, installs
// the named ones (all of them when none is named), for the agents given.
func bundleInstallCmd(verb string, args []string) error {
	source := args[0]
	var names, agents []string
	for _, a := range args[1:] {
		if s, ok := strings.CutPrefix(a, "agents="); ok {
			if s != "" && s != "none" {
				for _, x := range strings.Split(s, ",") {
					if x = strings.TrimSpace(x); x != "" {
						agents = append(agents, x)
					}
				}
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("no library plugin option %q", a)
		}
		names = append(names, a)
	}
	probe, err := library.ProbePlugins(source)
	if err != nil {
		return err
	}
	if len(probe.Candidates) == 0 {
		return fmt.Errorf("no plugin in %s: it has no .claude-plugin/plugin.json, .codex-plugin/plugin.json, skills, agents, hooks or .mcp.json", source)
	}
	if verb == "probe" {
		for _, c := range probe.Candidates {
			parts := []string{}
			for _, p := range []struct {
				n string
				v int
			}{{"skills", c.Skills}, {"mcp", c.MCP}, {"hooks", c.Hooks}, {"subagents", c.Subagents}} {
				if p.v > 0 {
					parts = append(parts, fmt.Sprintf("%s %d", p.n, p.v))
				}
			}
			detail := strings.Join(parts, " · ")
			if c.Remote {
				detail = "fetched when installed"
			}
			fmt.Printf("  %s %s  %s\n", bold.Render(c.Name), muted.Render(c.Version), detail)
		}
		return nil
	}
	if len(names) == 0 {
		for _, c := range probe.Candidates {
			names = append(names, c.Name)
		}
	}
	have := map[string]bool{}
	for _, c := range probe.Candidates {
		have[c.Name] = true
	}
	for _, n := range names {
		if !have[n] {
			return fmt.Errorf("%s has no plugin called %s", source, n)
		}
	}
	if _, err := library.InstallPlugins(source, names, agents); err != nil {
		return err
	}
	fmt.Println(green.Render("✓"), strings.Join(names, ", "), "installed")
	if len(agents) > 0 {
		fmt.Println(" ", muted.Render("its parts are given to "+strings.Join(agents, ", ")))
	} else {
		fmt.Println(" ", muted.Render("give its parts to an agent: magpie library plugin agents <name> <a,b…>"))
	}
	return nil
}

func listBundles(asJSON bool) error {
	rows, err := library.ReadPluginOrigins("")
	if err != nil {
		return err
	}
	if !asJSON {
		if len(rows) == 0 {
			fmt.Println(muted.Render("no plugins yet · magpie library plugin add <folder|git address|marketplace.json>"))
		}
		printPluginOrigins(rows)
		return nil
	}
	v, err := library.Read(nil)
	if err != nil {
		return err
	}
	type pluginReport struct {
		library.PluginView
		Origins []library.PluginOrigin `json:"origins"`
	}
	views := map[string]library.PluginView{}
	for _, plugin := range v.Plugins {
		views[plugin.Name] = plugin
	}
	byName := map[string][]library.PluginOrigin{}
	var names []string
	for _, row := range rows {
		if _, ok := byName[row.Name]; !ok {
			names = append(names, row.Name)
		}
		byName[row.Name] = append(byName[row.Name], row)
	}
	sort.Strings(names)
	out := make([]pluginReport, 0, len(names))
	for _, name := range names {
		view, ok := views[name]
		if !ok {
			view.Plugin = &library.Plugin{Name: name, Agents: []string{}}
		}
		out = append(out, pluginReport{view, byName[name]})
	}
	return printJSON(out)
}
