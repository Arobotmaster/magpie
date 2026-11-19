package library

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tidwall/jsonc"

	"github.com/yetone/magpie/internal/edit"
)

// A project can be given Pi packages and extensions of its own: Pi reads a
// project's own settings.json beside the folder it runs in, and the
// `packages` and `extensions` there are given to that project alone.
// MagPie's Pi page manages an agent's own settings (piext.go), which is
// user-wide; a project's file is a different one, so it is written here.
//
//	<project>/.pi/settings.json   {"packages": [...], "extensions": [...]}
//
// Pi's own rules decide what it loads (`-<entry>` takes one out, `!<glob>`
// excludes), so what magpie adds is a plain source string, and what it
// takes away is only the entry it wrote, recorded by source and kind
// (Project.PiEntries). An entry the user wrote, or one magpie wrote that
// the user has since changed, is never touched.

// ProjectPiSettings is the file, in a project, that Pi reads packages and
// extensions from: "" for an agent that isn't Pi.
func ProjectPiSettings(agentID string) string {
	if agentID != "pi" {
		return ""
	}
	return ".pi/settings.json"
}

// PiEntry is one packages or extensions entry magpie wrote into a
// project's Pi settings.
type PiEntry struct {
	Source string `json:"source"`
	// Kind is "package" or "extension": which list it went into.
	Kind string `json:"kind"`
	// Raw is the entry exactly as magpie wrote it. An entry still exactly
	// this is magpie's to take away; one the user changed — even to the
	// same source with filters added — is theirs from then on.
	Raw string `json:"raw,omitempty"`
	// Scope is where the entry belongs (PiEntryScope, project_role.go).
	// ScopeProject keeps it in the project's own folder: a worktree of the
	// same repository is an ordinary copy and doesn't load it, which takes
	// the project's .pi/settings.json out of git when every entry there is
	// so scoped. "" is the project's set: Pi loads it in each worktree, as
	// it did before this setting.
	Scope string `json:"scope,omitempty"`
	// An adopted declaration uses Pi's native package filters and keeps
	// its own switch state, independent of a same-named extension.
	Adopted bool `json:"adopted,omitempty"`
	Off     bool `json:"off,omitempty"`
}

// piRawOf is an entry as the canonical form magpie records it in.
func piRawOf(r json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, r) != nil {
		return string(r)
	}
	return b.String()
}

// piProjectConf is a project's .pi/settings.json: the two lists, and every
// other key kept exactly as it was.
type piProjectConf struct {
	top  map[string]json.RawMessage
	pkgs []json.RawMessage
	exts []json.RawMessage
}

func piProjectPath(dir string) string {
	return filepath.Join(dir, filepath.FromSlash(ProjectPiSettings("pi")))
}

func readPiProject(dir string) (piProjectConf, error) {
	return readPiSettings(piProjectPath(dir))
}

func readPiSettings(path string) (piProjectConf, error) {
	raw, err := edit.Read(path)
	if err != nil {
		return piProjectConf{}, err
	}
	return parsePiSettings(path, raw)
}

func (c piProjectConf) entries(kind string) []json.RawMessage {
	if kind == "package" {
		return c.pkgs
	}
	return c.exts
}

func parsePiSettings(path string, raw []byte) (piProjectConf, error) {
	c := piProjectConf{}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(jsonc.ToJSON(raw), &c.top); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if v, ok := c.top["packages"]; ok && len(strings.TrimSpace(string(v))) > 0 {
		if err := json.Unmarshal(v, &c.pkgs); err != nil {
			return c, fmt.Errorf("%s: packages: %w", path, err)
		}
	}
	if v, ok := c.top["extensions"]; ok && len(strings.TrimSpace(string(v))) > 0 {
		if err := json.Unmarshal(v, &c.exts); err != nil {
			return c, fmt.Errorf("%s: extensions: %w", path, err)
		}
	}
	return c, nil
}

// piSourceOf is an entry's source: a bare string, or an object with a
// source (Pi's shape for a package that carries filters).
// piSourceOf is the source an entry names, without Pi's "-" (off) or "+"
// (explicitly on) prefix: those are states of the entry, not part of its
// source, and comparing them as part of it made a turned-off entry look like a
// different one.
func piSourceOf(r json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(r, &s) == nil {
		if len(s) > 1 && (s[0] == '-' || s[0] == '+') {
			return s[1:], true
		}
		return s, strings.TrimSpace(s) != ""
	}
	var o map[string]json.RawMessage
	if json.Unmarshal(r, &o) != nil {
		return "", false
	}
	return piSourceOf(o["source"])
}

func piProjectSources(list []json.RawMessage) []string {
	var out []string
	for _, r := range list {
		if s, ok := piSourceOf(r); ok {
			out = append(out, s)
		}
	}
	return out
}

// syncProjectPi writes the project's Pi packages and extensions into its
// own .pi/settings.json, and takes away those it wrote that the project no
// longer has, leaving every other key and every entry of the user's.
func (l *Library) syncProjectPi(p *Project, fail func(what string, err error)) {
	if ProjectPiSettings("pi") == "" || len(p.Pi) == 0 && len(p.PiEntries) == 0 && !p.PiMade {
		return
	}
	path, err := p.file(piProjectPath(p.Dir))
	if err != nil {
		fail("pi", err)
		return
	}
	conf, err := readPiSettings(path)
	if err != nil {
		fail("pi", err)
		return
	}
	lists := map[string][]json.RawMessage{"package": conf.pkgs, "extension": conf.exts}
	changed := false
	var kept []PiEntry
	for _, kind := range []string{"package", "extension"} {
		cur := lists[kind]
		wanted := p.Pi[kind]
		mine := map[string]PiEntry{}
		scopes := map[string]string{}
		for _, e := range p.PiEntries {
			if e.Kind == kind {
				mine[e.Source] = e
				scopes[e.Source] = e.Scope
			}
		}
		var next []json.RawMessage
		for _, r := range cur {
			src, valid := piSourceOf(r)
			record, owned := mine[src]
			if !valid || !owned {
				next = append(next, r) // the user's: kept as it is
				continue
			}
			// An entry the user changed is theirs from then on, even when
			// its source is still wanted: magpie neither rewrites nor
			// takes it away.
			if record.Raw != "" && record.Raw != piRawOf(r) {
				next = append(next, r)
				continue
			}
			if slices.Contains(wanted, src) {
				if record.Adopted {
					b, err := switchPiEntry(kind, src, r, !record.Off)
					if err != nil {
						fail("pi:"+src, err)
						b = r
					}
					changed = changed || piRawOf(b) != piRawOf(r)
					record.Raw = piRawOf(b)
					next = append(next, b)
					kept = append(kept, record)
					delete(mine, src)
					continue
				}
				if slices.Contains(p.PiOff, src) && !strings.HasPrefix(piRawOf(r), `"-`) {
					// turned off: rewritten as Pi's own "-<source>" entry, which
					// keeps the source listed and loads nothing
					b, err := json.Marshal("-" + src)
					if err == nil {
						next = append(next, b)
						kept = append(kept, PiEntry{Source: src, Kind: kind, Raw: piRawOf(b), Scope: scopes[src]})
						delete(mine, src)
						changed = true
						continue
					}
				}
				if !slices.Contains(p.PiOff, src) && strings.HasPrefix(piRawOf(r), `"-`) {
					// turned back on: the plain source again
					b, err := json.Marshal(src)
					if err == nil {
						next = append(next, b)
						kept = append(kept, PiEntry{Source: src, Kind: kind, Raw: piRawOf(b), Scope: scopes[src]})
						delete(mine, src)
						changed = true
						continue
					}
				}
				next = append(next, r) // still wanted, still as magpie wrote it
				kept = append(kept, PiEntry{Source: src, Kind: kind, Raw: piRawOf(r), Scope: scopes[src]})
				delete(mine, src)
				continue
			}
			changed = true // magpie's, unchanged, and no longer wanted
			delete(mine, src)
		}
		// wanted, and not in the file yet: a source the project has that
		// neither the file nor a record accounts for is written now.
		present := piProjectSources(next)
		// a "-<source>" entry is the source, kept and turned off: it is present,
		// so the loop below must not add a second copy of it
		offPresent := map[string]bool{}
		for _, x := range present {
			if strings.HasPrefix(x, "-") {
				offPresent[strings.TrimPrefix(x, "-")] = true
			}
		}
		for _, src := range wanted {
			if record := mine[src]; record.Adopted {
				// The user removed or edited the declaration after adoption.
				// Drop the assignment so it cannot be recreated or copied.
				p.Pi[kind] = slices.DeleteFunc(slices.Clone(p.Pi[kind]), func(s string) bool { return s == src })
				continue
			}
			if slices.Contains(present, src) || offPresent[src] {
				continue
			}
			// turned off: Pi's own "-<source>" entry, kept in the file, so it
			// stays listed and coming back needs no re-entry
			if slices.Contains(p.PiOff, src) {
				b, err := json.Marshal("-" + src)
				if err != nil {
					fail("pi:"+src, err)
					continue
				}
				next = append(next, b)
				kept = append(kept, PiEntry{Source: src, Kind: kind, Raw: piRawOf(b), Scope: scopes[src]})
				changed = true
				continue
			}
			b, err := json.Marshal(src)
			if err != nil {
				fail("pi:"+src, err)
				continue
			}
			next = append(next, b)
			kept = append(kept, PiEntry{Source: src, Kind: kind, Raw: piRawOf(b), Scope: scopes[src]})
			changed = true
		}
		if kind == "package" {
			conf.pkgs = next
		} else {
			conf.exts = next
		}
	}
	if !changed {
		p.PiEntries = kept
		// the record that keeps the file out of git follows the scopes even
		// when the file itself needs no write
		p.keepPiOutOfGit(kept, fail)
		return
	}
	if conf.top == nil {
		conf.top = map[string]json.RawMessage{}
	}
	// The empty-file check below is about what is left once magpie's entries
	// are set: it must not treat a file magpie is filling as empty.
	for kind, vals := range map[string][]json.RawMessage{"packages": conf.pkgs, "extensions": conf.exts} {
		if len(vals) == 0 {
			delete(conf.top, kind)
			continue
		}
		b, err := json.Marshal(vals)
		if err != nil {
			fail("pi", err)
			continue
		}
		conf.top[kind] = b
	}
	if len(conf.top) == 0 && len(conf.pkgs) == 0 && len(conf.exts) == 0 {
		// A file that held only magpie's entries goes, so a project that
		// had none has none after; a file holding anything else stays.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fail("pi", err)
			return
		}
		p.dropMadeFile(piSettingsRel)
		p.PiMade = false
		p.PiEntries = kept
		return
	}
	out, err := json.MarshalIndent(conf.top, "", "  ")
	if err != nil {
		fail("pi", err)
		return
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fail("pi", err)
		return
	}
	if !exists(path) {
		p.PiMade = true
	}
	if err := edit.WriteAtomic(path, out); err != nil {
		fail("pi", err)
		return
	}
	p.PiEntries = kept
	p.keepPiOutOfGit(kept, fail)
}

// AdoptProjectPi records one existing declaration and its initial scope in
// the same library write. The settings and installed files are already there.
func AdoptProjectPi(dir, kind, source, scope string, options ...ProjectWriteOptions) (*Result, error) {
	if (kind != "package" && kind != "extension") || strings.TrimSpace(source) == "" {
		return nil, fmt.Errorf("give a Pi package or extension source")
	}
	scope, err := scopeNamed(scope)
	if err != nil {
		return nil, err
	}
	return changeProject(dir, 0, func(l *Library) error {
		p := l.project(dir)
		if p == nil {
			return fmt.Errorf("no project %s", dir)
		}
		stage, err := p.file(piProjectPath(dir))
		if err != nil {
			return err
		}
		config, err := readPiSettings(stage)
		if err != nil {
			return err
		}
		var match json.RawMessage
		for _, entry := range config.entries(kind) {
			if name, ok := piSourceOf(entry); ok && name == source {
				if match != nil {
					return fmt.Errorf("%s has multiple Pi %s declarations for %s", dir, kind, source)
				}
				match = entry
			}
		}
		if match == nil {
			return fmt.Errorf("%s has no Pi %s %s", dir, kind, source)
		}
		for _, e := range p.PiEntries {
			if e.Kind == kind && e.Source == source && (e.Raw == "" || e.Raw == piRawOf(match)) {
				if e.Scope == scope {
					return nil
				}
				return fmt.Errorf("%s already manages %s; use project pi scope", dir, source)
			}
		}
		if p.Pi == nil {
			p.Pi = map[string][]string{}
		}
		p.Pi[kind] = set(p.Pi[kind], source, true)
		p.PiEntries = slices.DeleteFunc(p.PiEntries, func(e PiEntry) bool { return e.Kind == kind && e.Source == source })
		p.PiEntries = append(p.PiEntries, PiEntry{
			Kind: kind, Source: source, Raw: piRawOf(match), Scope: scope, Adopted: true, Off: !piEntryOn(kind, match),
		})
		return nil
	}, options...)
}

// PiChange is what ProjectPiChange does with the Pi sources named: turn them on,
// turn them off KEEPING the entry (Pi's own "-<source>"), or remove them.
//
// off and remove are different answers to different questions. "I don't want
// this loading now" must not lose the source, and "take this out of my project"
// must actually take it out. Only the first has a kept state to come back to.
type PiChange int

const (
	PiOn PiChange = iota
	PiOff
	PiRemove
)

// ProjectPi sets which Pi packages or extensions a project has; on false stops
// loading those named and KEEPS the entry, the shape callers had before
// PiRemove. Use ProjectPiChange to take an entry out of the project.
func ProjectPi(dir, kind string, sources []string, on bool) (*Result, error) {
	if on {
		return ProjectPiChange(dir, kind, sources, PiOn)
	}
	return ProjectPiChange(dir, kind, sources, PiOff)
}

// ProjectPiChange turns a project's Pi sources on, off (kept), or removes them.
func ProjectPiChange(dir, kind string, sources []string, mode PiChange, options ...ProjectWriteOptions) (*Result, error) {
	if kind != "package" && kind != "extension" {
		return nil, fmt.Errorf("no Pi kind %q: package or extension", kind)
	}
	if mode == PiOn && len(sources) == 0 {
		return nil, errors.New("no Pi sources given")
	}
	for _, s := range sources {
		if strings.TrimSpace(s) == "" {
			return nil, errors.New("an empty Pi source")
		}
	}
	return changeProject(dir, projectPi, func(l *Library) error {
		p := l.project(dir)
		if p == nil {
			return fmt.Errorf("no project %s", dir)
		}
		// p.Pi says what the project has; p.PiEntries stays what magpie
		// wrote, which sync reads to know which entries are its to take
		// away. Dropping the record here would leave the file's entries
		// looking like the user's, and sync would keep them.
		// off: the sources named stop loading but stay LISTED, as Pi's own
		// "-<source>" entry does, so turning it back on needs no re-entry of
		// the source and the record survives. Every other entry stays.
		// rm: the sources named are truly removed from the project.
		// on: every entry stays, and the sources named are added below.
		var have []string
		for _, src := range p.Pi[kind] {
			have = append(have, src)
		}
		if mode == PiRemove {
			have = slices.DeleteFunc(have, func(x string) bool { return slices.Contains(sources, x) })
		}
		if mode == PiOn {
			for _, s := range sources {
				if !slices.Contains(have, s) {
					have = append(have, s)
				}
			}
		}
		slices.Sort(have)
		if p.Pi == nil {
			p.Pi = map[string][]string{}
		}
		if len(have) == 0 {
			delete(p.Pi, kind)
		} else {
			p.Pi[kind] = have
		}
		// which of them are turned off, kept beside the list
		for _, src := range sources {
			i := slices.IndexFunc(p.PiEntries, func(e PiEntry) bool {
				return e.Adopted && e.Kind == kind && e.Source == src
			})
			if i >= 0 {
				if mode != PiRemove {
					if _, err := switchPiEntry(kind, src, json.RawMessage(p.PiEntries[i].Raw), mode == PiOn); err != nil {
						return err
					}
				}
				p.PiEntries[i].Off = mode == PiOff
			} else {
				p.PiOff = set(p.PiOff, src, mode == PiOff)
			}
		}
		return nil
	}, options...)
}

// renameProjectPi has the projects follow a Pi source's new name.
func (l *Library) renameProjectPi(old, name string) {
	for _, p := range l.Projects {
		for i := range p.PiEntries {
			if p.PiEntries[i].Source == old {
				p.PiEntries[i].Source = name
			}
		}
	}
}
