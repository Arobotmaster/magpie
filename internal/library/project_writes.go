package library

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/claudelock"
	"github.com/yetone/magpie/internal/edit"
)

// ProjectDistribution selects which library resources a project shares
// with its repository's other worktrees. The saved zero value shares none.
type ProjectDistribution struct {
	Skills       bool `json:"skills"`
	Subagents    bool `json:"subagents"`
	Instructions bool `json:"instructions"`
}

// ProjectWriteOptions lets a caller preview the same change it can apply.
type ProjectWriteOptions struct {
	Preview bool
	PlanID  string
}

// ProjectWrite is a changed destination. A copied skill is one directory
// operation, with its individual file paths listed under Files.
type ProjectWrite struct {
	Path   string   `json:"path"`
	Action string   `json:"action"`
	Files  []string `json:"files,omitempty"`
}

type projectParts uint8

const (
	projectSkills projectParts = 1 << iota
	projectRoles
	projectInstructions
	projectServers
	projectHooks
	projectPi
	projectPlugins
	projectLegacyCopies
	projectAll = projectSkills | projectRoles | projectInstructions | projectServers | projectHooks | projectPi | projectPlugins
)

// ProjectPlan previews reconciling the registered project with its saved
// assignments. ProjectApply uses the same planner.
func ProjectPlan(dir string) (*Result, error) {
	return ProjectApply(dir, ProjectWriteOptions{Preview: true})
}

func ProjectApply(dir string, options ...ProjectWriteOptions) (*Result, error) {
	return changeProject(dir, projectAll, func(*Library) error { return nil }, options...)
}

func ProjectDistribute(dir string, distribution ProjectDistribution, options ...ProjectWriteOptions) (*Result, error) {
	return changeProject(dir, projectSkills|projectRoles|projectInstructions, func(l *Library) error {
		l.project(dir).Distribution = distribution
		return nil
	}, options...)
}

// ProjectDistributeCategory changes one saved switch without replacing
// the other two with values read earlier by the caller.
func ProjectDistributeCategory(dir, category string, on bool, options ...ProjectWriteOptions) (*Result, error) {
	var part projectParts
	switch category {
	case "skills":
		part = projectSkills
	case "subagents":
		part = projectRoles
	case "instructions":
		part = projectInstructions
	default:
		return nil, fmt.Errorf("unknown distribution category %q", category)
	}
	return changeProject(dir, part, func(l *Library) error {
		d := &l.project(dir).Distribution
		switch part {
		case projectSkills:
			d.Skills = on
		case projectRoles:
			d.Subagents = on
		case projectInstructions:
			d.Instructions = on
		}
		return nil
	}, options...)
}

func changeProject(dir string, parts projectParts, change func(*Library) error, options ...ProjectWriteOptions) (*Result, error) {
	mu.Lock()
	defer mu.Unlock()
	l, err := load()
	if err != nil {
		return nil, err
	}
	return l.writeProject(dir, parts, change, options...)
}

func (l *Library) writeProject(dir string, parts projectParts, change func(*Library) error, options ...ProjectWriteOptions) (*Result, error) {
	opt := ProjectWriteOptions{}
	if len(options) != 0 {
		opt = options[0]
	}
	p := l.project(dir)
	if !opt.Preview {
		unlock, err := lockLibraryWrites()
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	if p == nil && parts != 0 {
		return nil, fmt.Errorf("no project %s", dir)
	}
	w, err := newProjectWrites()
	if err != nil {
		return nil, err
	}
	defer w.close()
	if p != nil {
		if fi, err := os.Lstat(p.Dir); err == nil && linkEntry(fi) {
			return nil, fmt.Errorf("%s is a redirected project root", p.Dir)
		}
		p.writes = w
		defer func() {
			p.writes = nil
			for _, t := range p.Trees {
				t.writes = nil
			}
		}()
	}
	if err := change(l); err != nil {
		return nil, err
	}
	res := &Result{Changed: []string{}, Problems: []Problem{}, Writes: []ProjectWrite{}}
	if p != nil && parts != 0 && l.project(dir) != nil {
		l.syncProject(p, res, parts)
		if p.removeAfter && len(res.Problems) == 0 && !p.holds() && p.Claude == nil && !slices.ContainsFunc(p.Trees, (*Project).holds) {
			l.Projects = slices.DeleteFunc(l.Projects, func(x *Project) bool { return x == p })
		}
		p.removeAfter = false
	}
	state := filepath.Join(realBelow(filepath.Dir(path())), filepath.Base(path()))
	dst, err := w.localFile(state)
	if err != nil {
		return res, err
	}
	if !bytes.Equal(w.files[state].before, l.source) {
		return res, fmt.Errorf("%s changed during planning", state)
	}
	data, err := l.encoded()
	if err != nil {
		return res, err
	}
	if err := edit.WriteAtomic(dst, data); err != nil {
		return res, err
	}
	ops, err := w.operations(state)
	if err != nil {
		return nil, err
	}
	digest := sha256.New()
	for _, op := range ops {
		res.Writes = append(res.Writes, op.ProjectWrite)
		fmt.Fprintf(digest, "%q %q %q\n", op.Path, op.Action, op.fingerprint)
	}
	for i := range res.Problems {
		problem := &res.Problems[i]
		for _, f := range w.files {
			problem.Error = strings.ReplaceAll(problem.Error, f.stage, f.path)
		}
		fmt.Fprintf(digest, "%q %q %q\n", problem.Agent, problem.What, problem.Error)
	}
	res.PlanID = hex.EncodeToString(digest.Sum(nil))
	if opt.Preview {
		return res, nil
	}
	if opt.PlanID != "" && opt.PlanID != res.PlanID {
		return nil, errors.New("project write plan changed; preview it again")
	}
	// Refresh Git state and all paths before making the first edit.
	if slices.ContainsFunc(ops, func(op projectWrite) bool {
		return op.Path == filepath.Join(realBelow(filepath.Dir(claudeJSON())), filepath.Base(claudeJSON()))
	}) {
		release, err := claudelock.Config(claudeJSON())
		if err != nil {
			return res, err
		}
		defer release()
	}
	clear(w.git)
	for _, op := range ops {
		if err := op.check(); err != nil {
			return res, err
		}
	}
	var journal []projectUndo
	rollback := func(cause error) error {
		for i := len(journal) - 1; i >= 0; i-- {
			if err := journal[i].rollback(); err != nil {
				cause = errors.Join(cause, err)
			}
		}
		return cause
	}
	for _, op := range ops {
		if err := op.check(); err != nil {
			return res, rollback(err)
		}
		undo, err := op.apply()
		if err != nil {
			return res, rollback(fmt.Errorf("%s: %w", op.Path, err))
		}
		journal = append(journal, undo)
	}
	for _, undo := range journal {
		if undo.finish != nil {
			undo.finish()
		}
	}
	l.source = data
	return res, nil
}

func syncProjects(res *Result) error {
	mu.Lock()
	l, err := load()
	mu.Unlock()
	if err != nil {
		return err
	}
	for _, p := range l.Projects {
		r, err := changeProject(p.Dir, projectAll|projectLegacyCopies, func(*Library) error { return nil })
		if r != nil {
			res.Problems = append(res.Problems, r.Problems...)
			if err == nil {
				res.Writes = append(res.Writes, r.Writes...)
			}
		}
		if err != nil {
			res.fail(p.Dir, "project:", err)
		}
	}
	return nil
}
