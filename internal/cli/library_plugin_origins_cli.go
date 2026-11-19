package cli

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/library"
)

func libraryPluginObserveCmd(args []string) error {
	if len(args) < 1 || len(args) > 2 || strings.HasPrefix(args[0], "-") ||
		len(args) == 2 && args[1] != "--claude-startup" {
		return fmt.Errorf("usage: magpie library plugin observe <dir> [--claude-startup]")
	}
	if err := library.RecordPluginDiscovery(args[0], os.Stdin, len(args) == 2); err != nil {
		return err
	}
	fmt.Println("Recorded plugin discovery for this session.")
	return nil
}

func printPluginOrigins(rows []library.PluginOrigin) {
	origins := map[string]string{
		"library": "MagPie library", "claude-native": "Claude Code native",
		"project": "project-provided", "codex-mcp": "independent Codex MCP", "pi-package": "Pi native package",
	}
	for _, row := range rows {
		labels := strings.TrimSpace(strings.Join([]string{origins[row.Origin], row.Agent, row.Scope, row.Version}, " "))
		fmt.Printf("%s  %s  %s\n", bold.Render(row.Name), muted.Render(labels), pluginStatusText(row.PluginStatus))
		if row.Source != "" {
			fmt.Println("  ", muted.Render(row.Source))
		}
		for _, part := range row.Parts {
			if part.Agent == "" {
				fmt.Printf("    %s %s\n", part.Kind, part.Name)
			} else {
				fmt.Printf("    %s %s · %s  %s\n", part.Kind, part.Name, part.Agent, pluginStatusText(part.PluginStatus))
			}
		}
	}
}

// Keep the raw origin and the exact discovery fields so existing readers
// can still attribute an observation to the same record.
type projectPluginOrigin struct {
	library.PluginOrigin
	ProjectOrigin string `json:"projectOrigin"`
	Managed       bool   `json:"managed"`
}

func printProjectPluginSources(p *library.ProjectView, rows []library.PluginOrigin, asJSON bool) error {
	const other = "other"
	tagged := make([]projectPluginOrigin, 0, len(rows))
	groups := map[string][]library.PluginOrigin{}
	recorded := map[string]bool{}
	for _, row := range rows {
		origin := other
		if row.Scope == "project" || row.Scope == "local" {
			origin = projectProvided
			if row.Managed || row.Origin == "library" {
				origin = projectAssigned
			}
			if row.Origin == "library" {
				recorded[row.Name] = true
			}
		}
		tagged = append(tagged, projectPluginOrigin{row, origin, origin == projectAssigned})
		groups[origin] = append(groups[origin], row)
	}
	for _, id := range slices.Sorted(maps.Keys(p.ClaudePlugins)) {
		if recorded[id] {
			continue
		}
		on := p.ClaudePlugins[id]
		row := library.PluginOrigin{Name: id, Origin: "library", Agent: "claude", Scope: "project",
			PluginStatus: library.PluginStatus{Enabled: &on}}
		tagged = append(tagged, projectPluginOrigin{row, projectAssigned, true})
		groups[projectAssigned] = append(groups[projectAssigned], row)
	}
	if asJSON {
		return printJSON(struct {
			*library.ProjectView
			PluginOrigins []projectPluginOrigin `json:"pluginOrigins"`
		}{p, tagged})
	}

	fmt.Println(bold.Render(p.Name), muted.Render(p.Dir), muted.Render(projectWords("plugin sources", "插件来源")))
	for _, origin := range []string{projectProvided, projectAssigned} {
		printProjectSource(origin, len(groups[origin]))
		printPluginOrigins(groups[origin])
	}
	if len(groups[other]) != 0 {
		fmt.Println(" ", bold.Render(projectWords("Other plugin sources", "其他插件来源")))
		printPluginOrigins(groups[other])
	}
	if len(tagged) == 0 {
		fmt.Println(" ", muted.Render(projectWords("No plugin sources found", "未发现插件来源")))
	}
	return nil
}

func pluginStatusText(st library.PluginStatus) string {
	word := func(v *bool, yes, no string) string {
		switch {
		case v == nil:
			return "unknown"
		case *v:
			return yes
		default:
			return no
		}
	}
	return "installed=" + word(st.Installed, "yes", "no") + " enabled=" + word(st.Enabled, "yes", "no") + " discovered=" + word(st.Discovered, "yes", "no")
}
