package library

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const piBundleFixture = "fixture-pi-bundle"

func importPiBundleFixture(t *testing.T, source string, withSkill bool) {
	t.Helper()
	write(t, filepath.Join(source, "package.json"), `{
  "name":"fixture-pi-bundle",
  "version":"1.0.0",
  "pi":{"extensions":["entry.ts"],"skills":["skills"]}
}`)
	write(t, filepath.Join(source, "entry.ts"), piFixtureExtension("fixture-package"))
	if withSkill {
		write(t, filepath.Join(source, "skills", "fixture-package", "SKILL.md"),
			"---\nname: fixture-package\ndescription: Fixture documentation\n---\nFixture.\n")
	}
	ok(t)(InstallPlugins(source, nil, []string{"none"}))
}

func piFixtureExtension(name string) string {
	return `import { appendFileSync } from "node:fs";
export default function (pi) {
  appendFileSync(process.env.PI_FIXTURE_LOG, "` + name + `\n");
  pi.registerCommand("` + name + `", { description: "Fixture", handler: async () => {} });
}
`
}

// Exercise the adapter in the staged writer used by the bundle entry point.
func givePiFixture(t *testing.T, dir string, options ProjectWriteOptions) *Result {
	t.Helper()
	var skipped []Problem
	res, err := changeProject(dir, projectPi, func(l *Library) error {
		var err error
		skipped, err = l.assignProjectPiBundle(l.project(dir), l.plugin(piBundleFixture))
		return err
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("Pi project problems: %+v", res.Problems)
	}
	res.Skipped = skipped
	return res
}

func TestProjectPiBundleUsesOneNativePackage(t *testing.T) {
	_, project := piProjects(t)
	source := filepath.Join(t.TempDir(), "extension-only")
	importPiBundleFixture(t, source, false)
	preview := givePiFixture(t, project, ProjectWriteOptions{Preview: true})
	if preview.PlanID == "" || len(preview.Writes) == 0 || piProjectFile(t, project) != nil {
		t.Fatalf("preview = %+v, settings = %v", preview, piProjectFile(t, project))
	}
	givePiFixture(t, project, ProjectWriteOptions{PlanID: preview.PlanID})
	if got := piList(t, project, "packages"); !slices.Equal(got, []string{pluginDir(piBundleFixture)}) {
		t.Fatalf("packages = %v, want one library package", got)
	}
	if got := givePiFixture(t, project, ProjectWriteOptions{}); len(got.Writes) != 0 || len(got.Skipped) != 0 {
		t.Fatalf("repeat applied extra writes or skips: %+v", got)
	}
	p, err := ReadProject(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.PiEntries) != 1 || p.PiEntries[0].Scope != ScopeProject {
		t.Fatalf("Pi entries = %+v", p.PiEntries)
	}
}

func TestProjectPiBundleKeepsNativePackage(t *testing.T) {
	for _, nativeSource := range []string{"../original", "npm:fixture-pi-bundle@0.8.0"} {
		t.Run(nativeSource, func(t *testing.T) {
			_, project := piProjects(t)
			source := filepath.Join(project, "original")
			importPiBundleFixture(t, source, true)
			settings := `{"packages":[{"source":"` + nativeSource + `","skills":["skills/fixture-package/SKILL.md"]},"npm:unrelated"],"extensions":["../standalone.ts"],"theme":"dark"}`
			write(t, piProjectPath(project), settings)
			res := givePiFixture(t, project, ProjectWriteOptions{})
			if len(res.Writes) != 0 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Error, nativeSource) {
				t.Fatalf("native source result = %+v", res)
			}
			got, err := os.ReadFile(piProjectPath(project))
			if err != nil || string(got) != settings {
				t.Fatalf("settings = %q, error: %v", got, err)
			}
			p, err := ReadProject(project)
			if err != nil || len(p.PiEntries) != 0 {
				t.Fatalf("Pi project took ownership of the native source: %#v, %v", p, err)
			}
		})
	}
}

// An opt-in, fresh Pi process checks runtime registration without a model
// turn. The executable can be a local network-denying wrapper around Pi.
func TestProjectPiBundleRuntime(t *testing.T) {
	pi := os.Getenv("MAGPIE_TEST_PI")
	if !filepath.IsAbs(pi) {
		t.Skip("set MAGPIE_TEST_PI to an absolute Pi executable path")
	}
	piPath := os.Getenv("PATH") // the fixture HOME gives the Library its own PATH
	home, project := piProjects(t)
	source := filepath.Join(project, "original")
	importPiBundleFixture(t, source, true)
	outside := t.TempDir()
	gitInit(t, outside)
	agent := filepath.Join(t.TempDir(), "agent")
	loadLog := filepath.Join(t.TempDir(), "loaded")

	probe := func(dir string, wantCommands, wantLoads []string) {
		t.Helper()
		write(t, loadLog, "")
		ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		cmd := exec.CommandContext(ctx, pi, "--mode", "rpc", "--offline", "--no-session", "--approve")
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + piPath, "HOME=" + home, "USERPROFILE=" + home,
			"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "PI_CODING_AGENT_DIR=" + agent,
			"PI_OFFLINE=1", "PI_FIXTURE_LOG=" + loadLog}
		cmd.Stdin = strings.NewReader("{\"id\":\"fixture\",\"type\":\"get_commands\"}\n")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil || stderr.Len() != 0 {
			t.Fatalf("Pi RPC (%s): %v, stderr = %s", dir, err, &stderr)
		}
		var commands []string
		replied := false
		for line := range bytes.SplitSeq(bytes.TrimSpace(out), []byte{'\n'}) {
			var message struct {
				Type    string
				ID      string
				Success bool
				Data    struct {
					Commands []struct {
						Name       string
						SourceInfo struct{ Scope string }
					}
				}
			}
			if err := json.Unmarshal(line, &message); err != nil || message.Type == "extension_error" {
				t.Fatalf("Pi RPC (%s): %s (%v)", dir, line, err)
			}
			if message.ID != "fixture" || !message.Success {
				continue
			}
			replied = true
			for _, command := range message.Data.Commands {
				if strings.HasPrefix(command.Name, "fixture-") || strings.HasPrefix(command.Name, "skill:fixture-") {
					if command.SourceInfo.Scope != "project" {
						t.Fatalf("unexpected fixture scope: %+v", command)
					}
					commands = append(commands, command.Name)
				}
			}
		}
		loads, err := os.ReadFile(loadLog)
		if !replied || !slices.Equal(slices.Sorted(slices.Values(commands)), wantCommands) ||
			err != nil || !slices.Equal(slices.Sorted(slices.Values(strings.Fields(string(loads)))), wantLoads) {
			t.Fatalf("Pi RPC (%s): commands %v, loads %q, result %s, read error: %v", dir, commands, loads, out, err)
		}
		t.Logf("Pi RPC (%s): commands %v, loads %v", filepath.Base(dir), commands, strings.Fields(string(loads)))
	}

	givePiFixture(t, project, ProjectWriteOptions{})
	probe(project, []string{"fixture-package", "skill:fixture-package"}, []string{"fixture-package"})
	probe(outside, nil, nil)

	native := filepath.Join(t.TempDir(), "native")
	gitInit(t, native)
	ok(t)(AddProject(native))
	write(t, filepath.Join(native, "standalone.ts"), piFixtureExtension("fixture-standalone"))
	relative, err := filepath.Rel(filepath.Join(native, ".pi"), source)
	if err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(map[string]any{
		"packages":   []any{map[string]any{"source": relative, "extensions": []string{"entry.ts"}}},
		"extensions": []string{"../standalone.ts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, piProjectPath(native), string(config))
	if res := givePiFixture(t, native, ProjectWriteOptions{}); len(res.Skipped) != 1 || len(res.Writes) != 0 {
		t.Fatalf("existing native package was changed: %+v", res)
	}
	probe(native, []string{"fixture-package", "fixture-standalone", "skill:fixture-package"}, []string{"fixture-package", "fixture-standalone"})
	if _, err := os.Stat(filepath.Join(agent, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("Pi user settings appeared: %v", err)
	}
}
