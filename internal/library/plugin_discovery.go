package library

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/yetone/magpie/internal/edit"
)

// A snapshot describes one session in one project. The observer supplies keys
// only for registrations it can attribute to a specific source. An absence
// additionally requires a complete runtime enumeration of that kind.
type pluginDiscoveryObservation struct {
	Cwd      string   `json:"cwd"`
	Tool     string   `json:"tool"`
	Complete []string `json:"complete"`
	Seen     []string `json:"seen"`
	Missing  []string `json:"missing"`
}

type pluginDiscoveryTarget struct {
	tool, kind string
	key        *string
	status     *PluginStatus
}

func pluginDiscoveryProject(dir string) (string, error) {
	dir, err := ProjectPath(dir)
	if err != nil {
		return "", err
	}
	if !isDir(dir) {
		return "", fmt.Errorf("choose an existing project directory")
	}
	return realDirOf(dir), nil
}

func pluginDiscoveryHash(parts ...any) string {
	data, _ := json.Marshal(parts) // strings and PluginStatus values only
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func pluginDiscoveryPath(dir, tool string) string {
	host, _ := os.Hostname()
	return filepath.Join(filepath.Dir(path()), "plugin-discovery", pluginDiscoveryHash(host, dir), tool+".json")
}

// Bind the lookup key to the displayed source, scope, version and part. No
// caller-supplied names or paths go into the on-disk observations.
func pluginDiscoveryTargets(dir string, rows []PluginOrigin) map[string]pluginDiscoveryTarget {
	targets := map[string]pluginDiscoveryTarget{}
	for i := range rows {
		row := &rows[i]
		bind := func(target pluginDiscoveryTarget, name string) {
			if !pluginDiscoveryTool(target.tool) {
				return
			}
			key := pluginDiscoveryHash(dir, target.tool, row.Origin, row.Scope,
				row.Name, row.Source, row.Version, target.kind, name, row.PluginStatus, *target.status)
			if prev, exists := targets[key]; exists {
				// Identical rows cannot be distinguished by a session.
				*prev.key = ""
				return
			}
			*target.key = key
			targets[key] = target
		}
		// Enable declarations and MagPie bundles themselves are not runtime
		// registrations. The bundle's individual resources are.
		if row.Origin == "claude-native" && row.Source != "" ||
			row.Origin == "project" && row.Source == ".claude-plugin/plugin.json" {
			bind(pluginDiscoveryTarget{row.Agent, "plugin", &row.DiscoveryKey, &row.PluginStatus}, row.Name)
		}
		for j := range row.Parts {
			part := &row.Parts[j]
			bind(pluginDiscoveryTarget{part.Agent, part.Kind, &part.DiscoveryKey, &part.PluginStatus}, part.Name)
		}
	}
	for key, target := range targets {
		if *target.key == "" {
			delete(targets, key)
		}
	}
	return targets
}

func pluginDiscoveryTool(tool string) bool {
	return tool == "claude" || tool == "codex" || tool == "pi"
}

func applyPluginDiscovery(dir string, rows []PluginOrigin) {
	targets := pluginDiscoveryTargets(dir, rows)
	for _, tool := range []string{"claude", "codex", "pi"} {
		data, err := os.ReadFile(pluginDiscoveryPath(dir, tool))
		if err != nil {
			continue
		}
		var states map[string]*bool
		if json.Unmarshal(data, &states) != nil {
			continue
		}
		for key, state := range states {
			if target, ok := targets[key]; ok && target.tool == tool {
				target.status.Discovered = state
			}
		}
	}
	for i := range rows {
		row := &rows[i]
		if row.Origin == "codex-mcp" || row.Origin == "pi-package" {
			// These origins each describe a single standalone registration.
			row.Discovered = row.Parts[0].Discovered
		}
	}
}

// RecordPluginDiscovery replaces the last observation for one tool and project.
// It reads either a curated inventory or Claude's system/init evidence on stdin.
func RecordPluginDiscovery(dir string, input io.Reader, claudeStartup bool) error {
	dir, err := pluginDiscoveryProject(dir)
	if err != nil {
		return err
	}
	// Even Claude's startup parser gets only a bounded caller-supplied stream.
	const limit = 2 << 20
	data, err := io.ReadAll(io.LimitReader(input, limit+1))
	if err != nil || len(data) > limit {
		return fmt.Errorf("could not read plugin discovery input (maximum 2 MiB)")
	}
	rows, err := readPluginOriginRows(dir)
	if err != nil {
		return err
	}
	targets := pluginDiscoveryTargets(dir, rows)
	var obs pluginDiscoveryObservation
	if claudeStartup {
		obs, err = claudePluginDiscovery(bytes.NewReader(data), dir, rows)
		if err != nil {
			return err
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if dec.Decode(&obs) != nil || dec.Decode(new(any)) != io.EOF {
			return fmt.Errorf("expected one plugin discovery observation")
		}
	}
	if !filepath.IsAbs(obs.Cwd) || !sameDir(dir, obs.Cwd) || !pluginDiscoveryTool(obs.Tool) {
		return fmt.Errorf("plugin discovery needs the session's project directory and tool (claude, codex or pi)")
	}
	for _, kind := range obs.Complete {
		switch kind {
		case "plugin", "hook", "skill", "subagent", "mcp", "package":
		default:
			return fmt.Errorf("unknown complete plugin discovery kind")
		}
	}
	states := make(map[string]bool, len(obs.Seen)+len(obs.Missing))
	for _, key := range obs.Seen {
		if target, ok := targets[key]; !ok || target.tool != obs.Tool {
			return fmt.Errorf("seen registration does not identify a current source for this project and tool")
		}
		states[key] = true
	}
	for _, key := range obs.Missing {
		target, ok := targets[key]
		if !ok || target.tool != obs.Tool || !slices.Contains(obs.Complete, target.kind) || states[key] {
			return fmt.Errorf("missing registration requires a current source and complete observation of its kind")
		}
		states[key] = false
	}
	out, err := json.Marshal(states)
	if err != nil {
		return err
	}
	return edit.WriteAtomic(pluginDiscoveryPath(dir, obs.Tool), out)
}

func claudePluginDiscovery(r io.Reader, dir string, rows []PluginOrigin) (pluginDiscoveryObservation, error) {
	var report PluginToolProvenance
	obs := pluginDiscoveryObservation{Tool: "claude"}
	if err := readClaudeStartup(r, func(string) bool { return true }, dir, &report); err != nil {
		return obs, err
	}
	obs.Cwd = report.StartupDir
	for _, loaded := range report.Loaded {
		if loaded.Version == "unknown" {
			continue
		}
		var matches []string
		for _, row := range rows {
			if row.Origin == "claude-native" && row.Name == loaded.ID && row.Version == loaded.Version {
				matches = append(matches, row.DiscoveryKey)
			}
		}
		if len(matches) == 1 && matches[0] != "" {
			obs.Seen = append(obs.Seen, matches[0])
		}
	}
	return obs, nil
}
