package library

import (
	"os"
	"path/filepath"
	"testing"
)

// The marketplace entry Claude Code installs a plugin from holds a COPY of the
// plugin, not a link to it. Claude Code resolves a plugin's own module paths
// against the plugin directory it loaded, following links as it goes: reached
// through a link, a hooks module lands on a real path outside that directory
// and Claude Code refuses it ("Path escapes plugin directory"), so the module
// never loads and its hook never runs while the install still says it worked.
func TestPluginMarketplaceEntryIsACopyNotALink(t *testing.T) {
	h := sandbox(t)
	pluginAgents(t, h)
	src := filepath.Join(h, "work", "acme")
	writePlugin(t, src)
	write(t, filepath.Join(src, "scripts/lint.sh"), "#!/bin/sh\necho lint\n")
	if err := os.Chmod(filepath.Join(src, "scripts/lint.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	ok(t)(InstallPlugins(src, nil, []string{"claude"}))

	root, err := pluginMarketplace("acme-kit")
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "acme-kit")

	// the entry is a folder of its own, not a link: Lstat sees a link, and a
	// link is what Claude Code then refuses the module through
	fi, err := os.Lstat(entry)
	if err != nil {
		t.Fatalf("the marketplace entry %s: %v", entry, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("the marketplace entry %s is a link to %s, not a copy", entry, func() string { s, _ := os.Readlink(entry); return s }())
	}
	if !fi.IsDir() {
		t.Fatalf("the marketplace entry %s is not a folder", entry)
	}

	// every file of the plugin is there, at the path the plugin's own hooks
	// name them by ($CLAUDE_PLUGIN_ROOT is this folder)
	for _, p := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", "scripts/lint.sh", "skills/tdd/SKILL.md", ".mcp.json"} {
		if _, err := os.Stat(filepath.Join(entry, filepath.FromSlash(p))); err != nil {
			t.Errorf("the copy is missing %s: %v", p, err)
		}
	}
	// and the real path of the entry is inside the marketplace, which is what
	// Claude Code checks a resolved module against
	real, err := filepath.EvalSymlinks(entry)
	if err != nil {
		t.Fatal(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if !within(real, realRoot) {
		t.Errorf("the entry's real path %s is outside the marketplace %s", real, root)
	}

	// a second install replaces the copy rather than adding beside it, and a
	// copy that went stale is brought up to the plugin's files again
	write(t, filepath.Join(pluginDir("acme-kit"), "scripts/added.sh"), "#!/bin/sh\necho added\n")
	if _, err := pluginMarketplace("acme-kit"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(entry, "scripts/added.sh")); err != nil {
		t.Errorf("the copy was not made again from the plugin's files: %v", err)
	}
	if _, err := os.Stat(entry + ".new"); !os.IsNotExist(err) {
		t.Errorf("a staging folder was left behind: %v", err)
	}
}

// A link an older magpie left is replaced by a copy, not followed: the folder
// it pointed at is the plugin's own, and following it is what broke the module.
func TestPluginMarketplaceReplacesAnOldLink(t *testing.T) {
	h := sandbox(t)
	pluginAgents(t, h)
	src := filepath.Join(h, "work", "acme")
	writePlugin(t, src)
	ok(t)(InstallPlugins(src, nil, []string{"claude"}))

	root := pluginMarketplaceDir("acme-kit")
	entry := filepath.Join(root, "acme-kit")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(entry); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pluginDir("acme-kit"), entry); err != nil {
		t.Fatal(err)
	}

	if _, err := pluginMarketplace("acme-kit"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(entry)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("the old link was kept instead of replaced by a copy")
	}
	// the plugin's own folder is still there: ReplaceAll took the link, not
	// what it pointed at
	if _, err := os.Stat(filepath.Join(pluginDir("acme-kit"), ".claude-plugin/plugin.json")); err != nil {
		t.Errorf("replacing the link took the plugin's own folder with it: %v", err)
	}
}
