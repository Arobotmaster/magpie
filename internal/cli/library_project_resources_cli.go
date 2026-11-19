package cli

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/yetone/magpie/internal/lazyre"
	"github.com/yetone/magpie/internal/library"
	"github.com/yetone/magpie/internal/uilang"
)

const (
	projectProvided = "project"
	projectAssigned = "magpie"
)

type projectResourceRow struct {
	library.ProjectResource
	Origin  string   `json:"origin"`
	Managed bool     `json:"managed"`
	Paths   []string `json:"paths,omitempty"`
	Problem string   `json:"problem,omitempty"`
	Scope   string   `json:"scope,omitempty"`
}

func projectResource(name, origin string) projectResourceRow {
	return projectResourceRow{
		ProjectResource: library.ProjectResource{Name: name},
		Origin:          origin, Managed: origin == projectAssigned,
	}
}

func projectWords(en, zh string) string {
	if uilang.Pick("", func() string { return uilang.Env(os.Getenv) }) == "zh" {
		return zh
	}
	return en
}

func printProjectSource(origin string, count int) {
	if count == 0 {
		return
	}
	label, detail := projectWords("Project-provided", "项目自带"),
		projectWords("From this project's own files. Read-only in MagPie.", "来自项目自己的文件，在 MagPie 中只读。")
	if origin == projectAssigned {
		label, detail = projectWords("Assigned by MagPie", "MagPie 分配"),
			projectWords("Configured for this project through MagPie.", "通过 MagPie 为此项目配置。")
	}
	fmt.Printf("  %s · %d\n    %s\n", bold.Render(label), count, muted.Render(detail))
}

// A project hook's stored name is its shell command. Use the script name
// or event on the listing, while the original native JSON stays available.
var projectHookScript = lazyre.New(`(?i)(?:^|[/"'\s])([\w.-]+\.(?:sh|bash|zsh|py|js|mjs|ts|ps1))(?:["'\s;|&)]|$)`)

func nativeProjectRow(kind string, r library.ProjectResource) projectResourceRow {
	row := projectResource(r.Name, projectProvided)
	row.ProjectResource = r
	switch kind {
	case "hooks":
		row.Name = r.Event
		row.Description = "Hook"
		if match := projectHookScript.FindStringSubmatch(r.Name); len(match) > 1 {
			row.Name, row.Description = match[1], r.Event
		}
	case "servers":
		row.Description = projectMCPDescription(r.Transport)
	case "pi":
		row.Description = projectPiDescription(r.Event)
	}
	return row
}

func projectMCPDescription(transport string) string {
	title := projectWords("MCP servers", "MCP 服务器")
	if transport != "" {
		title += " · " + transport
	}
	return title
}

func projectPiDescription(kind string) string {
	if kind == "package" {
		return projectWords("Packages", "包")
	}
	return projectWords("Extensions", "扩展")
}

func assignedProjectRows(p *library.ProjectView, kind string, descriptions map[string]string) []projectResourceRow {
	var rows []projectResourceRow
	if kind == "pi" {
		for _, entry := range p.PiEntries {
			row := projectResource(entry.Source, projectAssigned)
			row.Description, row.Event = projectPiDescription(entry.Kind), entry.Kind
			row.Agent, row.Path, row.Off = "pi", library.ProjectPiSettings("pi"), entry.Off
			row.Problem, row.Scope = p.Problems["pi:"+entry.Source], entry.Scope
			rows = append(rows, row)
		}
		return rows
	}
	if kind == "skills" {
		for _, path := range slices.Sorted(maps.Keys(p.SkillLinks)) {
			row := projectResource(filepath.Base(path), projectAssigned)
			row.Path, row.Description = path, projectWords("MagPie link", "MagPie 链接")
			rows = append(rows, row)
		}
	}
	var on, off map[string][]string
	switch kind {
	case "skills":
		on, off = p.Skills, p.SkillsOff
	case "hooks":
		on = p.Hooks
	case "servers":
		on, off = p.Servers, p.ServersOff
	case "subagents":
		on = p.Subagents
	}
	names := slices.Sorted(maps.Keys(on))
	names = append(names, slices.Sorted(maps.Keys(off))...)
	slices.Sort(names)
	for _, name := range slices.Compact(names) {
		row := projectResource(name, projectAssigned)
		row.Agents, row.Description = on[name], descriptions[name]
		if len(row.Agents) == 0 && len(off[name]) != 0 {
			row.Agents, row.Off = off[name], true
		}
		switch kind {
		case "skills":
			row.Paths, row.Problem = skillPlaces(p, name)
		case "hooks":
			row.Paths, row.Problem = hookPlaces(p, name), p.Problems["hook:"+name]
		case "servers":
			row.Transport = descriptions[name]
			row.Description = projectMCPDescription(row.Transport)
			row.Problem = p.Problems["mcp:"+name]
		case "subagents":
			row.Paths, row.Problem = subagentPlaces(p, name), p.Problems["subagent:"+name]
		}
		rows = append(rows, row)
	}
	return rows
}

// A description belongs on one terminal line, including when a resource
// file provided it. Drop terminal control characters from text we display.
func projectDisplayLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}), " ")
}

func printProjectResourceRows(origin string, rows []projectResourceRow) {
	printProjectSource(origin, len(rows))
	for _, row := range rows {
		line := bold.Render(projectDisplayLine(row.Name))
		if row.Description != "" {
			line += " · " + projectDisplayLine(row.Description)
		}
		agents := append([]string{}, row.Agents...)
		if row.Agent != "" {
			agents = append(agents, row.Agent)
		}
		if len(agents) != 0 {
			line += " → " + strings.Join(agents, ", ")
		}
		if row.Off {
			line += " · " + projectWords("Off", "已关闭")
		}
		if row.Scope == library.ScopeProject {
			line += " · " + projectWords("This folder only", "仅此文件夹")
		}
		fmt.Println("   ", line)
		paths := append([]string{}, row.Paths...)
		if row.Path != "" {
			paths = append(paths, row.Path)
		}
		if len(paths) > 0 {
			fmt.Println("     ", muted.Render(projectDisplayLine(strings.Join(paths, " "))))
		}
		if row.Problem != "" {
			fmt.Println("     ", amber.Render(projectDisplayLine(row.Problem)))
		}
	}
}

func listProjectResources(dir, kind string, asJSON bool) error {
	p, descriptions, err := library.ReadProjectDescriptions(dir, kind)
	if err != nil {
		return err
	}
	native := make([]projectResourceRow, 0, len(p.Native[kind]))
	for _, r := range p.Native[kind] {
		native = append(native, nativeProjectRow(kind, r))
	}
	assigned := assignedProjectRows(p, kind, descriptions)
	if asJSON {
		rows := append(native, assigned...)
		switch kind {
		case "servers":
			return printJSON([]any{map[string]any{
				"native": p.Native[kind], "dir": p.Dir, "name": p.Name, "servers": p.Servers, "resources": rows,
			}})
		case "subagents":
			return printJSON([]any{map[string]any{
				"native": p.Native[kind], "dir": p.Dir, "name": p.Name, "subagents": p.Subagents,
				"agentFiles": p.AgentFiles, "problems": p.Problems, "resources": rows,
			}})
		default:
			return printJSON(struct {
				*library.ProjectView
				Resources []projectResourceRow `json:"resources"`
			}{p, rows})
		}
	}
	fmt.Println(bold.Render(p.Name), muted.Render(p.Dir), muted.Render(kind))
	printProjectResourceRows(projectProvided, native)
	printProjectResourceRows(projectAssigned, assigned)
	if len(native)+len(assigned) == 0 {
		fmt.Println(" ", muted.Render(projectWords("No project "+kind+" yet", "此项目暂无资源")))
	}
	return nil
}
