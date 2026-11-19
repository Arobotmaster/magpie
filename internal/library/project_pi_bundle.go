package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// readPiBundleManifest recognizes Pi's package.json manifest and the pi-package
// keyword used by Pi Marketplace packages that rely on directory discovery.
func readPiBundleManifest(dir string) (map[string]any, error) {
	doc, err := readJSONFile(filepath.Join(dir, "package.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, explicit := doc["pi"].(map[string]any); explicit || slices.Contains(pathsOf(doc["keywords"]), "pi-package") {
		return doc, nil
	}
	return nil, nil
}

// assignProjectPiBundle gives Pi a native package source through the same
// transaction as the rest of the project's bundle assignments. A native
// project declaration already covering that package keeps its own filters,
// version and ownership.
func (l *Library) assignProjectPiBundle(p *Project, b *Plugin) ([]Problem, error) {
	root := pluginDir(b.Name)
	doc, err := readPiBundleManifest(root)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", root, err)
	}
	skip := func(why string) ([]Problem, error) {
		return []Problem{{Agent: "pi", What: "bundle:" + b.Name, Error: why}}, nil
	}
	if doc == nil {
		return skip("the bundle has no Pi package manifest or pi-package keyword")
	}

	stage, err := p.file(piProjectPath(p.Dir))
	if err != nil {
		return nil, err
	}
	config, err := readPiSettings(stage)
	if err != nil {
		return nil, err
	}
	place := piPlace{dir: filepath.Dir(piProjectPath(p.Dir)), home: home()}
	copied := piBundlePackageIdentity(place, root)
	origin := piBundleOriginIdentity(place, b)
	npm := ""
	if name := plStr(doc["name"]); piNameRe.MatchString(name) {
		npm = place.piIdentity("npm:" + name)
	}
	for _, raw := range config.pkgs {
		source, ok := piSourceOf(raw)
		if !ok {
			continue
		}
		id := piBundlePackageIdentity(place, source)
		if id != copied && id != origin && id != npm {
			continue
		}
		if id == copied && slices.ContainsFunc(p.PiEntries, func(e PiEntry) bool {
			return e.Kind == "package" && e.Source == source && e.Raw == piRawOf(raw)
		}) {
			return nil, nil
		}
		return skip(fmt.Sprintf("the project already declares Pi package %s", source))
	}
	if p.Pi == nil {
		p.Pi = map[string][]string{}
	}
	p.Pi["package"] = set(p.Pi["package"], root, true)
	if !slices.ContainsFunc(p.PiEntries, func(e PiEntry) bool { return e.Kind == "package" && e.Source == root }) {
		p.PiEntries = append(p.PiEntries, PiEntry{Kind: "package", Source: root, Scope: ScopeProject})
	}
	return nil, nil
}

func piBundlePackageIdentity(place piPlace, source string) string {
	if piKind(source) == "local" {
		return "local:" + realDirOf(place.resolve(source))
	}
	return place.piIdentity(source)
}

// The original directory and the library copy are the same package. A
// marketplace may contain different plugins, so its root is not a package's
// identity.
func piBundleOriginIdentity(place piPlace, b *Plugin) string {
	input := expand(b.Source)
	if b.Pick != "" || !filepath.IsAbs(input) {
		return ""
	}
	root, _, err := resolvePluginRoot(input)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(root, ".claude-plugin", "marketplace.json")); !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if doc, err := readPiBundleManifest(root); err != nil || doc == nil {
		return ""
	}
	return piBundlePackageIdentity(place, root)
}
