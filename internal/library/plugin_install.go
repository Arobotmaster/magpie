package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// Installing a plugin is the agent's own job: the library decomposes a plugin
// into parts and writes those, but the plugin itself only loads once that agent
// has an install record for it. Measured on a fresh config dir: with the parts
// written and no record, Claude Code reports five cache-miss errors and loads
// no plugin; with the record, the same session loads its agents and skills.
//
// So MagPie drives the agent's own installer, through the same runner the skill
// installer already uses for git (proc.CommandContext / proc.FindTool), rather
// than keeping a second notion of "installed". Nothing here decides what a
// plugin is; it only asks the agent to install what MagPie already holds.
//
// The install is per agent, and for Claude Code per project when the user asks
// for it: a local-scope install records the project it was made in, and a
// record for another project is not this project's.
type PluginInstallResult struct {
	Agent   string `json:"agent"`
	Scope   string `json:"scope"`
	Project string `json:"project,omitempty"`
	Output  string `json:"output,omitempty"`
}

// pluginInstallTimeout bounds one agent's installer.
const pluginInstallTimeout = 10 * time.Minute

// pluginInstallers is how each agent installs a plugin from a folder this
// computer has. Only agents with a plugin installer are here; a plugin given to
// one of the others is parts only, and the page says so.
var pluginInstallers = map[string]func(ctx context.Context, dir, name, scope, project string) (string, error){
	"claude": claudeInstallPlugin,
}

// PluginInstallers are the agents whose own installer MagPie can drive.
func PluginInstallers() []string {
	out := make([]string, 0, len(pluginInstallers))
	for id := range pluginInstallers {
		out = append(out, id)
	}
	return out
}

// InstallPlugin has agent install the library's plugin name, either for the
// user or (scope "local") for project dir. It reads the plugin's folder and
// hands it to the agent's own command; MagPie keeps no install state of its own.
func InstallPlugin(ctx context.Context, name, agent, scope, project string) (*PluginInstallResult, error) {
	inst, ok := pluginInstallers[agent]
	if !ok {
		return nil, fmt.Errorf("%s has no plugin installer magpie can drive: its plugins are given as parts only", agent)
	}
	dir := pluginDir(name)
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json")); err != nil {
		return nil, fmt.Errorf("%s isn't a plugin folder magpie can install from: %w", dir, err)
	}
	if scope == "local" {
		if project == "" {
			return nil, fmt.Errorf("a local install needs the project it is for")
		}
		abs, err := ProjectPath(project)
		if err != nil {
			return nil, err
		}
		project = abs
	} else if scope != "user" && scope != "" {
		return nil, fmt.Errorf("no plugin install scope %q: user or local", scope)
	}
	cctx, cancel := context.WithTimeout(ctx, pluginInstallTimeout)
	defer cancel()
	out, err := inst(cctx, dir, name, scope, project)
	if err != nil {
		return nil, err
	}
	return &PluginInstallResult{Agent: agent, Scope: scope, Project: project, Output: strings.TrimSpace(out)}, nil
}

// claudeInstallPlugin installs dir through Claude Code's own commands: the
// marketplace it needs for a folder of this computer, then the install itself.
func claudeInstallPlugin(ctx context.Context, dir, name, scope, project string) (string, error) {
	cli := proc.FindTool("claude")
	if cli == "" {
		return "", fmt.Errorf("claude isn't on this computer's PATH, so magpie can't drive its installer")
	}
	// Claude Code installs plugins from a MARKETPLACE, and magpie keeps a
	// plugin as a folder, so a one-entry marketplace is made for it beside the
	// plugin (under plugin-data, which an update keeps) and registered as this
	// computer's. Its entry contains a copy of the bundle files.
	mkt, err := pluginMarketplace(name)
	if err != nil {
		return "", err
	}
	args := []string{"plugin", "marketplace", "add", mkt}
	if scope == "local" {
		args = append(args, "--scope", "local")
	}
	if out, err := runAgentCLI(ctx, cli, args, project); err != nil {
		// already added is fine: the next command is what matters
		if !strings.Contains(strings.ToLower(out), "already") {
			return out, fmt.Errorf("claude plugin marketplace add: %w\n%s", err, strings.TrimSpace(out))
		}
	}
	inst := []string{"plugin", "install", name, "--scope", scope}
	if scope == "" {
		inst = []string{"plugin", "install", name}
	}
	return runAgentCLI(ctx, cli, inst, project)
}

// runAgentCLI runs an agent's own command, in project's directory when it is
// given (Claude Code records a local install against the folder it ran in).
func runAgentCLI(ctx context.Context, cli string, args []string, project string) (string, error) {
	cmd := proc.CommandContext(ctx, cli, args...)
	if project != "" {
		cmd.Dir = project
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// pluginInstallRecord is one entry of Claude Code's installed_plugins.json as
// this file reads it.
type claudeInstallRecord struct {
	Scope       string `json:"scope"`
	Version     string `json:"version"`
	InstallPath string `json:"installPath"`
	ProjectPath string `json:"projectPath"`
}

// claudeRecords reads Claude Code's install record, by plugin@marketplace.
func claudeRecords() map[string][]claudeInstallRecord {
	records, _ := readClaudeInstallRecords()
	return records
}

func readClaudeInstallRecords() (map[string][]claudeInstallRecord, error) {
	b, err := os.ReadFile(claudeInstalled())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Plugins map[string][]claudeInstallRecord `json:"plugins"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	return doc.Plugins, nil
}

// pluginMarketplaceDir is where a plugin's one-entry marketplace is kept: a
// copy of the plugin's folder beside its manifest (pluginMarketplace).
func pluginMarketplaceDir(name string) string { return pluginDir(name) + "-marketplace" }

// pluginMarketplace writes a one-entry marketplace that holds the plugin's
// files and returns its folder. Claude Code refuses a bare plugin folder as a
// marketplace: it wants .claude-plugin/marketplace.json.
//
// The entry is a COPY of the plugin's folder, never a link to it. Claude Code
// resolves a plugin's own module paths against the plugin directory it loaded,
// and a link is followed as it resolves them: a hooks module reached through
// the link lands on a real path outside that directory, which Claude Code then
// refuses ("Path escapes plugin directory"), so the module never loads and its
// hook never runs, while the install still reports success. Copy the
// files again whenever the plugin folder changes.
func pluginMarketplace(name string) (string, error) {
	// The entry's source is a plain "./<name>" relative to this folder: Claude
	// Code refuses an absolute path and one that starts with "..".
	root := pluginMarketplaceDir(name)
	manifest := filepath.Join(root, ".claude-plugin", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		return "", err
	}
	// Claude Code wants the entry's source RELATIVE to the marketplace, as
	// "./…": an absolute path is refused ("Invalid string: must start with ./").
	doc := map[string]any{
		"name":  "magpie-" + name,
		"owner": map[string]string{"name": "magpie"},
		"plugins": []map[string]any{{
			"name":   name,
			"source": "./" + name,
		}},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(manifest, append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	// the files first, beside the old entry, and swapped in once copied: a
	// reader never sees a half-copied plugin, and a folder an older magpie
	// left as a link is replaced by a copy rather than followed
	entry, stage := filepath.Join(root, name), filepath.Join(root, name+".new")
	os.RemoveAll(stage)
	if err := copyDir(pluginDir(name), stage); err != nil {
		os.RemoveAll(stage)
		return "", fmt.Errorf("the marketplace entry needs %s beside it: %w", entry, err)
	}
	// RemoveAll takes a link itself, not what it points at
	os.RemoveAll(entry)
	if err := os.Rename(stage, entry); err != nil {
		os.RemoveAll(stage)
		return "", fmt.Errorf("the marketplace entry needs %s beside it: %w", entry, err)
	}
	return root, nil
}
