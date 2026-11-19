package cli

import (
	"fmt"
	"os"
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
