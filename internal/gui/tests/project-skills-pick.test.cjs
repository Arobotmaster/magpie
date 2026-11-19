// Run with Node's test runner and Playwright on the module path; see README.md.
// POM_JUMPING on X: "skill加入本地项目是能否可选？之前添加到本地项目会把所有skill添加进去".
// An open project's card listed every library skill, one row each, as
// though all were in the project. It now lists the skills the project has,
// and its Skills menu (the app menu, the project's skills ticked) picks which
// go in: one ticked posts projects/skills for the agents the project's skills
// are for already (every agent that reads a project's skills, in a project
// with none yet), one unticked posts it with no agents. The skills it had
// keep their agents. In English and Chinese. No backend: the API is faked.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const HOME = "/Users/emo";
const APP = HOME + "/code/app", WEB = HOME + "/code/web";
const agent = (id, name, more = {}) => ({ id, name, icon: "", skills: `${HOME}/.${id}/skills`, ...more });
const sk = (name) => ({ name, description: name + " things", agents: ["claude", "codex"] });

function server(lang, posts) {
  const projects = {
    [APP]: { pdf: ["codex"] },
    [WEB]: {},
  };
  const lib = () => ({
    dir: `${HOME}/.magpie/library`, backups: `${HOME}/.magpie/backups`, home: HOME,
    agents: [
      agent("claude", "Claude Code", { icon: "claudecode-color", projectSkills: ".claude/skills", projectHooks: ".claude/settings.local.json", projectSubagents: ".claude/agents" }),
      agent("codex", "Codex", { icon: "codex-color", projectSkills: ".agents/skills", projectMCP: ".codex/config.toml" }),
      agent("pi", "Pi", { icon: "pi" }),
      agent("goose", "Goose"),
    ],
    instructions: { sets: [], agents: [] },
    servers: ["native-server", "unused-server"].map((name) => ({ name, transport: "stdio", command: "true", agents: [] })),
    hooks: ["library-hook", "unused-hook"].map((name) => ({ name, event: "turn-end", command: "true", agents: [] })),
    subagents: ["library-role", "unused-role"].map((name) => ({ name, agents: [] })),
    skills: [sk("pdf"), sk("docx"), sk("xlsx")], foundServers: [], foundSkills: [],
    projects: Object.entries(projects).map(([dir, skills]) => ({
      dir, name: path.basename(dir), copy: false, skills: { ...skills }, placed: [], wrote: {},
      servers: dir === APP ? { "native-server": ["codex"] } : { "unused-server": ["codex"] },
      hooks: dir === APP ? { "library-hook": ["claude"] } : {},
      subagents: dir === APP ? { "library-role": ["claude"] } : {},
      piEntries: dir === APP ? [{ kind: "package", source: "npm:managed" }] : [],
      claudePlugins: dir === APP ? { "managed@market": true } : {},
      native: dir === APP ? {
        skills: [
          { name: "native-skill", description: "A project skill", path: ".agents/skills/native-skill" },
          ...Array.from({ length: 7 }, (_, i) => ({ name: "extra-" + i, path: ".agents/skills/extra-" + i })),
        ],
        hooks: [{ name: 'exec "$CLAUDE_PROJECT_DIR/bin/fixture.sh" "<b>literal</b>"', path: ".claude/settings.json", event: "PreToolUse", agent: "claude" }],
        servers: [{ name: "native-server", path: ".codex/config.toml", transport: "stdio", agent: "codex" }],
        subagents: [{ name: "native-role", path: ".claude/agents/native-role.md", description: "Reviews fixtures", agent: "claude" }],
        instructions: [{ name: "CLAUDE.md", path: "CLAUDE.md", agents: ["claude"] }],
        pi: [{ name: "npm:@sample/native", path: ".pi/settings.json", agent: "pi", event: "package", off: true }],
      } : {},
      skillLinks: dir === APP ? { ".claude/skills/native-skill": "../../.agents/skills/native-skill" } : {},
    })),
  });
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    if (url.protocol === "blob:") return route.continue();
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return route.fulfill({ json: { agents: [], profiles: [], settings: { lang, theme: "light" } } });
    if (url.pathname === "/api/library") return route.fulfill({ json: lib() });
    if (url.pathname === "/api/library/mcp/check") return route.fulfill({ json: { servers: {} } });
    if (url.pathname.startsWith("/api/library/") && req.method() === "POST") posts.push(req.postDataJSON());
    if (url.pathname === "/api/library/projects/plugin-origins") {
      assert.equal(req.method(), "GET");
      if (url.searchParams.get("dir") !== APP) return route.fulfill({ json: [] });
      return route.fulfill({ json: [
        { name: "example-kit", origin: "library", scope: "library", installed: true, enabled: true,
          parts: [{ kind: "hook", name: "example-kit-notify" }] },
        { name: "example-kit@market", origin: "claude-native", agent: "claude", scope: "local", installed: true, enabled: false,
          parts: [{ kind: "mcp", name: "lookup", agent: "claude" }] },
        { name: "project-search", origin: "codex-mcp", agent: "codex", scope: "project", enabled: true },
        { name: "npm:@example/pi-tools", origin: "pi-package", agent: "pi", scope: "project", installed: true, enabled: true },
        { name: "managed@market", origin: "library", agent: "claude", scope: "project", source: ".claude/settings.local.json", enabled: true },
        { name: "user-only@market", origin: "claude-native", agent: "claude", scope: "user", installed: true },
      ] });
    }
    if (url.pathname === "/api/library/projects/skills") {
      const b = req.postDataJSON();
      for (const n of b.names) {
        if (b.agents.length) projects[b.dir][n] = b.agents;
        else delete projects[b.dir][n];
      }
      return route.fulfill({ json: { ...lib(), result: { changed: [], problems: [] } } });
    }
    if (url.pathname === "/api/plugins") return route.fulfill({ json: { plugins: [] } });
    if (url.pathname === "/api/usage/quotas") return route.fulfill({ json: [] });
    if (url.pathname === "/api/groups") return route.fulfill({ json: { groups: [] } });
    if (url.pathname === "/api/providers") return route.fulfill({ json: { providers: [], gateway: { running: true } } });
    if (url.pathname.startsWith("/api/")) return route.fulfill({ json: {} });
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

const words = {
  en: { menu: "Skills", head: "Pick the skills it gets", none: "No skills yet", in: "docx is in app for Codex", out: "docx is out of app", both: "docx, xlsx are in web for Claude Code, Codex" },
  zh: { menu: "技能", head: "选择它要用的技能", none: "还没有技能", in: "docx 已放入 app，供 Codex 使用", out: "docx 已从 app 移除", both: "docx, xlsx 已放入 web，供 Claude Code, Codex 使用" },
};

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(engine + ": plugin sources distinguish bundle, native install and independent resources", async (t) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    t.after(() => browser.close());
    const labels = {
      en: ["MagPie library", "Claude Code native", "Independent Codex MCP", "Pi native package", "Session discovery Unknown"],
      zh: ["MagPie 资源库", "Claude Code 原生安装", "Codex 独立 MCP", "Pi 原生包", "会话发现 未知"],
      "zh-TW": ["MagPie 資源庫", "Claude Code 原生安裝", "Codex 獨立 MCP", "Pi 原生套件", "工作階段探索 未知"],
      ja: ["MagPie ライブラリ", "Claude Code ネイティブ", "Codex 独立 MCP", "Pi ネイティブパッケージ", "セッションで検出 不明"],
      de: ["MagPie-Bibliothek", "Claude Code nativ", "Eigenständiges Codex-MCP", "Natives Pi-Paket", "In Sitzung erkannt Unbekannt"],
    };
    for (const [lang, words] of Object.entries(labels)) {
      await t.test(lang, async (subtest) => {
        const page = await browser.newPage();
        subtest.after(() => page.close());
        await page.addInitScript(() => localStorage.setItem("magpie.libTab", "plugins"));
        const posts = [];
        await page.route("**/*", server(lang, posts));
        await page.goto("http://magpie.test/");
        await page.locator('button[data-view="library"]').click();
        const app = page.locator("#view-library .lib-project").filter({ hasText: "~/code/app" });
        await app.locator(".lib-projhead").click();
        const rows = app.locator(".lib-projplugin:has(button[aria-expanded])");
        await rows.last().waitFor();
        assert.equal(await rows.count(), 6);
        const names = ["example-kit", "example-kit@market", "project-search", "npm:@example/pi-tools"];
        const rowNamed = (name) => rows.filter({ has: page.getByText(name, { exact: true }) });
        for (const [i, word] of words.slice(0, 4).entries()) {
          const text = await rowNamed(names[i]).textContent();
          assert.ok(text.includes(word));
          assert.ok(!text.includes(words[4]));
        }
        await rowNamed("example-kit").locator("button[aria-expanded]").press("Enter");
        const details = rowNamed("example-kit").locator("+ .lib-detail");
        assert.match(await details.innerText(), /example-kit-notify/);
        assert.ok((await details.innerText()).includes(words[4]));
        assert.doesNotMatch(await details.innerText(), /lookup/);
        assert.deepEqual(posts, []);
      });
    }
  });

  test(engine + ": project-provided skills and MagPie links have read-only rows", async (t) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    t.after(() => browser.close());
    const labels = {
      en: "Project-provided",
      zh: "项目自带",
      "zh-TW": "專案自帶",
      ja: "プロジェクト付属",
      de: "Vom Projekt bereitgestellt",
    };
    for (const [lang, label] of Object.entries(labels)) {
      await t.test(lang, async (subtest) => {
        const page = await browser.newPage();
        subtest.after(() => page.close());
        await page.addInitScript(() => localStorage.setItem("magpie.libTab", "skills"));
        const posts = [];
        await page.route("**/*", server(lang, posts));
        await page.goto("http://magpie.test/");
        await page.locator('button[data-view="library"]').click();
        const app = page.locator("#view-library .lib-project").filter({ hasText: "~/code/app" });
        await app.locator(".lib-projhead").click();
        const headings = app.getByRole("heading", { level: 4 });
        const group = app.locator("section").filter({ has: page.getByRole("heading", { name: label + " · 8", exact: true }) });
        const rows = group.locator(".lib-projskill");
        assert.ok((await app.innerText()).includes(label));
        assert.match(await rows.first().textContent(), /A project skill/);
        assert.equal(await rows.first().getByRole("img", { name: "Codex" }).count(), 1);
        assert.equal(await rows.first().locator(".lib-ag, [role=switch]").count(), 0);
        assert.equal(await rows.count(), 5);
        const fold = app.locator(".lib-foundmore");
        await fold.press("Space");
        assert.equal(await fold.getAttribute("aria-expanded"), "true");
        assert.equal(await rows.count(), 8);
        assert.equal(await headings.first().textContent(), label + " · 8");
        const link = app.locator("section").nth(1).locator(".lib-projskill:has(button[aria-expanded])");
        assert.doesNotMatch(await link.textContent(), /\.claude\/skills\//);
        await link.locator("button").press("Enter");
        assert.match(await link.locator("+ .lib-detail").innerText(), /\.claude\/skills\/native-skill/);
        await fold.press("Space");
        assert.equal(await fold.getAttribute("aria-expanded"), "false");
        assert.equal(await rows.count(), 5);
        assert.equal(await headings.first().textContent(), label + " · 8");
        assert.deepEqual(posts, []);
      });
    }
  });

  test(engine + ": project declarations only disclose their source and details", async (t) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    t.after(() => browser.close());
    const command = 'exec "$CLAUDE_PROJECT_DIR/bin/fixture.sh" "<b>literal</b>"';
    for (const [tab, name, file, agent] of [
      ["hooks", "fixture.sh", ".claude/settings.json", "Claude Code"],
      ["mcp", "native-server", ".codex/config.toml", "Codex"],
      ["subagents", "native-role", ".claude/agents/native-role.md", "Claude Code"],
      ["instructions", "CLAUDE.md", "CLAUDE.md", "Claude Code"],
      ["pi", "npm:@sample/native", ".pi/settings.json", "Pi"],
    ]) {
      await t.test(tab, async () => {
        const page = await browser.newPage();
        const posts = [];
        try {
          await page.addInitScript((tab) => localStorage.setItem("magpie.libTab", tab), tab);
          await page.route("**/*", server("en", posts));
          await page.goto("http://magpie.test/");
          await page.locator('button[data-view="library"]').click();
          const app = page.locator("#view-library .lib-project").filter({ hasText: "~/code/app" });
          await app.locator(".lib-projhead").click();
          const row = app.locator(".lib-row:has(> button[aria-expanded])").filter({ has: page.getByText(name, { exact: true }) });
          assert.equal(await row.getByRole("img", { name: agent }).count(), 1);
          assert.equal(await row.locator(".lib-ag, [role=switch]").count(), 0);
          if (tab === "hooks") {
            assert.match(await row.textContent(), /PreToolUse/);
            assert.ok(!(await app.textContent()).includes(command));
          }
          const more = row.getByRole("button");
          await more.press("Enter");
          assert.equal(await more.getAttribute("aria-expanded"), "true");
          const detail = row.locator("+ .lib-detail");
          assert.ok((await detail.innerText()).includes(file));
          if (tab === "hooks") {
            assert.equal(await detail.locator("pre").textContent(), command);
            assert.equal(await detail.locator("pre b").count(), 0);
          }
          await more.press("Space");
          assert.equal(await more.getAttribute("aria-expanded"), "false");
          assert.equal(await detail.count(), 0);
          assert.deepEqual(posts, []);
        } finally {
          await page.close();
        }
      });
    }
  });

  test(engine + ": project resource groups show only project sources in their totals", async (t) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    t.after(() => browser.close());
    for (const [tab, native, assigned, available, name] of [
      ["skills", 8, 2, "", "pdf"],
      ["hooks", 1, 1, "Available from the library", "library-hook"],
      ["mcp", 1, 1, "Available from the library", "native-server"],
      ["subagents", 1, 1, "Available from the library", "library-role"],
      ["pi", 1, 1, "", "npm:managed"],
      ["plugins", 3, 1, "Other plugin sources", "managed@market"],
    ]) {
      await t.test(tab, async (subtest) => {
        const page = await browser.newPage();
        subtest.after(() => page.close());
        const posts = [];
        await page.addInitScript((tab) => localStorage.setItem("magpie.libTab", tab), tab);
        await page.route("**/*", server("en", posts));
        await page.goto("http://magpie.test/");
        await page.locator('button[data-view="library"]').click();
        const app = page.locator("#view-library .lib-project").filter({ hasText: "~/code/app" });
        await app.locator(".lib-projhead").click();
        const titles = ["Project-provided · " + native, "Assigned by MagPie · " + assigned, available].filter(Boolean);
        const heading = (name) => page.getByRole("heading", { level: 4, name, exact: true });
        await app.getByRole("heading", { level: 4, name: titles.at(-1), exact: true }).waitFor({ state: "attached" });
        assert.equal(await page.getByRole("heading", { level: 2, name: /^Project / }).count(), 1);
        assert.equal(await app.getByRole("heading", { level: 3, name: "app" }).count(), 1);
        assert.deepEqual(await app.getByRole("heading", { level: 4 }).allTextContents(), titles);
        const groups = titles.map((title) => app.locator("section").filter({ has: heading(title) }));
        assert.match(await groups[0].textContent(), /Read-only in MagPie/);
        assert.match(await groups[1].textContent(), /Configured for this project through MagPie/);
        assert.equal(await groups[0].locator(".lib-ag, [role=switch]").count(), 0);
        assert.equal(await groups[1].getByText(name, { exact: true }).count(), 1);
        assert.ok(await groups[1].locator(".lib-ag, [role=switch]").count() > 0);
        if (tab === "mcp") {
          assert.equal(await groups[0].getByText(name, { exact: true }).count(), 1);
          assert.match(await groups[2].textContent(), /unused-server/);
          const web = page.locator("#view-library .lib-project").filter({ hasText: "~/code/web" });
          await web.locator(".lib-projhead").click();
          assert.deepEqual(await web.getByRole("heading", { level: 4 }).allTextContents(), ["Assigned by MagPie · 1", available]);
        }
        if (tab === "plugins") {
          assert.equal(await groups[0].getByText("example-kit@market", { exact: true }).count(), 1);
          assert.equal(await groups[1].getByRole("switch").count(), 1);
          assert.deepEqual(await groups[2].locator(".lib-projplugin .name").allTextContents(), ["example-kit", "user-only@market"]);
        }
        assert.deepEqual(posts, []);
      });
    }
  });

  test(engine + ": a project's skills, picked from its menu", async (t) => {
    assert(["chromium", "webkit"].includes(engine), "BROWSER must be chromium or webkit");
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    const errors = [];
    t.after(async () => {
      if (errors.length) console.log(errors);
      await browser.close();
    });
    for (const lang of ["en", "zh"]) {
      await t.test(lang, async () => {
        const w = words[lang], posts = [];
        const ctx = await browser.newContext({ viewport: { width: 860, height: 1300 } });
        await ctx.addInitScript(() => { try { localStorage.setItem("magpie.libTab", "skills"); } catch {} });
        const page = await ctx.newPage();
        page.setDefaultTimeout(5000);
        page.setDefaultNavigationTimeout(60000);
        page.on("pageerror", (e) => errors.push(e.message));
        await page.route("**/*", server(lang, posts));
        await page.goto("http://magpie.test/");
        await page.locator('button[data-view="library"]').click();
        const app = page.locator("#view-library .lib-project").filter({ hasText: "~/code/app" });
        await app.waitFor();
        await app.locator(".lib-projhead").click();
        const rows = app.locator(".lib-projskill:has(.lib-agents)");
        await rows.first().waitFor();
        const names = async (loc) => loc.locator(".who > .name").evaluateAll((ns) => ns.map((n) => n.firstChild.textContent.trim()));
        assert.deepEqual(await names(rows), ["pdf"], "the open card lists the skills the project has, not every library skill");

        const status = (s) => page.waitForFunction((s) => document.querySelector("#status").textContent.trim() === s, s);
        const menu = page.locator(".proto-menu");
        const pick = app.locator(".lib-projpick[aria-haspopup=menu]");
        assert.equal((await pick.textContent()).trim(), w.menu);
        await pick.click();
        await menu.waitFor();
        assert.equal((await menu.locator(".pm-head").textContent()).trim(), w.head);
        assert.deepEqual(await menu.locator(".pm-item").evaluateAll((bs) => bs.map((b) => [b.textContent.trim(), b.getAttribute("aria-checked")])), [["pdf", "true"], ["docx", "false"], ["xlsx", "false"]]);
        await menu.locator(".pm-item", { hasText: "docx" }).click();
        assert.equal(posts.length, 0, "nothing is written while the menu is open");
        await page.keyboard.press("Escape");
        await status(w.in);
        assert.deepEqual(posts, [{ dir: APP, names: ["docx"], agents: ["codex"] }], "a skill ticked goes to the agents the project's skills are for");
        await page.waitForFunction(() => document.querySelectorAll("#view-library .lib-project .lib-projskill:has(.lib-agents)").length === 2);
        assert.deepEqual(await names(rows), ["pdf", "docx"]);

        // unticked: out of the project, the rest kept as they were
        await pick.click();
        await menu.waitFor();
        await menu.locator(".pm-item", { hasText: "docx" }).click();
        await page.keyboard.press("Escape");
        await status(w.out);
        assert.deepEqual(posts.at(-1), { dir: APP, names: ["docx"], agents: [] });
        await page.waitForFunction(() => document.querySelectorAll("#view-library .lib-projskill:has(.lib-agents)").length === 1);
        assert.deepEqual(await names(rows), ["pdf"]);
        assert.equal(posts.length, 2, "pdf was never written");

        // a project with no skills yet: its card says so, and two ticked go
        // in together, to every agent that reads a project's skills
        const web = page.locator("#view-library .lib-project").filter({ hasText: "~/code/web" });
        await web.locator(".lib-projhead").click();
        await web.locator(".lib-projnone").waitFor();
        assert.equal((await web.locator(".lib-projnone").textContent()).trim(), w.none);
        assert.equal(await web.locator(".lib-projskill").count(), 0);
        const webPick = web.locator(".lib-projpick[aria-haspopup=menu]");
        // Scroll the Library as a reader would, with both project cards open.
        await page.mouse.move(750, 900);
        await page.mouse.wheel(0, 410);
        await page.waitForFunction((button) => button.getBoundingClientRect().bottom < innerHeight - 30, await webPick.elementHandle());
        await webPick.click();
        await menu.waitFor();
        await menu.locator(".pm-item", { hasText: "docx" }).click();
        await menu.locator(".pm-item", { hasText: "xlsx" }).click();
        await page.mouse.click(5, 5);
        await status(w.both);
        assert.deepEqual(posts.at(-1), { dir: WEB, names: ["docx", "xlsx"], agents: ["claude", "codex"] });
        await page.waitForFunction(() => [...document.querySelectorAll("#view-library .lib-project")].some((c) => c.querySelectorAll(".lib-projskill").length === 2 && c.textContent.includes("~/code/web")));
        assert.deepEqual(errors, []);
        await ctx.close();
      });
    }
  });
}
