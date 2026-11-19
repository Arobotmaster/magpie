package library

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/proc"
)

// PluginProvenance is a local observation. Native installs and a particular
// startup's evidence are recorded separately, even when their versions agree.
type PluginProvenance struct {
	Machine        string                 `json:"machine"`
	Plugin         string                 `json:"plugin"`
	Project        string                 `json:"project,omitempty"`
	LibrarySource  string                 `json:"librarySource"`
	LibraryVersion string                 `json:"libraryVersion"`
	Tools          []PluginToolProvenance `json:"tools"`
}

type PluginToolProvenance struct {
	Tool           string                `json:"tool"`
	NativeStatus   string                `json:"nativeStatus"`
	NativeInstalls []NativePluginVersion `json:"nativeInstalls"`
	LoadStatus     string                `json:"loadStatus"`
	Loaded         []LoadedPluginVersion `json:"loaded"`
	StartupDir     string                `json:"startupDir,omitempty"`
}

type NativePluginVersion struct {
	ID      string `json:"id"`
	Scope   string `json:"scope,omitempty"`
	Project string `json:"project,omitempty"`
	Source  string `json:"source,omitempty"`
	Version string `json:"version"`
}

type LoadedPluginVersion struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// ReadPluginProvenance uses Claude Code's install records, Codex's installed
// plugin listing, and Pi's local package reader. startup, if supplied, is
// a stream-json capture from a single local Claude Code launch.
func ReadPluginProvenance(ctx context.Context, name, project, piSource string, startup io.Reader) (*PluginProvenance, error) {
	l, err := load()
	if err != nil {
		return nil, err
	}
	p := l.plugin(name)
	if p == nil {
		return nil, fmt.Errorf("no library plugin %q", name)
	}
	if project != "" {
		project, err = ProjectPath(project)
		if err != nil {
			return nil, err
		}
	}
	matches := func(s string) bool { return s == p.Name || p.Pick != "" && s == p.Pick }
	machine, _ := os.Hostname()
	report := &PluginProvenance{
		Machine: knownValue(machine), Plugin: p.Name, Project: project,
		LibrarySource: provenanceSource(p.Source), LibraryVersion: knownValue(p.Version),
	}
	claude := toolProvenance("claude", claudeNativeVersions(matches, project))
	if startup != nil {
		if err := readClaudeStartup(startup, matches, project, &claude); err != nil {
			return nil, err
		}
	}
	report.Tools = []PluginToolProvenance{
		claude,
		toolProvenance("codex", codexNativeVersions(ctx, matches)),
		toolProvenance("pi", piNativeVersions(matches, project, piSource)),
	}
	return report, nil
}

func toolProvenance(tool string, installs []NativePluginVersion) PluginToolProvenance {
	row := PluginToolProvenance{Tool: tool, NativeStatus: "unknown", NativeInstalls: []NativePluginVersion{}, LoadStatus: "unknown", Loaded: []LoadedPluginVersion{}}
	if installs != nil {
		row.NativeInstalls = installs
		row.NativeStatus = "not-recorded"
		if len(installs) > 0 {
			row.NativeStatus = "recorded"
		}
	}
	return row
}

func claudeNativeVersions(matches func(string) bool, project string) []NativePluginVersion {
	records, err := readClaudeInstallRecords()
	if err != nil {
		return nil
	}
	out := []NativePluginVersion{}
	for id, recs := range records {
		if name, _, ok := strings.Cut(id, "@"); !ok || !matches(name) {
			continue
		}
		for _, r := range recs {
			if project != "" && r.ProjectPath != "" && !sameDir(r.ProjectPath, project) {
				continue
			}
			if project != "" && r.Scope != "user" && r.ProjectPath == "" {
				continue
			}
			out = append(out, NativePluginVersion{ID: id, Scope: r.Scope, Project: r.ProjectPath, Version: knownValue(r.Version)})
		}
	}
	slices.SortStableFunc(out, func(a, b NativePluginVersion) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func codexNativeVersions(ctx context.Context, matches func(string) bool) []NativePluginVersion {
	bin := proc.FindTool("codex")
	if bin == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := proc.CommandContext(ctx, bin, "plugin", "list", "--json").Output()
	if err != nil {
		return nil
	}
	var list struct {
		Installed []struct {
			ID            string `json:"pluginId"`
			Name, Version string
		}
	}
	if json.Unmarshal(raw, &list) != nil || list.Installed == nil {
		return nil
	}
	out := []NativePluginVersion{}
	for _, p := range list.Installed {
		if p.ID != "" && matches(p.Name) {
			out = append(out, NativePluginVersion{ID: p.ID, Scope: "user", Version: knownValue(p.Version)})
		}
	}
	return out
}

func piNativeVersions(matches func(string) bool, project, source string) []NativePluginVersion {
	p := piPlace{dir: agent.LocalPiDir(), home: home(), roots: piGlobalRoots()}
	settings, err := p.settings()
	if err != nil {
		return nil
	}
	out := []NativePluginVersion{}
	add := func(raw []json.RawMessage, scope string) {
		for _, entry := range raw {
			pkg, ok := piPackageOf(entry)
			if !ok || source != "" && pkg.Source != source || source == "" && !matches(pkg.Name) {
				continue
			}
			if scope == "project" && pkg.Kind == "local" && !filepath.IsAbs(pkg.Source) && !strings.HasPrefix(pkg.Source, "~") {
				pkg.Source = filepath.Join(project, pkg.Source)
			}
			if installed, version := p.installed(pkg); installed {
				out = append(out, NativePluginVersion{ID: pkg.Name, Scope: scope, Source: provenanceSource(pkg.Source), Version: knownValue(version)})
			}
		}
	}
	add(settings.pkgs, "user")
	if project != "" {
		s, err := readPiProject(project)
		if err != nil {
			return nil
		}
		p.dir = filepath.Join(project, ".pi")
		add(s.pkgs, "project")
	}
	return out
}

func readClaudeStartup(r io.Reader, matches func(string) bool, project string, row *PluginToolProvenance) error {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 64<<10), 2<<20)
	for scan.Scan() {
		var event struct {
			Type, Subtype, Cwd string
			Plugins            []struct{ Name, Source, Version string }
		}
		if json.Unmarshal(scan.Bytes(), &event) != nil || event.Type != "system" || event.Subtype != "init" {
			continue
		}
		if event.Cwd == "" || project != "" && !sameDir(event.Cwd, project) {
			return fmt.Errorf("Claude startup evidence belongs to a different project or has no working directory")
		}
		row.StartupDir = event.Cwd
		for _, p := range event.Plugins {
			if matches(p.Name) {
				row.Loaded = append(row.Loaded, LoadedPluginVersion{ID: provenanceSource(p.Source), Version: knownValue(p.Version)})
			}
		}
		if len(row.Loaded) > 0 {
			row.LoadStatus = "observed"
		}
		return nil
	}
	if err := scan.Err(); err != nil {
		return fmt.Errorf("read Claude startup evidence: %w", err)
	}
	return fmt.Errorf("Claude startup evidence has no system/init event")
}

func knownValue(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func provenanceSource(s string) string {
	raw, git := strings.CutPrefix(s, "git:")
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		u.User, u.RawQuery, u.Fragment, u.ForceQuery = nil, "", "", false
		if git {
			return "git:" + u.String()
		}
		return u.String()
	}
	if userinfo.Regexp().MatchString(raw) {
		return "unknown"
	}
	return knownValue(s)
}
