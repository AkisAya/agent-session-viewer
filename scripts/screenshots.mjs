// screenshots.mjs — regenerate the README screenshots from synthetic data.
//
// Builds a fake Claude / Codex / DSH history in a temp dir (never your real
// sessions), runs the viewer against it in -memory mode, and captures the
// list, dashboard and transcript views with headless Chrome over CDP.
//
//   node scripts/screenshots.mjs          # writes docs/screenshots/*.webp
//   CHROME=/path/to/chrome node scripts/screenshots.mjs
//
// Needs Go, Node >= 22 (built-in WebSocket) and Chrome/Chromium.

import { execFileSync, spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = join(root, "docs", "screenshots");
const tmp = mkdtempSync(join(tmpdir(), "asv-shots-"));
const PORT = 18799;
const CDP = 19299;

/* ---------- deterministic fake data ---------- */
let seed = 42;
const rnd = () => ((seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648);
const pick = (a) => a[Math.floor(rnd() * a.length)];
const int = (lo, hi) => lo + Math.floor(rnd() * (hi - lo + 1));

const DAY = 86400000;
const today = new Date();
today.setHours(0, 0, 0, 0);
// A session starts on a working-ish day within the last ~150 days, busier lately.
const startAt = () => {
  const back = Math.floor(Math.pow(rnd(), 1.8) * 150);
  const d = new Date(today.getTime() - back * DAY + int(9, 21) * 3600000 + int(0, 59) * 60000);
  return d.getDay() === 0 && rnd() < 0.7 ? new Date(d.getTime() + DAY) : d;
};
const iso = (ms) => new Date(Math.min(ms, Date.now() - 60000)).toISOString();

const PROJECTS = ["/home/dev/projects/web-shop", "/home/dev/projects/api-gateway", "/home/dev/projects/data-pipeline",
  "/home/dev/projects/mobile-app", "/home/dev/projects/infra-terraform", "/home/dev/oss/markdown-lint"];
const TOPICS = ["购物车结算页加优惠券校验", "修复订单列表分页重复", "网关限流改为令牌桶", "接入 OpenTelemetry 链路追踪",
  "ETL 任务支持增量回放", "重构用户鉴权中间件", "Terraform 拆分 staging 环境", "iOS 推送证书过期排查",
  "为 lint 规则补单元测试", "优化首页首屏加载", "数据库慢查询分析", "升级 React 19 适配", "CI 缓存命中率优化",
  "实现导出 CSV 功能", "重写错误码文档", "排查内存泄漏", "Kafka 消费延迟告警", "多语言文案抽取"];
const CLAUDE_TOOLS = ["Bash", "Bash", "Bash", "Read", "Read", "Edit", "Edit", "Grep", "Glob", "Write", "WebFetch", "TodoWrite"];

function writeLines(path, lines) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, lines.map((l) => JSON.stringify(l)).join("\n") + "\n");
}

// The showcase session opened in the transcript screenshot.
const DEMO_ID = "0d7c5a3e-1b2f-4c8d-9e6a-5f4b3c2d1e0f";

function claudeSession(i) {
  const id = i === 0 ? DEMO_ID : `${String(i).padStart(8, "0")}-aaaa-4bbb-8ccc-${String(1000 + i).padStart(12, "0")}`;
  const cwd = i === 0 ? PROJECTS[0] : pick(PROJECTS);
  const title = i === 0 ? "购物车结算页加优惠券校验" : pick(TOPICS);
  const model = pick(["claude-opus-4-5", "claude-opus-4-5", "claude-sonnet-4-5", "claude-haiku-4-5"]);
  let t = i === 0 ? today.getTime() - 2 * 3600000 : startAt().getTime();
  const days = rnd() < 0.3 ? int(2, 4) : 1;
  const lines = [{ type: "custom-title", customTitle: title, sessionId: id }];
  const turns = i === 0 ? 0 : int(6, 40);
  const push = (type, message, extra = {}) => {
    if (type === "system-compact") {
      lines.push({ type: "system", subtype: "compact_boundary", timestamp: iso(t), sessionId: id, content: "Conversation compacted" });
      type = "user";
      message = { role: "user", content: "## 摘要\n- 目标：结算页优惠券校验（过期 / 门槛 / 互斥）\n- 已定位 `checkout/coupon.ts`，计划写成纯函数并补单测" };
      extra = { isCompactSummary: true };
    }
    lines.push({ type, timestamp: iso(t), sessionId: id, cwd, message, ...extra });
  };
  const usage = () => ({ input_tokens: int(5, 400), cache_creation_input_tokens: int(500, 8000), cache_read_input_tokens: int(20000, 180000), output_tokens: int(200, 3000) });
  let cost = 0;
  for (let k = 0; k < turns; k++) {
    if (days > 1 && k === Math.floor(turns / 2)) t += DAY * (days - 1);
    push("user", { role: "user", content: k === 0 ? `帮我${title}` : pick(["继续", "跑一下测试", "看下这个报错", "可以，提交吧", "再补一个边界用例"]) });
    t += int(20, 240) * 1000;
    const u = usage();
    cost += (u.input_tokens + u.cache_creation_input_tokens * 1.25) * 3e-6 + u.cache_read_input_tokens * 0.3e-6 + u.output_tokens * 15e-6;
    const content = [{ type: "thinking", thinking: "先看相关代码。" }];
    for (let n = int(0, 4); n > 0; n--) content.push({ type: "tool_use", name: pick(CLAUDE_TOOLS), input: {} });
    content.push({ type: "text", text: "已处理。" });
    push("assistant", { id: `msg_${i}_${k}`, role: "assistant", model, content, usage: u });
    t += int(30, 600) * 1000;
    if (k === 20 && rnd() < 0.5) {
      lines.push({ type: "system", subtype: "compact_boundary", timestamp: iso(t), sessionId: id, content: "Conversation compacted" });
      push("user", { role: "user", content: "This session is being continued from a previous conversation. Summary: ..." }, { isCompactSummary: true });
    }
  }
  if (i === 0) demoTranscript(push, () => (t += int(40, 200) * 1000), model);
  lines.push({ type: "cost-state", totalCostUSD: i === 0 ? 1.84 : Number(cost.toFixed(2)) });
  writeLines(join(tmp, "claude", cwd.replaceAll("/", "-"), id + ".jsonl"), lines);
}

function demoTranscript(push, tick, model) {
  const u = (o) => ({ input_tokens: 12, cache_creation_input_tokens: 3200, cache_read_input_tokens: 64000 + o, output_tokens: 900 });
  push("user", { role: "user", content: "结算页需要校验优惠券：过期、未达门槛、与其他活动互斥时给出明确提示。先看看现在的实现。" });
  tick();
  push("assistant", { id: "msg_d1", role: "assistant", model, usage: u(0), content: [
    { type: "thinking", thinking: "需要先找到结算页的优惠券逻辑和现有的校验入口。" },
    { type: "tool_use", name: "Grep", input: {} },
    { type: "tool_use", name: "Read", input: {} },
    { type: "text", text: "现在的校验集中在 `checkout/coupon.ts`，只判断了**是否存在**，缺少三类规则：\n\n| 规则 | 现状 | 计划 |\n|---|---|---|\n| 过期 | 未校验 | 比较 `expiresAt` 与服务端时间 |\n| 门槛 | 未校验 | 比较 `minAmount` 与商品小计 |\n| 互斥 | 未校验 | 读取活动的 `exclusiveGroup` |\n\n我会把规则写成纯函数，方便单测。" },
  ] });
  tick();
  push("system-compact");
  push("user", { role: "user", content: "可以，按这个来，记得补测试。" });
  tick();
  push("assistant", { id: "msg_d2", role: "assistant", model, usage: u(9000), content: [
    { type: "tool_use", name: "Edit", input: {} },
    { type: "tool_use", name: "Bash", input: {} },
    { type: "text", text: "已加上校验，核心逻辑如下：\n\n```ts\nexport function validateCoupon(c: Coupon, cart: Cart, now: Date): CouponError | null {\n  if (c.expiresAt < now) return \"expired\";\n  if (cart.subtotal < c.minAmount) return \"below_threshold\";\n  if (cart.promotions.some((p) => p.exclusiveGroup === c.exclusiveGroup)) return \"exclusive\";\n  return null;\n}\n```\n\n新增 6 个用例，`npm test` 全部通过。" },
  ] });
}

function codexSession(i) {
  const id = `01a0${String(i).padStart(4, "0")}-d842-75a3-b205-a43e027bd${String(100 + i).padStart(3, "0")}`;
  const cwd = pick(PROJECTS);
  let t = startAt().getTime();
  const d = new Date(t);
  const stamp = d.toISOString().slice(0, 19).replaceAll(":", "-");
  const lines = [
    { type: "session_meta", timestamp: iso(t), payload: { session_id: id, cwd, model_provider: "openai" } },
    { type: "turn_context", timestamp: iso(t), payload: { model: pick(["gpt-5.1-codex", "gpt-5.1"]), cwd } },
  ];
  let input = 0, cached = 0, output = 0;
  for (let k = int(4, 18); k > 0; k--) {
    t += int(30, 300) * 1000;
    lines.push({ type: "event_msg", timestamp: iso(t), payload: { type: "item_completed", item: { type: "UserMessage", content: [{ text: pick(TOPICS) }] } } });
    for (let n = int(1, 3); n > 0; n--) lines.push({ type: "event_msg", timestamp: iso(t), payload: { type: "item_completed", item: { type: "CommandExecution", command: ["bash", "-lc", "make test"] } } });
    t += int(30, 300) * 1000;
    lines.push({ type: "event_msg", timestamp: iso(t), payload: { type: "item_completed", item: { type: "AgentMessage", content: [{ text: "done" }] } } });
    const c = int(20000, 90000);
    input += c + int(500, 4000); cached += c; output += int(300, 2500);
    lines.push({ type: "event_msg", timestamp: iso(t), payload: { type: "token_count", info: { total_token_usage: { input_tokens: input, cached_input_tokens: cached, output_tokens: output } } } });
  }
  const p = String(d.getMonth() + 1).padStart(2, "0"), q = String(d.getDate()).padStart(2, "0");
  writeLines(join(tmp, "codex", String(d.getFullYear()), p, q, `rollout-${stamp}-${id}.jsonl`), lines);
}

function dshSession(i) {
  const id = `session-${String(i).padStart(8, "0")}-1a37-463c-8197-22f3f9cd857d`;
  const cwd = pick(PROJECTS);
  let t = startAt().getTime();
  let seq = 1;
  const lines = [{ type: "session", version: 4, id, createdAt: t, cwd, isSeeded: false }];
  lines.push({ type: "session/title", seq: seq++, time: t, data: { title: pick(TOPICS) } });
  for (let k = int(4, 14); k > 0; k--) {
    t += int(30, 300) * 1000;
    lines.push({ type: "user/message", seq: seq++, time: t, data: { content: [{ type: "text", text: pick(TOPICS) }], source: { kind: "user" } } });
    t += int(20, 200) * 1000;
    lines.push({ type: "assistant/message", seq: seq++, time: t, data: { message: { role: "assistant", content: [{ type: "text", text: "ok" }], source: { provider: "deepseek-official", model: "deepseek-v4-flash" } }, usage: { inputTokens: int(200, 3000), outputTokens: int(100, 1500), cacheReadTokens: int(8000, 60000) } } });
    lines.push({ type: "tool/call", seq: seq++, time: t, data: { callId: `c${seq}`, name: pick(["bash", "read", "write", "web_fetch"]), arguments: "{}" } });
  }
  writeLines(join(tmp, "dsh", cwd.replaceAll("/", "-"), id, "session.v4.jsonl"), lines);
}

for (let i = 0; i < 42; i++) claudeSession(i);
for (let i = 0; i < 14; i++) codexSession(i);
for (let i = 0; i < 6; i++) dshSession(i);

/* ---------- run the viewer and Chrome ---------- */
const chrome = process.env.CHROME || [
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser",
].find(existsSync);
if (!chrome) {
  console.error("Chrome not found; set CHROME=/path/to/chrome");
  process.exit(1);
}

const bin = join(tmp, "asv");
execFileSync("go", ["build", "-o", bin, "."], { cwd: root, stdio: "inherit" });
const server = spawn(bin, ["-memory", "-port", String(PORT),
  "-claude-dir", join(tmp, "claude"), "-codex-dir", join(tmp, "codex"), "-codex-index", join(tmp, "none.jsonl"),
  "-opencode-db", join(tmp, "none.db"), "-dsh-dir", join(tmp, "dsh")], { stdio: "ignore" });
const browser = spawn(chrome, ["--headless=new", "--disable-gpu", "--hide-scrollbars", `--remote-debugging-port=${CDP}`,
  `--user-data-dir=${join(tmp, "chrome")}`, "about:blank"], { stdio: "ignore" });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function waitFor(url) {
  for (let i = 0; i < 100; i++) {
    try {
      const r = await fetch(url);
      if (r.ok) return r;
    } catch {}
    await sleep(200);
  }
  throw new Error("timed out waiting for " + url);
}

try {
  await waitFor(`http://127.0.0.1:${PORT}/api/stats`);
  const tabs = await (await waitFor(`http://127.0.0.1:${CDP}/json`)).json();
  const ws = new WebSocket(tabs.find((t) => t.type === "page").webSocketDebuggerUrl);
  await new Promise((r) => ws.addEventListener("open", r));
  let id = 0;
  const pending = new Map();
  ws.addEventListener("message", (e) => {
    const m = JSON.parse(e.data);
    if (pending.has(m.id)) pending.get(m.id)(m.result), pending.delete(m.id);
  });
  const send = (method, params = {}) => new Promise((r) => (pending.set(++id, r), ws.send(JSON.stringify({ id, method, params }))));
  const ev = (expression) => send("Runtime.evaluate", { expression, awaitPromise: true });
  // 1280 CSS px at 1.25x = 1600 px wide WebP: sharp at GitHub's ~880 px README
  // width (and on retina), yet roughly 100 KB per image.
  const W = 1280;
  const viewport = (height) => send("Emulation.setDeviceMetricsOverride", { width: W, height, deviceScaleFactor: 1.25, mobile: false });
  const shot = async (name, height) => {
    const clip = height ? { clip: { x: 0, y: 0, width: W, height, scale: 1 } } : {};
    const { data } = await send("Page.captureScreenshot", { format: "webp", quality: 88, ...clip });
    const buf = Buffer.from(data, "base64");
    writeFileSync(join(outDir, name), buf);
    console.log(`wrote docs/screenshots/${name} (${Math.round(buf.length / 1024)} KB)`);
  };
  const open = async (path) => {
    await send("Page.navigate", { url: `http://localhost:${PORT}${path}` });
    await sleep(2500);
  };

  mkdirSync(outDir, { recursive: true });
  await viewport(800);
  await open("/");
  await ev(`localStorage.setItem("as-theme", "light")`);

  await open("/");
  await shot("list.webp");

  // Tall viewport so the whole dashboard renders, then crop to its content.
  await viewport(1600);
  await open("/?view=dash");
  const r = await ev(`Math.ceil(document.getElementById("dash").getBoundingClientRect().bottom + 16)`);
  await shot("dashboard.webp", Math.min(1600, r.result.value));

  await viewport(800);
  await open("/");
  await ev(`document.querySelector('.card[data-id="${DEMO_ID}"]').click()`);
  await sleep(1500);
  await shot("detail.webp");
  ws.close();
} finally {
  server.kill();
  browser.kill();
  await sleep(300);
  rmSync(tmp, { recursive: true, force: true });
}
