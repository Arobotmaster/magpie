package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yetone/magpie/internal/library"
)

func libraryPluginProvenanceCmd(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("usage: magpie library plugin provenance <name> [project=<dir>] [pi-source=<spec>] [claude-startup=<jsonl>]")
	}
	var project, piSource, startupPath string
	for _, arg := range args[1:] {
		key, val, _ := strings.Cut(arg, "=")
		if val == "" {
			return fmt.Errorf("provenance option %q needs a value", key)
		}
		switch key {
		case "project":
			project = val
		case "pi-source":
			piSource = val
		case "claude-startup":
			startupPath = val
		default:
			return fmt.Errorf("unknown provenance option %q", key)
		}
	}
	var startup io.Reader
	if startupPath != "" {
		f, err := os.Open(startupPath)
		if err != nil {
			return err
		}
		defer f.Close()
		startup = f
	}
	report, err := library.ReadPluginProvenance(context.Background(), args[0], project, piSource, startup)
	if err != nil {
		return err
	}
	return printJSON(report)
}

// libraryPluginInstallCmd is `magpie library plugin install`: it has the
// agent's own installer install a plugin the library already keeps.
//
// The library writes a plugin's parts; it does not install the plugin, and a
// plugin with no install record does not load however many parts were written.
// This drives the agent's installer (reusing the runner the skill installer
// uses for git) instead of adding a second notion of "installed".
func libraryPluginInstallCmd(args []string) error {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("usage:\n  %s", libraryBundleUsage)
	}
	name := args[0]
	agent, scope, project := "claude", "user", ""
	for _, a := range args[1:] {
		switch {
		case strings.HasPrefix(a, "agent="):
			agent = strings.TrimPrefix(a, "agent=")
		case strings.HasPrefix(a, "scope="):
			scope = strings.TrimPrefix(a, "scope=")
		case strings.HasPrefix(a, "project="):
			project = strings.TrimPrefix(a, "project=")
		default:
			return fmt.Errorf("no library plugin install option %q", a)
		}
	}
	if scope == "local" && project == "" {
		return fmt.Errorf("scope=local needs project=<dir>")
	}
	res, err := library.InstallPlugin(context.Background(), name, agent, scope, project)
	if err != nil {
		return err
	}
	fmt.Println(green.Render("✓"), name, "installed in", res.Agent, muted.Render("("+res.Scope+")"))
	// what the agent recorded, read back rather than assumed
	st := library.PluginInstallFor(name, res.Agent, project)
	if st.Installed {
		fmt.Println("  ", muted.Render("record: "+st.Scope+" "+st.Version))
	} else {
		fmt.Println("  ", amber.Render("the install command succeeded but no record was found afterwards"))
	}
	return nil
}
