package library

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/edit"
)

// A project can be given library hooks (#6874), each for some agents, as
// entries of the project's own: the file each agent reads a project's hooks
// from, in the shape its user-wide one has (hooks.go). Only an agent whose
// docs name a project hooks file is listed:
//
//	Claude Code  <project>/.claude/settings.local.json  hooks.<Event>   code.claude.com/docs/en/hooks
//	Codex        <project>/.codex/hooks.json            hooks.<Event>   codex-rs/hooks
//
// The user-wide shape is the project shape for both: Claude Code merges a
// project's settings.local.json hooks over its own, and Codex reads a
// project's .codex/hooks.json beside its config.toml. An entry is recorded
// by its canonical form (Project.HookFiles), as a project's subagents are:
// magpie writes over or takes away only an entry still exactly as it wrote
// it, and an entry the user changed is theirs from then on.
//
// Nothing is written outside the project's folder, so the agents' own
// (user-wide) hooks stay as they are. syncHooks (hooks.go) owns the
// user-wide file; this owns the project's.

// projectHookFiles is, per agent, the file in a project that agent reads
// its own hooks from.
var projectHookFiles = map[string]string{
	"claude": ".claude/settings.local.json",
	"codex":  ".codex/hooks.json",
}

// ProjectHooksFile is the file, in a project, an agent reads its hooks
// from: "" for one magpie can't give a project's hooks to.
func ProjectHooksFile(agentID string) string { return projectHookFiles[agentID] }

// ProjectHookAgents are the agents a project can be given hooks for, in a
// stable order.
func ProjectHookAgents() []string {
	out := make([]string, 0, len(projectHookFiles))
	for id := range projectHookFiles {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// projectHookSpec is how agent id keeps its hooks inside project p: the
// user-wide spec (hookSpecFor) with its file moved into the project.
func projectHookSpec(p *Project, id string) *hookSpec {
	sp, _ := hookSpecOf(&Target{Agent: &agent.Agent{ID: id}})
	if sp == nil || sp.toml {
		// Grok keeps its hooks in a config.toml, which a project's own
		// hooks file isn't: this feature is the JSON pair above.
		return nil
	}
	rel, ok := projectHookFiles[id]
	if !ok {
		return nil
	}
	cp := *sp
	cp.path = filepath.Join(p.Dir, filepath.FromSlash(rel))
	return &cp
}

// projectHookHash is what a written entry is recorded as: the same
// canonical form the entry is compared in when it is taken away again.
func projectHookHash(entry json.RawMessage) string { return canonical(entry) }

// projectHookWant is one entry that belongs in a project's hooks file.
type projectHookWant struct {
	h     *Hook
	ev    string
	entry json.RawMessage
}

// projectHookKey identifies one written entry in one file.
func projectHookKey(rel, ev, hash string) string { return rel + "\x00" + ev + "\x00" + hash }

// syncProjectHooks writes the library's hooks the project has into each
// agent's file there, and takes away those it wrote that it no longer has.
func (l *Library) syncProjectHooks(p *Project, fail func(what string, err error)) {
	// rel → event → the entries that belong there
	wanted := map[string]map[string][]projectHookWant{}
	for _, name := range slices.Sorted(maps.Keys(p.Hooks)) {
		h := l.hook(name)
		if h == nil {
			continue
		}
		for _, id := range p.Hooks[name] {
			sp := projectHookSpec(p, id)
			if sp == nil {
				continue
			}
			ev, err := sp.event(id, h)
			if err == nil && h.Event == "session-start" {
				_, err = startMatcher(id, h.Matcher)
			}
			if err != nil {
				fail("hook:"+h.Name, fmt.Errorf("%s: %w", id, err))
				continue
			}
			rel := projectHookFiles[id]
			if wanted[rel] == nil {
				wanted[rel] = map[string][]projectHookWant{}
			}
			wanted[rel][ev] = append(wanted[rel][ev], projectHookWant{h, ev, sp.entry(h, ev)})
		}
	}

	// take away what is magpie's and no longer wanted
	var kept []HookFile
	for _, f := range p.HookFiles {
		abs := filepath.Join(p.Dir, filepath.FromSlash(f.Path))
		dst, err := p.file(abs)
		if err != nil {
			fail("hook:"+f.Name, err)
			kept = append(kept, f)
			continue
		}
		doc, err := readHooksFile(dst)
		if errors.Is(err, os.ErrNotExist) {
			continue // gone: written again below if it is still wanted
		}
		if err != nil {
			fail("hook:"+f.Name, err)
			kept = append(kept, f)
			continue
		}
		list, err := hookList(doc, abs, f.Event)
		if err != nil {
			fail("hook:"+f.Name, err)
			kept = append(kept, f)
			continue
		}
		// Still magpie's only while exactly what magpie wrote.
		idx := slices.IndexFunc(list, func(e json.RawMessage) bool { return canonical(e) == f.Hash })
		if idx < 0 {
			continue // the user's from then on
		}
		if projectHookWanted(wanted[f.Path][f.Event], f.Hash) {
			kept = append(kept, f)
			continue
		}
		if err := writeHookList(dst, doc, f.Event, slices.Delete(slices.Clone(list), idx, idx+1)); err != nil {
			fail("hook:"+f.Name, err)
			kept = append(kept, f)
		}
	}

	// add what belongs there and isn't yet
	for _, rel := range slices.Sorted(maps.Keys(wanted)) {
		id := projectHookAgentOf(rel)
		abs := filepath.Join(p.Dir, filepath.FromSlash(rel))
		dst, err := p.file(abs)
		if err != nil {
			for _, w := range flattenHookWants(wanted[rel]) {
				fail("hook:"+w.h.Name, err)
			}
			continue
		}
		doc, err := readHooksFile(dst)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			for _, w := range flattenHookWants(wanted[rel]) {
				fail("hook:"+w.h.Name, err)
			}
			continue
		}
		native := p.nativeHooks(id)
		for _, ev := range slices.Sorted(maps.Keys(wanted[rel])) {
			list, err := hookList(doc, abs, ev)
			if err != nil {
				for _, w := range wanted[rel][ev] {
					fail("hook:"+w.h.Name, err)
				}
				continue
			}
			next := list
			var added []HookFile
			for _, w := range wanted[rel][ev] {
				hash := projectHookHash(w.entry)
				if slices.ContainsFunc(kept, func(f HookFile) bool {
					return f.Path == rel && f.Event == ev && f.Hash == hash && f.Name == w.h.Name
				}) {
					continue
				}
				command := strings.TrimSpace(w.h.Command)
				if hasNativeHook(native, ev, command) || slices.ContainsFunc(next, func(e json.RawMessage) bool {
					return slices.ContainsFunc(projectHookCommands(e), func(h projectHookCommand) bool {
						return h.Command != "" && strings.TrimSpace(h.Command) == command
					})
				}) {
					fail("hook:"+w.h.Name, fmt.Errorf("%w %s (%s %s)", errProjectAlreadyHas, w.h.Name, id, ev))
					continue
				}
				next = append(next, w.entry)
				added = append(added, HookFile{Name: w.h.Name, Path: rel, Event: ev, Hash: hash})
			}
			if len(added) == 0 {
				continue
			}
			if err := writeHookList(dst, doc, ev, next); err != nil {
				for _, w := range wanted[rel][ev] {
					fail("hook:"+w.h.Name, err)
				}
				continue
			}
			kept = append(kept, added...)
			// re-read so the next event of this file builds on it
			if doc, err = readHooksFile(dst); err != nil {
				for _, w := range wanted[rel][ev] {
					fail("hook:"+w.h.Name, err)
				}
			}
		}
	}
	slices.SortFunc(kept, func(x, y HookFile) int {
		if c := strings.Compare(x.Path, y.Path); c != 0 {
			return c
		}
		if c := strings.Compare(x.Event, y.Event); c != 0 {
			return c
		}
		return strings.Compare(x.Hash, y.Hash)
	})
	p.HookFiles = kept
}

func projectHookWanted(wants []projectHookWant, hash string) bool {
	return slices.ContainsFunc(wants, func(w projectHookWant) bool { return projectHookHash(w.entry) == hash })
}

func projectHookAgentOf(rel string) string {
	for id, r := range projectHookFiles {
		if r == rel {
			return id
		}
	}
	return ""
}

func flattenHookWants(m map[string][]projectHookWant) []projectHookWant {
	var out []projectHookWant
	for _, ev := range slices.Sorted(maps.Keys(m)) {
		out = append(out, m[ev]...)
	}
	return out
}

// writeHookList writes doc back with ev's list replaced by entries; an
// empty list takes the event out, and a hooks object left with nothing goes
// too, as does a file left holding nothing but magpie's empty document.
func writeHookList(path string, doc []byte, ev string, entries []json.RawMessage) error {
	if len(bytes.TrimSpace(doc)) == 0 {
		doc = []byte("{}")
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(doc, &top); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var hooks map[string]json.RawMessage
	if raw, ok := top["hooks"]; ok && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return fmt.Errorf("%s: hooks: %w", path, err)
		}
	}
	if hooks == nil {
		hooks = map[string]json.RawMessage{}
	}
	if len(entries) == 0 {
		delete(hooks, ev)
	} else {
		b, err := encodeHookList(entries)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", path, ev, err)
		}
		hooks[ev] = b
	}
	if len(hooks) == 0 {
		delete(top, "hooks")
	} else {
		b, err := json.Marshal(hooks)
		if err != nil {
			return err
		}
		top["hooks"] = b
	}
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	// A file magpie made and then emptied is taken away again, so a project
	// that had no hooks file before has none after. A file still holding
	// anything else is written back, never removed.
	if string(out) == "{}\n" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return edit.WriteAtomic(path, out)
}

// encodeHookList is entries as the event's array: each kept exactly as it
// was read or built, so a field an agent it isn't for keeps there survives.
func encodeHookList(entries []json.RawMessage) (json.RawMessage, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, e := range entries {
		if i > 0 {
			buf.WriteByte(',')
		}
		// compact, so a hand-formatted entry is stable across writes and
		// compares equal to what magpie wrote (canonical does the same)
		var tmp bytes.Buffer
		if err := json.Compact(&tmp, e); err != nil {
			return nil, err
		}
		buf.Write(tmp.Bytes())
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

// ProjectHooks sets which agents get each of some library hooks in a
// project; none takes them out of it.
func ProjectHooks(dir string, names, agents []string, options ...ProjectWriteOptions) (*Result, error) {
	var skipped []Problem
	res, err := changeProject(dir, projectHooks, func(l *Library) error {
		p := l.project(dir)
		if p == nil {
			return fmt.Errorf("no project %s", dir)
		}
		if len(names) == 0 {
			return errors.New("no hooks given")
		}
		for _, n := range names {
			if l.hook(n) == nil {
				return fmt.Errorf("no hook called %s", n)
			}
		}
		assign := map[string][]string{}
		for _, id := range agents {
			if ProjectHooksFile(id) == "" {
				return fmt.Errorf("magpie knows of no folder %s reads a project's hooks from", id)
			}
			// An agent that cannot carry a hook's event must be refused HERE, as
			// the write itself refuses it, so the command does not report success
			// for something no file will hold. syncProjectHooks already skips such
			// a pair and says why, but only on a later sync, which also exits 0:
			// `hook add <proj> <claude-only hook> agents=codex` answered "✓ … →
			// codex", recorded the assignment, wrote no file, and failed only as a
			// note later (#6.1 in the WSL report). Same class as the stale
			// confirmation line: the answer has to come from the check the write
			// uses, not from the record it just made.
			sp := projectHookSpec(p, id)
			if sp == nil {
				return fmt.Errorf("%s reads no project hooks file magpie can write", id)
			}
			native := p.nativeHooks(id)
			for _, n := range names {
				h := l.hook(n)
				if h == nil {
					continue
				}
				ev, err := sp.event(id, h)
				if err == nil && h.Event == "session-start" {
					_, err = startMatcher(id, h.Matcher)
				}
				if err != nil {
					return fmt.Errorf("%s can't take %s: %w", id, n, err)
				}
				if hasNativeHook(native, ev, h.Command) {
					skipped = append(skipped, projectSkip(p, "hook:"+n, n, id+" "+ev))
					continue
				}
				assign[n] = set(assign[n], id, true)
			}
		}
		for _, n := range names {
			if p.Hooks == nil {
				p.Hooks = map[string][]string{}
			}
			// An empty assignment KEEPS the record: "in this project, for no
			// agent" is a state the user can come back from, and deleting the
			// key made `project hook agents <dir> <hook> claude` answer "has no
			// hook <hook>" afterwards. The sync writes nothing for an empty list,
			// so the project's files end up exactly as before.
			p.Hooks[n] = assign[n]
		}
		return nil
	}, options...)
	return withProjectSkips(res, skipped), err
}

func hasNativeHook(rows []ProjectResource, event, command string) bool {
	command = strings.TrimSpace(command)
	return slices.ContainsFunc(rows, func(r ProjectResource) bool {
		return r.Event == event && strings.TrimSpace(r.Name) == command
	})
}

// renameProjectHooks has the projects give a hook its new name.
func (l *Library) renameProjectHooks(old, name string) {
	for _, p := range l.Projects {
		if a, ok := p.Hooks[old]; ok {
			delete(p.Hooks, old)
			p.Hooks[name] = a
		}
	}
}

// removeProjectHook takes a removed hook out of every project that had it.
func (l *Library) removeProjectHook(name string) {
	for _, p := range l.Projects {
		delete(p.Hooks, name)
	}
}
