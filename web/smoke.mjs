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
  scrollTo() {},
  select() {},
  focus() {},
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
};
globalThis.window = globalThis;
globalThis.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
globalThis.location = { search: "", pathname: "/" };
globalThis.history = { replaceState() {} };
Object.defineProperty(globalThis.navigator, "clipboard", { value: { writeText: async () => {} }, configurable: true });

/* ---------- stubbed API ---------- */
const shapes = {
  "/api/stats": { agents: [], total: { sessions: 0 }, lastScan: "" },
  "/api/projects": [],
  "/api/sessions": [],
  "/api/trend": [],
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

const { state } = await mod("state.js");
const { hl, displayTitle, shortPath } = await mod("util.js");
const { renderList } = await mod("list.js");
const detail = await mod("detail.js");
const dash = await mod("dashboard.js");

/* ---------- pure helpers ---------- */
assert(hl("Hello World", "world") === "Hello <mark>World</mark>", "hl highlights case-insensitively");
assert(displayTitle({ title: "550e8400-e29b-41d4-a716-446655440000", preview: "first line\nsecond" }) === "first line", "displayTitle skips uuid titles");
assert(shortPath("/a/b/c/d") === "c/d", "shortPath keeps the tail");

/* ---------- session list ---------- */
renderList([
  { agent: "claude", id: "s1", title: "Fix the bug", preview: "p", cwd: "/a/b/c", msgCount: 3, tokensIn: 1, tokensOut: 2, cacheRead: 3, cost: 0.5, compacts: 2, model: "openai/gpt-5", updatedAt: "2026-01-01T12:00:00Z" },
  { agent: "claude", id: "s2", relation: "subagent", parentId: "s1", title: "child", updatedAt: "2026-01-01T12:00:00Z" },
]);
const listHTML = cache.get("list").innerHTML;
assert(listHTML.includes('data-id="s1"'), "renderList renders the parent card");
assert(!listHTML.includes('data-id="s2"'), "renderList hides subagent cards");
assert(listHTML.includes("subagent ×1"), "renderList counts children on the parent");
assert(listHTML.includes("compact ×2"), "renderList shows the compaction count");

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
state.relations = [{ agent: "claude", id: "s1", title: "parent" }];
detail.openDetail({ agent: "claude", id: "s1", title: "Fix the bug", cwd: "/a/b", model: "m", tokensIn: 25, tokensOut: 4, cacheRead: 75, createdAt: "2026-01-01T12:00:00Z" });
await new Promise((r) => setTimeout(r, 60));
assert(cache.get("detail-stats").textContent.includes("3 条对话"), "openDetail renders the stats line");
const body = cache.get("detail-body").innerHTML;
assert(body.includes('class="msg user"') && body.includes("<strong>world</strong>"), "transcript renders markdown messages");
assert(body.includes("md-code"), "transcript renders fenced code blocks");
assert(body.includes("上下文压缩 #1") && body.includes("compact-sum") && body.includes("earlier work"), "transcript renders the compaction marker with its summary");
assert(cache.get("detail-stats").textContent.includes("压缩 1 次"), "stats line counts compactions");
assert(/输入 25 · 输出 4 · 缓存读 75/.test(cache.get("detail-stats").textContent), "stats line splits input / output / cache");
assert(cache.get("detail-stats").textContent.includes("缓存命中 75.0%"), "stats line shows the cache hit rate");
assert(cache.get("rail-list").innerHTML.includes("rail-item"), "transcript builds the outline rail");

console.log("SMOKE OK: renders exercised with sample data");
process.exit(0);
