// smoke.mjs — frontend smoke test. Dev-only: not embedded in the binary
// (embed.go only ships *.js), so it is not served to the browser.
//
// There is no build step and no browser test runner here, so this stubs just
// enough DOM to load the *real* module graph in node: it evaluates app.js's
// boot path and then exercises the main renderers (list cards, heatmap,
// transcript + markdown, outline rail) with sample data. That catches
// ReferenceErrors, typos and broken imports that `node --check` cannot.
//
//   node web/smoke.mjs
//
// It runs in CI (see .github/workflows/ci.yml).

import { mkdtempSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const src = here;

/* ---------- minimal DOM ---------- */
const el = () => ({
  addEventListener() {},
  removeEventListener() {},
  classList: { toggle() {}, add() {}, remove() {}, contains: () => false },
  dataset: {},
  style: {},
  hidden: false,
  innerHTML: "",
  textContent: "",
  checked: false,
  value: "",
  disabled: false,
  offsetParent: null,
  offsetTop: 0,
  offsetHeight: 0,
  clientHeight: 0,
  scrollTop: 0,
  children: [],
  querySelector: () => null,
  querySelectorAll: () => [],
  closest: () => null,
  setAttribute() {},
  removeAttribute() {},
  insertAdjacentHTML(_pos, html) { this.innerHTML += html; },
  replaceWith() {},
  scrollTo() {},
  scrollIntoView() {},
  select() {},
  focus() {},
  click() {},
});

const cache = new Map();
globalThis.document = {
  getElementById: (id) => {
    if (!cache.has(id)) cache.set(id, el());
    return cache.get(id);
  },
  querySelectorAll: () => [],
  querySelector: () => null,
  addEventListener() {},
  createElement: () => el(),
  documentElement: el(),
  body: el(),
  activeElement: null,
};
globalThis.window = globalThis;
if (!globalThis.addEventListener) globalThis.addEventListener = () => {};
globalThis.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
globalThis.location = { search: "", pathname: "/" };
globalThis.history = { replaceState() {}, pushState() {} };
globalThis.requestAnimationFrame = (fn) => setTimeout(fn, 0);
Object.defineProperty(globalThis.navigator, "clipboard", { value: { writeText: async () => {} }, configurable: true });

/* ---------- stubbed API ---------- */
const shapes = {
  "/api/version": { version: "test" },
  "/api/scan": { scanning: false, done: 4, total: 4, lastScan: "" },
  "/api/stats": { agents: [], total: { sessions: 0 }, lastScan: "" },
  "/api/projects": [],
  "/api/sessions": { sessions: [], total: 0, limit: 60, offset: 0 },
  "/api/trend": [],
  "/api/hours": [],
  "/api/tools": { tools: [], total: 0 },
  "/api/models": [],
  "/api/relations": [],
  "/api/rescan": { ok: true },
};
const transcript = [
  { role: "user", text: "hello **world**", timestamp: "2026-01-01T12:00:00Z" },
  { role: "reasoning", text: "thinking...", timestamp: "2026-01-01T12:00:01Z" },
  { role: "tool", text: "ls -la", timestamp: "2026-01-01T12:00:02Z" },
  { role: "assistant", text: "```go\nfmt.Println()\n```", timestamp: "2026-01-01T12:00:03Z" },
  { role: "compact", text: "## Summary\nearlier work", timestamp: "2026-01-02T08:59:00Z" },
  { role: "user", text: "one more", timestamp: "2026-01-02T09:00:00Z" },
];
globalThis.fetch = async (url) => {
  const p = String(url).split("?")[0];
  if (p.startsWith("/api/sessions/")) return { ok: true, status: 200, json: async () => transcript };
  if (!(p in shapes)) throw new Error("unexpected fetch: " + url);
  return { ok: true, status: 200, json: async () => shapes[p] };
};

// The repo has no package.json, so node would load the .js sources as
// CommonJS. Copy them into a temp dir typed as ESM and import from there.
const dir = mkdtempSync(join(tmpdir(), "asv-web-"));
writeFileSync(join(dir, "package.json"), JSON.stringify({ type: "module" }));
for (const f of readdirSync(src)) {
  if (f.endsWith(".js")) writeFileSync(join(dir, f), readFileSync(join(src, f)));
}
const mod = (name) => import(pathToFileURL(join(dir, name)).href);

// md.js is a classic script in the page (it assigns window.md); run it in the
// global scope so the markdown rendering path is exercised too.
(0, eval)(readFileSync(join(src, "md.js"), "utf8"));

const assert = (cond, msg) => {
  if (!cond) throw new Error("assertion failed: " + msg);
};

/* ---------- boot ---------- */
await mod("app.js");
await new Promise((r) => setTimeout(r, 300)); // let the boot promise chain settle
console.log("ok: module graph evaluated and boot path ran");

const { state, setTags, tagsOf } = await mod("state.js");
const { hl, displayTitle, shortPath } = await mod("util.js");
const { renderList } = await mod("list.js");
const detail = await mod("detail.js");
const dash = await mod("dashboard.js");

/* ---------- top-bar stats ---------- */
shapes["/api/stats"] = { agents: [], total: { sessions: 1, tokensIn: 25, tokensOut: 4, cacheRead: 75 }, lastScan: "" };
await (await mod("stats.js")).loadStats();
assert(cache.get("stats").innerHTML.includes("75.0%"), "top bar shows the cache hit rate");

/* ---------- pure helpers ---------- */
assert(hl("Hello World", "world") === "Hello <mark>World</mark>", "hl highlights case-insensitively");
assert(displayTitle({ title: "550e8400-e29b-41d4-a716-446655440000", preview: "first line\nsecond" }) === "first line", "displayTitle skips uuid titles");
assert(shortPath("/a/b/c/d") === "c/d", "shortPath keeps the tail");
assert(shortPath("C:\\Users\\me\\proj") === "me/proj", "shortPath accepts Windows separators");

/* ---------- markdown edge cases ---------- */
{
  const html = globalThis.md.render("- a\n  - b\n    - c\n- [x] done\n![x](https://example.com/x.png)");
  assert((html.match(/<ul>/g) || []).length === 3, "nested lists render to their real depth");
  assert(html.includes("md-task done"), "task list items render a checked box");
  assert(html.includes("md-img-blocked") && !html.includes("<img"), "remote images are blocked by default");
}

/* ---------- session list ---------- */
// The subagent count comes from the unscoped relations, so it survives a
// filter that leaves the child out of the list rows.
state.relations = [
  { agent: "claude", id: "s2", relation: "subagent", parentId: "s1" },
  { agent: "claude", id: "s3", relation: "subagent", parentId: "s1" },
];
renderList([
  { agent: "claude", id: "s1", title: "Fix the bug", preview: "p", cwd: "/a/b/c", msgCount: 3, tokensIn: 1, tokensOut: 2, cacheRead: 3, cost: 0.5, compacts: 2, model: "openai/gpt-5", updatedAt: "2026-01-01T12:00:00Z" },
  { agent: "claude", id: "s2", relation: "subagent", parentId: "s1", title: "child", updatedAt: "2026-01-01T12:00:00Z" },
]);
const listHTML = cache.get("list").innerHTML;
assert(listHTML.includes('data-id="s1"'), "renderList renders the parent card");
assert(!listHTML.includes('data-id="s2"'), "renderList hides subagent cards");
assert(listHTML.includes("subagent ×2"), "renderList counts children from the relations, not just the rows");
assert(listHTML.includes("compact ×2"), "renderList shows the compaction count");
assert(listHTML.includes("card-cwd-copy"), "renderList offers a copy-directory action");

/* ---------- table view + pagination ---------- */
state.tags["claude|s1"] = ["demo"];
state.mode = "table";
state.total = 120;
renderList([
  { agent: "claude", id: "s1", title: "Fix the bug", cwd: "/a/b/c", updatedAt: "2026-01-01T12:00:00Z" },
]);
const tableHTML = cache.get("list").innerHTML;
assert(tableHTML.includes("session-table") && tableHTML.includes('data-id="s1"'), "table mode renders session rows");
assert(cache.get("list-info").textContent.includes("已显示"), "pagination reports the visible subset");
state.mode = "cards";
state.total = 0;
delete state.tags["claude|s1"];

/* ---------- tags on cards ---------- */
state.tags["claude|s1"] = ["demo"];
renderList([{ agent: "claude", id: "s1", title: "Fix the bug", updatedAt: "2026-01-01T12:00:00Z" }]);
assert(cache.get("list").innerHTML.includes("#demo"), "cards render local tags");
delete state.tags["claude|s1"];

/* ---------- tag storage + project ordering ---------- */
setTags("claude", "s1", ["demo", " demo ", "x", ""]);
assert(tagsOf("claude", "s1").join(",") === "demo,x", "tags are trimmed and deduplicated");
setTags("claude", "s1", []);
assert(tagsOf("claude", "s1").length === 0, "empty tags are removed");

{
  const { renderProjects } = await mod("projects.js");
  state.projects = [
    { cwd: "/b", count: 1, agents: {} },
    { cwd: "/a", count: 2, agents: {} },
  ];
  state.cwd = "";
  state.projectOrder = "name";
  renderProjects();
  const html = cache.get("projects").innerHTML;
  assert(html.indexOf('data-cwd="/a"') < html.indexOf('data-cwd="/b"'), "projects sort by name");
  state.projectOrder = "count";
  state.projects = [];
}

/* ---------- empty / loading states ---------- */
{
  state.agent = "";
  state.cwd = "";
  state.q = "";
  state.day = "";
  state.scan = null;
  state.sessions = [];
  state.total = 0;
  renderList([]);
  assert(cache.get("empty").innerHTML.includes("正在加载会话"), "empty state while scan status is unknown");
  state.scan = { scanning: false, lastScan: "2026-01-01T00:00:00Z" };
  state.agent = "codex";
  renderList([]);
  assert(cache.get("empty").innerHTML.includes("data-clear-all") && cache.get("empty").innerHTML.includes("agent"), "filtered empty state offers a clear-filters action");
  state.agent = "";
}

/* ---------- dashboard extras ---------- */
dash.renderTopSessions(cache.get("top-body"), [
  { agent: "claude", id: "s1", title: "expensive", tokensIn: 10, msgCount: 2, cost: 1 },
]);
assert(cache.get("top-body").innerHTML.includes("data-id=\"s1\""), "top sessions render rows");
state.topMetric = "tools";
dash.renderTopSessions(cache.get("top-body"), [
  { agent: "claude", id: "s2", title: "tool heavy", toolCount: 7 },
]);
assert(cache.get("top-body").innerHTML.includes("7 次"), "top sessions can rank by tool calls");
state.topMetric = "cost";
const weekDays = [0, 1, 2].map((off) => {
  const d = new Date();
  d.setDate(d.getDate() - off);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
});
dash.renderWeek(cache.get("week-body"), [{ day: weekDays[0], msgs: 5, tokens: 100 }], "msgs");
assert(cache.get("week-body").innerHTML.includes("rh-bar"), "last-7-days chart renders bars");
assert(cache.get("week-body").innerHTML.split("rh-bar").length === 8, "last-7-days chart renders 7 bars");
dash.renderWeek(cache.get("week-body"), [{ day: weekDays[1], msgs: 5, tokens: 100 }], "tokens");
assert(cache.get("week-sub").textContent.includes("100 tokens"), "last-7-days chart follows the metric");
dash.renderHourly(cache.get("hourly-body"), [{ hour: 9, msgs: 4, sessions: 1, tokens: 50 }, { hour: 14, msgs: 1, tokens: 10 }], "tokens");
const hourlyHTML = cache.get("hourly-body").innerHTML;
assert(hourlyHTML.split("rh-bar").length === 25, "hourly chart renders all 24 hour bars");
assert(cache.get("hourly-sub").textContent.includes("峰值 9 点 50 tokens"), "hourly sub reports the peak hour in the selected metric");

/* ---------- dashboard ---------- */
dash.renderActivity(cache.get("activity-body"), [{ day: "2026-01-02", msgs: 5, sessions: 1, tokens: 10 }], 90, "msgs", true);
assert(cache.get("activity-body").innerHTML.includes("hm-cell"), "renderActivity draws heatmap cells");
{
  const day = (off) => {
    const d = new Date();
    d.setDate(d.getDate() - off);
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  };
  // today 200 tokens vs yesterday 100 -> +100%; cache 75 of (25 + 75) prompt tokens.
  dash.renderKPI(cache.get("kpi-body"), [{ day: day(0), msgs: 2, tokens: 200, cost: 1 }, { day: day(1), msgs: 1, tokens: 100, cost: 0 }], { tokensIn: 25, cacheRead: 75 });
  const kpi = cache.get("kpi-body").innerHTML;
  assert(kpi.includes("+100%"), "renderKPI compares today with yesterday");
  assert(kpi.includes("75.0%"), "renderKPI shows the cache hit rate");

  // Two recent active days in a 90-day range: leading idle days are trimmed to 14 bars.
  const short = dash.trendBuckets([{ day: day(0), msgs: 3 }, { day: day(2), msgs: 1 }], 90, "msgs");
  assert(!short.weekly && short.bars.length === 14, "trendBuckets trims idle lead-in to 14 days");
  // Activity spread over a year groups into weekly bars, keeping the total.
  const year = dash.trendBuckets([{ day: day(300), msgs: 2 }, { day: day(0), msgs: 5 }], 365, "msgs");
  assert(year.weekly && year.bars.length <= 45, "trendBuckets groups long ranges by week");
  assert(year.bars.reduce((n, b) => n + b.v, 0) === 7, "weekly buckets keep the total");

  state.agent = "claude";
  dash.renderAgentCompare(cache.get("agent-body"), [{ agent: "claude", sessions: 2, tokensIn: 10, cacheRead: 30 }, { agent: "codex", sessions: 1 }]);
  const at = cache.get("agent-body").innerHTML;
  assert(at.includes('data-agent="codex"') && /class="active"\s+data-agent="claude"/.test(at), "agent table marks the active tab");
  state.agent = "";
}

/* ---------- transcript modal ---------- */
state.relations = [
  { agent: "claude", id: "s1", title: "parent" },
  { agent: "claude", id: "k1", parentId: "s1", relation: "subagent", title: "Explore transport code", msgCount: 12, tokensIn: 1000 },
  { agent: "claude", id: "k2", parentId: "s1", relation: "subagent", title: "Explore tests", msgCount: 3 },
];
detail.openDetail({ agent: "claude", id: "s1", title: "Fix the bug", cwd: "/a/b", model: "m", tokensIn: 25, tokensOut: 4, cacheRead: 75, createdAt: "2026-01-01T12:00:00Z" });
await new Promise((r) => setTimeout(r, 60));
const stats = cache.get("detail-stats").innerHTML;
const rel = cache.get("detail-rel").innerHTML;
assert(/<i>对话<\/i>3</.test(stats), "openDetail renders the stats chips");
assert((rel.match(/class="rel-row"/g) || []).length === 2 && rel.includes("Explore transport code") && rel.includes("12 条"), "subagents render as one row each");
const body = cache.get("detail-body").innerHTML;
assert(body.includes('class="msg user"') && body.includes("<strong>world</strong>"), "transcript renders markdown messages");
assert(body.includes("md-code"), "transcript renders fenced code blocks");
assert(body.includes("上下文压缩 #1") && body.includes("compact-excerpt") && body.includes("earlier work"), "transcript renders the compaction marker with excerpt and summary");
assert(stats.includes("<i>压缩</i>1 次"), "stats line counts compactions");
assert(stats.includes("<i>输入</i>25") && stats.includes("<i>输出</i>4") && stats.includes("<i>缓存读</i>75"), "stats line splits input / output / cache");
assert(stats.includes("<i>缓存命中</i>75.0%"), "stats line shows the cache hit rate");
assert(cache.get("rail-list").innerHTML.includes("rail-item"), "transcript builds the outline rail");
detail.findInTranscript("hello", 1);
assert(cache.get("find-count").textContent === "1/1", "find counts matching messages");

console.log("SMOKE OK: renders exercised with sample data");
process.exit(0);
