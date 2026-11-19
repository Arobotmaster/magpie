package cli

import (
	"fmt"
	"strings"

	"github.com/yetone/magpie/internal/library"
)

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
