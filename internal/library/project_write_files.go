package library

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/yetone/magpie/internal/edit"
)

type projectFile struct {
	path, stage string
	before      []byte
	existed     bool
	mode        fs.FileMode
	check       func() error
}

type projectWrite struct {
	ProjectWrite
	fingerprint string
	check       func() error
	apply       func() (projectUndo, error)
}

type projectUndo struct {
	rollback func() error
	finish   func()
}

type projectWrites struct {
	dir   string
	files map[string]*projectFile
	other map[string]projectWrite
	git   map[string]*projectGit
}

func newProjectWrites() (*projectWrites, error) {
	dir, err := os.MkdirTemp("", "magpie-project-plan-")
	if err != nil {
		return nil, err
	}
	return &projectWrites{dir, map[string]*projectFile{}, map[string]projectWrite{}, map[string]*projectGit{}}, nil
}

func (w *projectWrites) close() { _ = os.RemoveAll(w.dir) }

// file gives the existing editors a private copy. Multiple editors of one
// config see each other's changes. Only the final bytes reach the project.
func (p *Project) file(abs string) (string, error) {
	if p.writes == nil {
		return abs, p.inside(abs)
	}
	if fi, err := os.Lstat(abs); err == nil && linkEntry(fi) {
		return "", fmt.Errorf("%s is a symbolic link", abs)
	}
	real, err := p.writePath(abs, false)
	if err != nil {
		return "", err
	}
	return p.writes.stage(real, func() error {
		if fi, err := os.Lstat(abs); err == nil && linkEntry(fi) {
			return fmt.Errorf("%s became a symbolic link", abs)
		}
		to, err := p.writePath(abs, false)
		if err == nil && to != real {
			err = fmt.Errorf("%s changed its destination", abs)
		}
		return err
	})
}

// localFile stages an explicitly named metadata file. It must be at its
// own path: a symlink must never redirect repository or agent metadata.
func (w *projectWrites) localFile(path string) (string, error) {
	check := func() error {
		if realBelow(path) != path {
			return fmt.Errorf("%s is a redirected metadata file", path)
		}
		return nil
	}
	if err := check(); err != nil {
		return "", err
	}
	return w.stage(path, check)
}

func (w *projectWrites) stage(path string, check func() error) (string, error) {
	if f := w.files[path]; f != nil {
		return f.stage, nil
	}
	f := &projectFile{path: path, stage: filepath.Join(w.dir, strconv.Itoa(len(w.files)), filepath.Base(path)), check: check}
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return "", err
	case !fi.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a regular file", path)
	case edit.HasHardLinks(path):
		return "", fmt.Errorf("%s is a hard-linked file", path)
	default:
		f.existed = true
		f.mode = fi.Mode().Perm()
		f.before, err = os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if err := edit.WriteAtomic(f.stage, f.before); err != nil {
			return "", err
		}
	}
	w.files[path] = f
	return f.stage, nil
}

func (w *projectWrites) operations(state string) ([]projectWrite, error) {
	out := make([]projectWrite, 0, len(w.files)+len(w.other))
	for _, f := range w.files {
		after, err := os.ReadFile(f.stage)
		exists := err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if exists == f.existed && bytes.Equal(after, f.before) {
			continue
		}
		action := "write"
		if !exists {
			action = "remove"
		}
		out = append(out, projectWrite{
			ProjectWrite: ProjectWrite{Path: f.path, Action: action},
			fingerprint:  fmt.Sprintf("%t:%x:%x", f.existed, sha256.Sum256(f.before), sha256.Sum256(after)),
			check: func() error {
				if err := f.check(); err != nil {
					return err
				}
				if edit.HasHardLinks(f.path) {
					return fmt.Errorf("%s is a hard-linked file", f.path)
				}
				b, err := os.ReadFile(f.path)
				if (err == nil) != f.existed || err != nil && !errors.Is(err, os.ErrNotExist) || !bytes.Equal(b, f.before) {
					return fmt.Errorf("%s changed after planning", f.path)
				}
				return nil
			},
			apply: func() (projectUndo, error) {
				var err error
				if exists {
					err = edit.WriteAtomic(f.path, after)
				} else {
					err = os.Remove(f.path)
				}
				if err != nil {
					return projectUndo{}, err
				}
				return projectUndo{rollback: func() error {
					if err := f.check(); err != nil {
						return err
					}
					current, err := os.ReadFile(f.path)
					if (err == nil) != exists || err != nil && !errors.Is(err, os.ErrNotExist) || !bytes.Equal(current, after) {
						return fmt.Errorf("%s changed again; rollback refused", f.path)
					}
					if !f.existed {
						return os.Remove(f.path)
					}
					if err := edit.WriteAtomic(f.path, f.before); err != nil {
						return err
					}
					return os.Chmod(f.path, f.mode)
				}}, nil
			},
		})
	}
	out = append(out, slices.Collect(maps.Values(w.other))...)
	dirs := map[string]bool{}
	for _, op := range out {
		if op.Action == "remove" || op.Action == "rmdir" {
			continue
		}
		for dir := filepath.Dir(op.Path); !dirs[dir]; dir = filepath.Dir(dir) {
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				if err != nil {
					return nil, err
				}
				break
			}
			dirs[dir] = true
		}
	}
	for dir := range dirs {
		out = append(out, projectWrite{
			ProjectWrite: ProjectWrite{Path: dir, Action: "mkdir"},
			check: func() error {
				if _, err := os.Lstat(dir); !os.IsNotExist(err) || realBelow(dir) != dir {
					return fmt.Errorf("%s changed after planning", dir)
				}
				return nil
			},
			apply: func() (projectUndo, error) {
				if err := os.Mkdir(dir, 0o755); err != nil {
					return projectUndo{}, err
				}
				return projectUndo{rollback: func() error { return os.Remove(dir) }}, nil
			},
		})
	}
	slices.SortFunc(out, func(a, b projectWrite) int {
		if a.Path == state {
			return 1
		}
		if b.Path == state {
			return -1
		}
		if a.Action == "mkdir" || b.Action == "mkdir" {
			switch {
			case a.Action == b.Action:
				return strings.Compare(a.Path, b.Path)
			case a.Action == "mkdir":
				return -1
			default:
				return 1
			}
		}
		// Remove empty directories after their contents. The lexical order
		// of everything else also makes the plan stable across requests.
		if a.Action == "rmdir" || b.Action == "rmdir" {
			switch {
			case a.Action != b.Action && a.Action == "rmdir":
				return 1
			case a.Action != b.Action:
				return -1
			default:
				return strings.Compare(b.Path, a.Path)
			}
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out, nil
}

// planTidy removes only directories MagPie created, after every entry
// seen in them is scheduled for removal.
func (p *Project) planTidy() {
	slices.SortFunc(p.Made, func(a, b string) int { return len(b) - len(a) })
	var kept []string
	for _, rel := range p.Made {
		abs := filepath.Join(p.Dir, filepath.FromSlash(rel))
		fi, err := os.Lstat(abs)
		if os.IsNotExist(err) {
			// A directory that has not been made yet must be recorded for
			// the next reconciliation.
			dir := realBelow(abs)
			if p.writes.hasWriteBelow(dir) {
				kept = append(kept, rel)
			}
			continue
		}
		real, pathErr := p.writePath(abs, true)
		if err != nil || !fi.IsDir() || pathErr != nil {
			kept = append(kept, rel)
			continue
		}
		entries, err := os.ReadDir(real)
		if err != nil || p.writes.hasWriteBelow(real) || slices.ContainsFunc(entries, func(entry fs.DirEntry) bool {
			path := filepath.Join(real, entry.Name())
			if op, ok := p.writes.other[path]; ok {
				return op.Action != "remove" && op.Action != "rmdir"
			}
			if f := p.writes.files[path]; f != nil && f.existed {
				_, err := os.Lstat(f.stage)
				return !os.IsNotExist(err)
			}
			return true
		}) {
			kept = append(kept, rel)
			continue
		}
		p.writes.other[real] = projectWrite{
			ProjectWrite: ProjectWrite{Path: real, Action: "rmdir"},
			check: func() error {
				to, err := p.writePath(abs, true)
				switch current, statErr := os.Lstat(real); {
				case err != nil:
					return err
				case to != real || statErr != nil || !os.SameFile(fi, current):
					return fmt.Errorf("%s changed after planning", abs)
				}
				return nil
			},
			apply: func() (projectUndo, error) {
				if err := os.Remove(real); err != nil {
					return projectUndo{}, err
				}
				return projectUndo{rollback: func() error { return os.Mkdir(real, fi.Mode().Perm()) }}, nil
			},
		}
	}
	slices.Sort(kept)
	p.Made = kept
}

func (w *projectWrites) hasWriteBelow(dir string) bool {
	for path, op := range w.other {
		if within(path, dir) && op.Action != "remove" && op.Action != "rmdir" {
			return true
		}
	}
	for path, f := range w.files {
		if within(path, dir) && !f.existed {
			if _, err := os.Stat(f.stage); err == nil {
				return true
			}
		}
	}
	return false
}
