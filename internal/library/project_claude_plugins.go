package library

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tidwall/jsonc"

	"github.com/yetone/magpie/internal/edit"
)

// A project can give itself some of the library's Claude Code plugins: the
// project's own .claude/settings.local.json carries enabledPlugins, which
// Claude Code reads for that folder and which wins a name over the user's
// settings.json (claude_plugins.go carries the user-wide ones). MagPie's
// Claude plugins page is about the user's; a project's file is a different
// one, written here.
//
//	<project>/.claude/settings.local.json   {"enabledPlugins": {"p@m": true}}
//
// The plugin itself is installed through Claude Code's own CLI, as it is
// for the user: magpie writes the project's on/off record and nothing else,
// and takes away only the key it wrote (Project.ClaudePlugins).

// ProjectClaudeSettings is the file, in a project, that carries
// enabledPlugins for it. "" for an agent that isn't Claude Code.
func ProjectClaudeSettings(agentID string) string {
	if agentID != "claude" {
		return ""
	}
	return ".claude/settings.local.json"
}

func projectClaudePath(dir string) string {
	return filepath.Join(dir, filepath.FromSlash(ProjectClaudeSettings("claude")))
}

// readProjectClaude is the project's settings.local.json as this reads and
// writes it: every key kept, so a write here never drops the user's hooks,
// permissions or anything else in that file.
func readProjectClaude(dir string) (map[string]json.RawMessage, error) {
	return readProjectClaudeFile(projectClaudePath(dir))
}

func readProjectClaudeFile(path string) (map[string]json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(jsonc.ToJSON(raw), &top); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return top, nil
}

// projectEnabledPlugins is the project's enabledPlugins map, "" for an
// entry whose value isn't a bool (never written here).
func projectEnabledPlugins(top map[string]json.RawMessage) map[string]bool {
	out := map[string]bool{}
	v, ok := top["enabledPlugins"]
	if !ok || len(bytes.TrimSpace(v)) == 0 {
		return out
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(v, &m) != nil {
		return out
	}
	for k, raw := range m {
		var b bool
		if json.Unmarshal(raw, &b) == nil {
			out[k] = b
		}
	}
	return out
}

// syncProjectClaudePlugins writes the project's Claude plugins into its own
// settings.local.json, and takes away only the keys magpie wrote, whose
// value is still the one magpie wrote, that the project no longer has.
// Everything else in the file stays exactly as it was.
//
// Three sets matter, and p.ClaudeWritten is what tells them apart:
//
//	want  = p.ClaudePlugins   what the project should turn on or off
//	wrote = p.ClaudeWritten   plugin → the value magpie last wrote there
//	have  = the file's enabledPlugins now
//
// An id whose have differs from wrote is the user's: magpie neither rewrites
// nor removes it.
func (l *Library) syncProjectClaudePlugins(p *Project, fail func(what string, err error)) {
	if ProjectClaudeSettings("claude") == "" {
		return
	}
	if len(p.ClaudePlugins) == 0 && len(p.ClaudeWritten) == 0 {
		return
	}
	path, err := p.file(projectClaudePath(p.Dir))
	if err != nil {
		fail("claude-plugin", err)
		return
	}
	top, err := readProjectClaudeFile(path)
	if err != nil {
		fail("claude-plugin", err)
		return
	}
	if top == nil {
		top = map[string]json.RawMessage{}
	}
	_, statErr := os.Stat(path)
	made := p.ClaudeMade || errors.Is(statErr, os.ErrNotExist)
	var enabled map[string]json.RawMessage
	if raw, ok := top["enabledPlugins"]; ok {
		if err := json.Unmarshal(raw, &enabled); err != nil || enabled == nil {
			fail("claude-plugin", fmt.Errorf("%s has an invalid enabledPlugins object", projectClaudePath(p.Dir)))
			return
		}
	} else {
		enabled = map[string]json.RawMessage{}
	}
	known := projectEnabledPlugins(top)
	written := map[string]bool{}
	changed := false
	for id, prior := range p.ClaudeWritten {
		have, present := known[id]
		if !present || have != prior {
			continue
		}
		if want, wanted := p.ClaudePlugins[id]; wanted && want == prior {
			written[id] = prior
			continue
		}
		delete(enabled, id)
		changed = true
	}
	for id, want := range p.ClaudePlugins {
		if _, present := enabled[id]; present {
			continue
		}
		enabled[id], _ = json.Marshal(want)
		written[id] = want
		changed = true
	}
	if !changed {
		p.ClaudeWritten = written
		return
	}
	if len(enabled) == 0 {
		delete(top, "enabledPlugins")
	} else {
		b, err := json.Marshal(enabled)
		if err != nil {
			fail("claude-plugin", err)
			return
		}
		top["enabledPlugins"] = b
	}
	if len(top) == 0 && made {
		// a file that held only magpie's entry goes; one holding anything
		// else (hooks, permissions, the user's own keys) is written back
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fail("claude-plugin", err)
			return
		}
	} else {
		out, err := json.MarshalIndent(top, "", "  ")
		if err != nil {
			fail("claude-plugin", err)
			return
		}
		out = append(out, '\n')
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fail("claude-plugin", err)
			return
		}
		if err := edit.WriteAtomic(path, out); err != nil {
			fail("claude-plugin", err)
			return
		}
	}
	p.ClaudeWritten = written
	p.ClaudeMade = made && len(top) > 0
}

// ProjectClaudePlugin sets whether a project turns a Claude Code plugin on
// or off in its own settings; the plugin itself is installed through
// Claude Code's CLI.
func ProjectClaudePlugin(dir, id string, on bool, options ...ProjectWriteOptions) (*Result, error) {
	if !strings.Contains(id, "@") {
		return nil, fmt.Errorf("%q isn't a plugin id: it looks like name@marketplace", id)
	}
	return changeProject(dir, projectPlugins, func(l *Library) error {
		p := l.project(dir)
		if p == nil {
			return fmt.Errorf("no project %s", dir)
		}
		if on {
			if p.ClaudePlugins == nil {
				p.ClaudePlugins = map[string]bool{}
			}
			p.ClaudePlugins[id] = true
			return nil
		}
		// off is recorded as an explicit false, which Claude Code reads as
		// the project turning it off over the user's settings
		if p.ClaudePlugins == nil {
			p.ClaudePlugins = map[string]bool{}
		}
		p.ClaudePlugins[id] = false
		return nil
	}, options...)
}
