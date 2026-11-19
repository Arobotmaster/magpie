package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func (p *Project) ownsSkillLink(rel string) bool {
	want, ok := p.SkillLinks[rel]
	if !ok {
		return false
	}
	got, err := os.Readlink(filepath.Join(p.Dir, filepath.FromSlash(rel)))
	return err == nil && got == want
}

// syncSkillLinks runs for an enrolled project in its own folder. Worktree
// sync continues to place only assignments from the library.
func (p *Project) syncSkillLinks(on bool, res *Result) {
	if !isDir(p.Dir) {
		return
	}
	const shared = ".agents/skills"
	const claude = ".claude/skills"
	dst := filepath.Join(p.Dir, filepath.FromSlash(claude))
	if err := p.inside(dst); err != nil {
		return
	}
	wanted := map[string]string{}
	if on {
		existing := p.nativeSkills(claude)
		for _, r := range p.nativeSkills(shared) {
			if slices.ContainsFunc(existing, func(e ProjectResource) bool { return strings.EqualFold(e.Name, r.Name) }) {
				continue
			}
			src := filepath.Join(p.Dir, filepath.FromSlash(r.Path))
			if p.inside(src) != nil {
				continue
			}
			target, err := filepath.Rel(realBelow(dst), realBelow(src))
			if err == nil {
				wanted[claude+"/"+filepath.Base(src)] = target
			}
		}
	}
	failed := func(rel string, err error) {
		res.Problems = append(res.Problems, Problem{Agent: p.Dir, What: "project:" + rel, Error: err.Error()})
	}
	for rel, target := range p.SkillLinks {
		if p.ownsSkillLink(rel) {
			if wanted[rel] == target {
				delete(wanted, rel)
				continue
			}
			abs := filepath.Join(p.Dir, filepath.FromSlash(rel))
			if err := p.inside(filepath.Dir(abs)); err != nil {
				failed(rel, err)
				continue
			}
			if err := p.planNativeSkillLink(abs, target, true); err != nil {
				failed(rel, err)
				continue
			}
		}
		delete(p.SkillLinks, rel)
	}
	for rel, target := range wanted {
		abs := filepath.Join(p.Dir, filepath.FromSlash(rel))
		if _, err := os.Lstat(abs); !os.IsNotExist(err) {
			continue
		}
		if err := p.mkdirs(dst); err != nil {
			failed(rel, err)
			return
		}
		if err := p.planNativeSkillLink(abs, target, false); err != nil {
			failed(rel, fmt.Errorf("link project skill for Claude: %w", err))
			continue
		}
		if p.SkillLinks == nil {
			p.SkillLinks = map[string]string{}
		}
		p.SkillLinks[rel] = target
	}
}

func (p *Project) planNativeSkillLink(abs, target string, remove bool) error {
	path, err := p.writePath(abs, true)
	if err != nil {
		return err
	}
	action := "link"
	if remove {
		action = "remove"
	}
	p.writes.other[path] = projectWrite{
		ProjectWrite: ProjectWrite{Path: path, Action: action},
		fingerprint:  target,
		check: func() error {
			current, err := p.writePath(abs, true)
			if err != nil {
				return err
			}
			if current == path {
				if remove {
					if link, err := os.Readlink(path); err == nil && link == target {
						return nil
					}
				} else if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
					src := filepath.Join(filepath.Dir(path), target)
					if p.inside(src) == nil {
						if _, ok := readMeta(src); ok {
							return nil
						}
					}
				}
			}
			return fmt.Errorf("%s or its project skill changed after planning", abs)
		},
		apply: func() (projectUndo, error) {
			if remove {
				if err := os.Remove(path); err != nil {
					return projectUndo{}, err
				}
				return projectUndo{rollback: func() error { return os.Symlink(target, path) }}, nil
			}
			if err := os.Symlink(target, path); err != nil {
				return projectUndo{}, err
			}
			return projectUndo{rollback: func() error {
				if link, err := os.Readlink(path); err != nil || link != target {
					return fmt.Errorf("%s changed again; rollback refused", path)
				}
				return os.Remove(path)
			}}, nil
		},
	}
	return nil
}
