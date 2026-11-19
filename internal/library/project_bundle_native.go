package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/tidwall/gjson"
)

// A native plugin must be installed, enabled in this project, and tied to
// the library bundle by a local path or a catalogue source. Names alone
// are only used to find entries to check.
func projectNativeBundle(p *Project, b *Plugin, id string) (string, error) {
	switch id {
	case "claude":
		return p.claudeNativeBundle(b)
	case "codex":
		return p.codexNativeBundle(b)
	}
	return "", nil
}

func bundleNativeName(b *Plugin, name string) bool {
	return name == b.Name || b.Pick != "" && name == b.Pick
}

func (p *Project) claudeNativeBundle(b *Plugin) (string, error) {
	records, err := readClaudeInstallRecords()
	if err != nil || len(records) == 0 {
		return "", err
	}
	flags := map[string]bool{}
	markets := map[string]gjson.Result{}
	known, err := readClaudeJSON(claudeMarketplaces())
	if err != nil {
		return "", err
	}
	for name, entry := range gjson.ParseBytes(known).Map() {
		markets[name] = entry
	}
	for _, path := range []string{claudeSettings(), filepath.Join(p.Dir, ".claude", "settings.json"), projectClaudePath(p.Dir)} {
		if path != claudeSettings() {
			if err := p.inside(path); err != nil {
				return "", err
			}
		}
		doc, err := readClaudeJSON(path)
		if err != nil {
			return "", err
		}
		for id, enabled := range claudePluginFlags(doc) {
			flags[id] = enabled
		}
		for name, entry := range gjson.GetBytes(doc, "extraKnownMarketplaces").Map() {
			if !markets[name].Get("source").Exists() {
				markets[name] = entry
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(records)) {
		plugin, market, ok := strings.Cut(id, "@")
		if !ok || !flags[id] || !bundleNativeName(b, plugin) {
			continue
		}
		rec := effectiveClaudeBundleRecord(records[id], p.Dir)
		if rec.InstallPath == "" {
			continue
		}
		fi, err := os.Stat(rec.InstallPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !fi.IsDir() {
			continue
		}
		if bundleSourceEqual(b.Source, rec.InstallPath) {
			return id + " (" + rec.Scope + ")", nil
		}
		entry := markets[market]
		root := entry.Get("installLocation").String()
		source := claudeBundleMarketSource(entry.Get("source"))
		if root == "" && filepath.IsAbs(expand(source)) {
			root = expand(source)
		}
		// The known catalogue is normally checked out under this directory.
		if root == "" && nativeBundleSegment(market) {
			root = filepath.Join(claudeDir(), "plugins", "marketplaces", market)
		}
		match, err := bundleMarketMatch(b, plugin, root, source, []string{".claude-plugin/marketplace.json"})
		if err != nil {
			return "", err
		}
		if match {
			return id + " (" + rec.Scope + ")", nil
		}
	}
	return "", nil
}

func effectiveClaudeBundleRecord(records []claudeInstallRecord, dir string) claudeInstallRecord {
	var best claudeInstallRecord
	rank := 0
	for _, rec := range records {
		score := 0
		switch rec.Scope {
		case "user":
			score = 1
		case "project", "local":
			if sameDir(rec.ProjectPath, dir) {
				score = 2
				if rec.Scope == "local" {
					score = 3
				}
			}
		}
		if score > rank {
			best, rank = rec, score
		}
	}
	return best
}

func claudeBundleMarketSource(source gjson.Result) string {
	switch source.Get("source").String() {
	case "directory", "file":
		return source.Get("path").String()
	case "github":
		return source.Get("repo").String()
	case "git", "url":
		return source.Get("url").String()
	}
	return ""
}

type codexBundlePlugin struct {
	Enabled *bool `toml:"enabled"`
}

type codexBundleMarket struct {
	SourceType string `toml:"source_type"`
	Source     string `toml:"source"`
}

type codexBundleConfig struct {
	Plugins      map[string]codexBundlePlugin `toml:"plugins"`
	Marketplaces map[string]codexBundleMarket `toml:"marketplaces"`
	Projects     map[string]struct {
		Trust string `toml:"trust_level"`
	} `toml:"projects"`
}

func readCodexBundleConfig(path string) (cfg codexBundleConfig, err error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func (p *Project) codexNativeBundle(b *Plugin) (string, error) {
	user := codexDir()
	cfg, err := readCodexBundleConfig(filepath.Join(user, "config.toml"))
	if err != nil {
		return "", err
	}
	root := p.Dir
	if git, ok := gitOf(p.Dir); ok {
		root = git.Top
	}
	trusted := cfg.Projects[realDirOf(root)].Trust == "trusted"
	if level := cfg.Projects[realDirOf(p.Dir)].Trust; level != "" {
		trusted = level == "trusted"
	}
	if trusted {
		var folders []string
		for d := p.Dir; ; d = filepath.Dir(d) {
			folders = append(folders, d)
			if d == root {
				break
			}
		}
		slices.Reverse(folders)
		for _, dir := range folders {
			path := filepath.Join(dir, ProjectMCPFile("codex"))
			if err := (&Project{Dir: dir}).inside(path); err != nil {
				return "", err
			}
			project, err := readCodexBundleConfig(path)
			if err != nil {
				return "", err
			}
			for k, v := range project.Plugins {
				if cfg.Plugins == nil {
					cfg.Plugins = map[string]codexBundlePlugin{}
				}
				if v.Enabled != nil {
					cfg.Plugins[k] = v
				} else if _, found := cfg.Plugins[k]; !found {
					cfg.Plugins[k] = v
				}
			}
			for k, v := range project.Marketplaces {
				if cfg.Marketplaces == nil {
					cfg.Marketplaces = map[string]codexBundleMarket{}
				}
				prior := cfg.Marketplaces[k]
				if v.SourceType != "" {
					prior.SourceType = v.SourceType
				}
				if v.Source != "" {
					prior.Source = v.Source
				}
				cfg.Marketplaces[k] = prior
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(cfg.Plugins)) {
		plugin, market, ok := strings.Cut(id, "@")
		if !ok || !nativeBundleSegment(plugin) || !nativeBundleSegment(market) || !bundleNativeName(b, plugin) {
			continue
		}
		if flag := cfg.Plugins[id].Enabled; flag != nil && !*flag {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(user, "plugins", "cache", market, plugin))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !slices.ContainsFunc(entries, func(e os.DirEntry) bool {
			return e.IsDir() && nativeBundleSegment(strings.ReplaceAll(e.Name(), "+", "-"))
		}) {
			continue
		}
		origin := cfg.Marketplaces[market]
		root := filepath.Join(user, ".tmp", "marketplaces", market)
		if origin.SourceType == "local" && filepath.IsAbs(origin.Source) {
			root = origin.Source
		}
		match, err := bundleMarketMatch(b, plugin, root, origin.Source, []string{
			".agents/plugins/marketplace.json", ".agents/plugins/api_marketplace.json", ".claude-plugin/marketplace.json", ".cursor-plugin/marketplace.json",
		})
		if err != nil {
			return "", err
		}
		if match {
			return id, nil
		}
	}
	return "", nil
}

func nativeBundleSegment(s string) bool {
	return s != "" && s != "." && s != ".." && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r))
	}) == -1
}

// bundleMarketMatch checks the named entry in the installed catalogue. The
// library source can name either the catalogue or the plugin's own folder.
func bundleMarketMatch(b *Plugin, name, root, source string, manifests []string) (bool, error) {
	if root == "" {
		return false, nil
	}
	for _, rel := range manifests {
		path := filepath.Join(root, filepath.FromSlash(rel))
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		var doc struct {
			Metadata struct{ PluginRoot string }
			Plugins  []struct {
				Name   string
				Source json.RawMessage
			}
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
		for _, entry := range doc.Plugins {
			if entry.Name != name {
				continue
			}
			local := ""
			var external map[string]any
			if json.Unmarshal(entry.Source, &local) != nil && json.Unmarshal(entry.Source, &external) == nil {
				if plStr(external["source"]) == "local" {
					local = plStr(external["path"])
				}
			}
			if local != "" {
				pluginRel := filepath.Join(doc.Metadata.PluginRoot, local)
				dir, ok := inside(root, pluginRel)
				if !ok {
					return false, nil
				}
				if sameDir(root, pluginMarketplaceDir(b.Name)) && sameDir(dir, filepath.Join(root, b.Name)) {
					return true, nil
				}
				if bundleSourceEqual(b.Source, dir) || sameBundleRepo(b.Source, source, filepath.ToSlash(pluginRel)) {
					return true, nil
				}
			} else if src, why := marketSource(external); why == "" {
				if sameBundleRepo(b.Source, src.Repo, plStr(external["path"])) {
					return true, nil
				}
			}
			return bundleSourceEqual(b.Source, root) || bundleSourceEqual(b.Source, source), nil
		}
		return false, nil
	}
	return false, nil
}

func bundleSourceEqual(a, b string) bool {
	if filepath.IsAbs(expand(a)) && filepath.IsAbs(expand(b)) {
		return sameDir(bundleSourceFolder(a), bundleSourceFolder(b))
	}
	return sameBundleRepo(a, b, "")
}

func bundleSourceFolder(s string) string {
	s = expand(s)
	if filepath.Base(s) == "marketplace.json" || filepath.Base(s) == "plugin.json" {
		s = filepath.Dir(s)
		if strings.HasSuffix(filepath.Base(s), "-plugin") {
			s = filepath.Dir(s)
		} else if filepath.Base(s) == "plugins" && filepath.Base(filepath.Dir(s)) == ".agents" {
			s = filepath.Dir(filepath.Dir(s))
		}
	}
	return s
}

func sameBundleRepo(a, b, subdir string) bool {
	repo := func(s string) (string, string) {
		if src, ok := githubSource(s); ok {
			return strings.ToLower(src.Repo), src.Path
		}
		if src, ok, err := gitSource(s); ok && err == nil {
			return remoteRepo(src.Repo), src.Path
		}
		return "", ""
	}
	ar, ap := repo(a)
	br, bp := repo(b)
	return ar != "" && ar == br && filepath.Clean(ap) == filepath.Join(".", bp, subdir)
}
