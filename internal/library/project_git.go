package library

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/proc"
)

// A project in a git repository keeps what magpie places in it out of git
// through the repository's own info/exclude, which git reads beside the
// .gitignore and which is never committed (gitignore(5)): nothing of
// magpie's goes into the project's history, its .gitignore included. The
// file is shared by the repository's worktrees, each reading it against
// its own top folder, so one block of lines keeps a project's entries out
// of git in every worktree.

// gitWhere is where the project's repository is: Top, the worktree's top
// folder the project is in; Common, the repository's .git folder that
// every worktree shares (info/exclude, worktrees/).
type gitWhere struct {
	Top, Common string
}

// gitOf is the repository dir is in, by the nearest .git above it: a
// folder (the main worktree's), or a file naming the worktree's own
// folder under the main .git (git worktree add), whose commondir names
// the shared one. ok is false outside a repository.
func gitOf(dir string) (gitWhere, bool) {
	for d := dir; ; {
		g := filepath.Join(d, ".git")
		if fi, err := os.Stat(g); err == nil {
			if fi.IsDir() {
				return gitWhere{Top: d, Common: g}, true
			}
			if own, ok := gitdirOf(g); ok {
				common := own
				if b, err := os.ReadFile(filepath.Join(own, "commondir")); err == nil {
					c := strings.TrimSpace(string(b))
					if !filepath.IsAbs(c) {
						c = filepath.Join(own, c)
					}
					common = filepath.Clean(c)
				}
				return gitWhere{Top: d, Common: common}, true
			}
		}
		up := filepath.Dir(d)
		if up == d {
			return gitWhere{}, false
		}
		d = up
	}
}

// gitdirOf is the folder a .git file names ("gitdir: <path>").
func gitdirOf(file string) (string, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	g, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return "", false
	}
	g = strings.TrimSpace(g)
	if !filepath.IsAbs(g) {
		g = filepath.Join(filepath.Dir(file), g)
	}
	return filepath.Clean(g), true
}

// mainTop is the main worktree's top folder: the folder the shared .git is
// in. "" for a bare repository's worktrees, which have no main one.
func (g gitWhere) mainTop() string {
	if filepath.Base(g.Common) != ".git" {
		return ""
	}
	return filepath.Dir(g.Common)
}

// tops are the top folders of every worktree of the repository that is on
// disk, the main one first, as git lists them in <common>/worktrees/*/gitdir.
func (g gitWhere) tops() []string {
	var out []string
	if m := g.mainTop(); m != "" {
		out = append(out, m)
	}
	es, _ := os.ReadDir(filepath.Join(g.Common, "worktrees"))
	var more []string
	for _, e := range es {
		b, err := os.ReadFile(filepath.Join(g.Common, "worktrees", e.Name(), "gitdir"))
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(b))
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(g.Common, "worktrees", e.Name(), p)
		}
		top := filepath.Dir(filepath.Clean(p))
		if fi, err := os.Stat(top); err == nil && fi.IsDir() && !slices.Contains(out, top) && !slices.Contains(more, top) {
			more = append(more, top)
		}
	}
	sort.Strings(more)
	return append(out, more...)
}

// treeDirs are the project's folder in each other worktree of its
// repository that has it: the same folder under each worktree's top.
func (p *Project) treeDirs() []string {
	g, ok := gitOf(p.Dir)
	if !ok {
		return nil
	}
	rel, err := filepath.Rel(realDir(g.Top), realDir(p.Dir))
	if err != nil {
		return nil
	}
	var out []string
	for _, top := range g.tops() {
		if top == g.Top || realDir(top) == realDir(g.Top) {
			continue
		}
		d := filepath.Join(top, rel)
		other, err := projectRepository(realDir(d))
		if err != nil || other.Top == "" || realDir(other.Common) != realDir(g.Common) || realDir(other.Top) != realDir(top) ||
			realDir(d) != filepath.Join(realDir(top), rel) {
			continue
		}
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// projectKey is the short name a project is known to the router by: the
// hub's address for its agents ends in it (HubProjectSep).
func projectKey(dir string) string {
	h := sha256.Sum256([]byte(filepath.Clean(dir)))
	return hex.EncodeToString(h[:6])
}

// excludeBegin is the first of the lines magpie keeps a project's entries
// between in info/exclude, one block per project (several can share a
// repository); ignoreEnd ends it.
func excludeBegin(dir string) string {
	return "# >>> magpie: library items placed in " + filepath.ToSlash(dir) + " (magpie adds and removes these lines) >>>"
}

// writeExclude keeps the repository's info/exclude listing the entries
// magpie placed in the project, relative to its worktree's top, between
// the project's two lines; every other line is git's or the user's.
func (p *Project) writeExclude(g gitWhere, entries []string) error {
	f := filepath.Join(realBelow(filepath.Dir(g.Common)), filepath.Base(g.Common), "info", "exclude")
	if p.writes != nil {
		// The worktree's .git entry names where Git itself reads this
		// metadata. Treat a link inside that directory as foreign.
		var err error
		f, err = p.writes.localFile(f)
		if err != nil {
			return err
		}
	}
	b, err := os.ReadFile(f)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	rel, err := filepath.Rel(realDir(g.Top), realDir(p.Dir))
	if err != nil {
		return err
	}
	pre := "/"
	if rel = filepath.ToSlash(rel); rel != "." {
		pre += rel + "/"
	}
	nl := "\n"
	if strings.Contains(string(b), "\r\n") {
		nl = "\r\n"
	}
	begin := excludeBegin(p.Dir)
	original := strings.ReplaceAll(string(b), "\r\n", "\n")
	rest := stripBlock(original, begin)
	out := rest
	if len(entries) > 0 {
		sort.Strings(entries)
		var block strings.Builder
		block.WriteString(begin + "\n")
		for _, e := range slices.Compact(entries) {
			block.WriteString(pre + e + "\n")
		}
		block.WriteString(ignoreEnd + "\n")
		if strings.Contains(original, block.String()) {
			return nil
		}
		if out != "" {
			out += "\n"
		}
		out += block.String()
	} else if !strings.Contains(original, begin+"\n") {
		return nil
	}
	out = strings.ReplaceAll(out, "\n", nl)
	if out == string(b) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	return edit.WriteAtomic(f, []byte(out))
}

// stripBlock is s without the lines from begin to ignoreEnd, and the blank
// line left before them.
func stripBlock(s, begin string) string {
	var out []string
	in := false
	for _, line := range strings.Split(s, "\n") {
		switch t := strings.TrimSpace(line); {
		case t == begin:
			in = true
		case in && t == ignoreEnd:
			in = false
		case !in:
			out = append(out, line)
		}
	}
	r := strings.TrimRight(strings.Join(out, "\n"), "\n")
	if r != "" {
		r += "\n"
	}
	return r
}

// projectGit combines the index and HEAD, so staged and unstaged deletions
// both stay deleted. Directories are included by checking path prefixes.
type projectGit struct {
	where   gitWhere
	tracked map[string]string
	folders map[string]bool
	err     error
}

func (w *projectWrites) repository(root string) *projectGit {
	if g := w.git[root]; g != nil {
		return g
	}
	g := &projectGit{}
	w.git[root] = g
	g.where, g.err = projectRepository(root)
	if g.err != nil || g.where.Top == "" {
		return g
	}
	run := func(args ...string) ([]byte, error) {
		cmd := proc.Command("git", append([]string{"--no-optional-locks", "-C", root}, args...)...)
		// The caller may itself be a Git hook with repository overrides.
		cmd.Env = slices.DeleteFunc(os.Environ(), func(v string) bool { return strings.HasPrefix(v, "GIT_") })
		return cmd.Output()
	}
	index, err := run("ls-files", "-z", "--full-name", "--", ":/")
	if err != nil {
		g.err = fmt.Errorf("%s: cannot read the Git index: %w", root, err)
		return g
	}
	entries := strings.Split(string(index), "\x00")
	head, err := run("rev-parse", "--verify", "--quiet", "HEAD")
	if err == nil {
		committed, err := run("ls-tree", "-rz", "--name-only", strings.TrimSpace(string(head)))
		if err != nil {
			g.err = fmt.Errorf("%s: cannot read Git HEAD: %w", root, err)
			return g
		}
		entries = append(entries, strings.Split(string(committed), "\x00")...)
	} else if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 1 {
		g.err = fmt.Errorf("%s: cannot resolve Git HEAD: %w", root, err)
	}
	g.tracked, g.folders = map[string]string{}, map[string]bool{}
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		key := strings.ToLower(entry)
		g.tracked[key] = entry
		for top := filepath.Dir(key); top != "."; top = filepath.Dir(top) {
			g.folders[filepath.ToSlash(top)] = true
		}
	}
	return g
}

// projectRepository confirms the nearest Git administrative entry
// belongs to the selected root. A Git worktree's record points back to
// its own .git file, so a hand-edited gitfile can't claim a different repo.
func projectRepository(root string) (gitWhere, error) {
	for dir := root; ; dir = filepath.Dir(dir) {
		admin := filepath.Join(dir, ".git")
		fi, err := os.Lstat(admin)
		if err == nil {
			g, ok := gitOf(root)
			if !ok || g.Top != dir || linkEntry(fi) {
				return gitWhere{}, fmt.Errorf("%s: Git repository boundary is unclear", admin)
			}
			if fi.IsDir() {
				return g, nil
			}
			own, ok := gitdirOf(admin)
			back, err := os.ReadFile(filepath.Join(own, "gitdir"))
			dst := strings.TrimSpace(string(back))
			if !filepath.IsAbs(dst) {
				dst = filepath.Join(own, dst)
			}
			if !ok || err != nil || realBelow(dst) != admin ||
				filepath.Dir(realDir(own)) != filepath.Join(realDir(g.Common), "worktrees") {
				return gitWhere{}, fmt.Errorf("%s: Git worktree binding could not be verified", admin)
			}
			return g, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return gitWhere{}, err
		}
		if dir == filepath.Dir(dir) {
			return gitWhere{}, nil
		}
	}
}

func (p *Project) writePath(abs string, skill bool) (string, error) {
	root := realDir(p.Dir)
	if !filepath.IsAbs(root) || abs == p.Dir || !within(abs, p.Dir) {
		return "", fmt.Errorf("%s is outside the project", abs)
	}
	anchor := abs
	if skill {
		anchor = filepath.Dir(abs) // magpie's own skill link is an entry
	}
	if err := p.inside(anchor); err != nil {
		return "", err
	}
	real := realBelow(anchor)
	if skill {
		real = filepath.Join(real, filepath.Base(abs))
	}
	g := p.writes.repository(root)
	if g.err != nil {
		return "", g.err
	}
	// Reject nested repositories (including gitfiles and submodules) and
	// their administration paths on both the typed and resolved routes.
	for _, target := range []string{abs, real} {
		base := p.Dir
		if target == real {
			base = root
		}
		for at := target; at != base; at = filepath.Dir(at) {
			if !within(at, base) || strings.EqualFold(filepath.Base(at), ".git") {
				return "", fmt.Errorf("%s crosses a Git repository boundary", abs)
			}
			if _, err := os.Lstat(filepath.Join(at, ".git")); err == nil {
				return "", fmt.Errorf("%s crosses a Git repository boundary", abs)
			} else if !errors.Is(err, fs.ErrNotExist) {
				// A regular file and an owned skill link cannot contain .git.
				if fi, statErr := os.Lstat(at); statErr != nil || fi.IsDir() {
					return "", err
				}
			}
		}
	}
	if g.where.Top != "" {
		for _, target := range []string{filepath.Join(root, filepath.FromSlash(p.rel(abs))), real} {
			rel, err := filepath.Rel(g.where.Top, target)
			if err != nil {
				return "", err
			}
			rel = strings.ToLower(filepath.ToSlash(rel))
			for top := filepath.Dir(rel); top != "." && top != filepath.Dir(top); top = filepath.Dir(top) {
				if tracked := g.tracked[filepath.ToSlash(top)]; tracked != "" {
					return "", fmt.Errorf("%s is beneath Git-tracked path %s", abs, tracked)
				}
			}
			if g.tracked[rel] == "" && !g.folders[rel] {
				continue
			}
			fi, err := os.Lstat(target)
			switch {
			case errors.Is(err, os.ErrNotExist):
				return "", fmt.Errorf("%s is a deleted Git-tracked path", target)
			case err != nil:
				return "", err
			case skill || fi.IsDir():
				return "", fmt.Errorf("%s contains Git-tracked content", target)
			}
		}
	}
	return real, nil
}
