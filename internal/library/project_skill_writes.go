package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

func (p *Project) planSkill(abs, name string, asCopy, remove bool) error {
	path, err := p.writePath(abs, true)
	if err != nil {
		return err
	}
	before, err := projectSkillState(path, name)
	if err != nil {
		return err
	}
	if before != "" && !slices.Contains(p.Placed, p.rel(abs)) {
		return errForeign
	}
	if remove && before == "" || !remove && !asCopy && strings.HasPrefix(before, "link:") {
		return nil
	}
	source := realDir(skillDir(name))
	after := ""
	if !remove {
		after = "link:" + skillDir(name)
		if asCopy || runtime.GOOS == "windows" && strings.HasPrefix(before, "copy:") {
			h := hashDir(source)
			if h == "" {
				return fmt.Errorf("%s: could not read the library skill", source)
			}
			after = "copy:" + h
		}
		if before == after {
			return nil
		}
	}
	action := "link"
	if remove {
		action = "remove"
	} else if strings.HasPrefix(after, "copy:") {
		action = "copy"
	}
	var files []string
	if strings.HasPrefix(before, "copy:") {
		files, err = skillFiles(path, path)
		if err != nil {
			return err
		}
	}
	if action == "copy" {
		out, err := skillFiles(source, path)
		if err != nil {
			return err
		}
		files = append(files, out...)
		files = append(files, filepath.Join(path, marker))
	}
	slices.Sort(files)
	if !remove {
		if err := p.mkdirs(filepath.Dir(abs)); err != nil {
			return err
		}
	}
	p.writes.other[path] = projectWrite{
		ProjectWrite: ProjectWrite{Path: path, Action: action, Files: slices.Compact(files)},
		fingerprint:  before + "\x00" + after,
		check: func() error {
			dest, err := p.writePath(abs, true)
			if err != nil {
				return err
			}
			current, err := projectSkillState(dest, name)
			if err != nil {
				return err
			}
			if dest != path || current != before {
				return fmt.Errorf("%s changed after planning", abs)
			}
			if action == "copy" && hashDir(source) != strings.TrimPrefix(after, "copy:") {
				return fmt.Errorf("%s changed after planning", source)
			}
			return nil
		},
		apply: func() (projectUndo, error) {
			return applyProjectSkill(realDir(p.Dir), path, name, source, action, before != "")
		},
	}
	return nil
}

// A copy's marker establishes ownership only while its files still match
// the hash originally written there. Older markers with no hash can never
// establish permission to replace or remove a directory.
func projectSkillState(path, name string) (string, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !ours(path, name) {
		return "", errForeign
	}
	if linkEntry(fi) {
		target, _ := linkTarget(path)
		return "link:" + target, nil
	}
	markerInfo, err := os.Lstat(filepath.Join(path, marker))
	if err != nil || !markerInfo.Mode().IsRegular() {
		return "", errForeign
	}
	// copyDir deliberately ignores Git metadata, so hash equality cannot
	// tell us whether someone put a repository inside an existing copy.
	if err := filepath.WalkDir(path, func(at string, d fs.DirEntry, err error) error {
		if err == nil && strings.EqualFold(d.Name(), ".git") {
			return fmt.Errorf("%s crosses a Git repository boundary", at)
		}
		return err
	}); err != nil {
		return "", err
	}
	written, ok := markedHash(path)
	current := hashDir(path)
	if !ok || current == "" || written != current {
		return "", fmt.Errorf("%s is a skill copy changed outside magpie", path)
	}
	return "copy:" + current, nil
}

func skillFiles(src, dest string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(src, func(at string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() || d.Type()&fs.ModeSymlink != 0 {
			rel, _ := filepath.Rel(src, at)
			files = append(files, filepath.Join(dest, rel))
		}
		return nil
	})
	return files, err
}

func applyProjectSkill(root, path, name, source, action string, replace bool) (projectUndo, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return projectUndo{}, err
	}
	temp, err := os.MkdirTemp(root, ".magpie-skill-")
	if err != nil {
		return projectUndo{}, err
	}
	cleanup := func() { _ = os.RemoveAll(temp) }
	next := filepath.Join(temp, "next")
	switch action {
	case "link":
		err = link(next, name)
	case "copy":
		if err = copyDir(source, next); err == nil {
			err = markCopy(next, "copied from "+skillDir(name)+" by magpie\n")
		}
	}
	if err != nil {
		cleanup()
		return projectUndo{}, err
	}
	previous := filepath.Join(temp, "previous")
	if replace {
		if err := os.Rename(path, previous); err != nil {
			cleanup()
			return projectUndo{}, err
		}
	}
	if action != "remove" {
		if err := os.Rename(next, path); err != nil {
			if replace {
				if back := os.Rename(previous, path); back != nil {
					return projectUndo{}, errors.Join(err, fmt.Errorf("restore %s: %w", previous, back))
				}
			}
			cleanup()
			return projectUndo{}, err
		}
	}
	// Keep the prior copy until the library has committed ownership. The
	// backup lives outside the skill's parent when it is being removed so
	// that empty parent directories can be removed too.
	written, _ := projectSkillState(path, name)
	return projectUndo{finish: cleanup, rollback: func() error {
		current, err := projectSkillState(path, name)
		if err != nil || current != written {
			return fmt.Errorf("%s changed again; rollback refused (backup %s)", path, temp)
		}
		if current != "" {
			if err := os.Rename(path, next); err != nil {
				return err
			}
		}
		if replace {
			if err := os.Rename(previous, path); err != nil {
				return fmt.Errorf("restore %s: %w", previous, err)
			}
		}
		cleanup()
		return nil
	}}, nil
}
