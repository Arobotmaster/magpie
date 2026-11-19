package library

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUserPiDeclarationChanges(t *testing.T) {
	h := sandbox(t)
	path := filepath.Join(h, ".pi/agent/settings.json")
	module := filepath.Join(h, ".pi/agent/npm/node_modules/example/index.js")
	const keep = `"unknown": {"keep":[1,  2]}`
	write(t, path, "{\n  "+keep+",\n  \"packages\": [\"npm:example\", {\"source\":\"npm:other\",\"extensions\":[\"one.js\"]}]\n}\n")
	write(t, module, "module")
	for _, mode := range []PiChange{PiOff, PiOn, PiRemove} {
		if err := ChangeUserPi("package", "npm:example", mode, false); err != nil {
			t.Fatal(err)
		}
		entries, err := UserPiEntries()
		if err != nil {
			t.Fatal(err)
		}
		if mode == PiRemove {
			if len(entries) != 1 || entries[0].Source != "npm:other" {
				t.Fatalf("unexpected entries: %+v", entries)
			}
		} else if len(entries) != 2 || entries[0].On != (mode == PiOn) {
			t.Fatalf("unexpected state: %+v", entries)
		}
	}
	if err := ChangeUserPi("extension", "./extension.ts", PiOn, true); err != nil {
		t.Fatal(err)
	}
	if err := ChangeUserPi("extension", "./extension.ts", PiOff, false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); !strings.Contains(got, keep) || !strings.Contains(got, "-./extension.ts") || !strings.Contains(got, "one.js") {
		t.Fatalf("unexpected settings: %s", got)
	}
	if read(t, module) != "module" {
		t.Fatal("local installation changed")
	}
}

func TestProjectPiAdoptKeepsBytesAndScopeThenToggles(t *testing.T) {
	project, copy := piProject(t)
	path := piProjectPath(project)
	const source = "/local/npm/example"
	const before = `{"theme":"dark","packages":["/local/npm/example","./manual"]}`
	write(t, path, before)
	preview, err := AdoptProjectPi(project, "package", source, ScopeProject, ProjectWriteOptions{Preview: true})
	if err != nil || len(preview.Writes) != 1 || filepath.Base(preview.Writes[0].Path) != "library.json" {
		t.Fatalf("adoption should only plan the ownership record: %+v, %v", preview, err)
	}
	if _, err := AdoptProjectPi(project, "package", source, ScopeProject, ProjectWriteOptions{PlanID: preview.PlanID}); err != nil {
		t.Fatal(err)
	}
	if read(t, path) != before {
		t.Fatal("adopt rewrote the project settings")
	}
	v, err := ReadProject(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.PiEntries) != 1 || v.PiEntries[0].Scope != ScopeProject ||
		len(v.Native["pi"]) != 1 || v.Native["pi"][0].Name != "./manual" {
		t.Fatalf("unexpected ownership and scope: %+v, %+v", v.PiEntries, v.Native)
	}
	for _, mode := range []PiChange{PiOff, PiOn, PiRemove} {
		res, err := ProjectPiChange(project, "package", []string{source}, mode)
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range res.Problems {
			if problem.What != "project:pi" || !strings.Contains(problem.Error, "own file") {
				t.Fatalf("unexpected problem: %+v", problem)
			}
		}
		conf, err := readPiProject(project)
		if err != nil {
			t.Fatal(err)
		}
		if len(conf.pkgs) == 0 || !strings.Contains(string(conf.pkgs[len(conf.pkgs)-1]), "./manual") {
			t.Fatal("native package declaration was lost")
		}
		if mode == PiRemove {
			if len(conf.pkgs) != 1 {
				t.Fatalf("adopted entry remained: %s", conf.pkgs)
			}
		} else if len(conf.pkgs) != 2 || piEntryOn("package", conf.pkgs[0]) != (mode == PiOn) {
			t.Fatalf("unexpected package state: %s", conf.pkgs)
		}
	}
	gone(t, piProjectPath(copy))
}
