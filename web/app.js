const state = {
  agent: "",
  cwd: "",
  q: "",
  sort: "updated",
  view: "list",
  projects: [],
  sessions: [],
  theme: "auto",
  noise: false,
  lastScan: "",
  // dashboard data (scoped by agent + cwd)
  trend: [],
  models: [],
  agents: [],
  tools: { tools: [], total: 0 },
  // heatmap / trend display settings
  hmRange: 180,
  hmMetric: "msgs",
  trendMetric: "msgs",
  // active single-day filter (YYYY-MM-DD), set by clicking a heatmap cell
  day: "",
  // every session involved in a parent/child link, fetched once and filtered
  // client side so the detail view can link across scopes.
  relations: [],
  // transcript of the session currently open in the detail modal
  msgs: [],
  current: null,
  // true while the outline rail highlight follows a click instead of the
  // scroll position (a jump that cannot reach the top of the viewport).
  railLock: false,
};

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

const TAB_LABEL = { "": "全部", claude: "Claude", codex: "Codex", opencode: "OpenCode" };
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/* ---------- theme ---------- */
const THEME_KEY = "as-theme";
state.theme = localStorage.getItem(THEME_KEY) || "auto";

/* ---------- url state ---------- */
// The active filters live in the query string so a view can be shared or
// bookmarked and the browser back button works.
function readURL() {
  const p = new URLSearchParams(location.search);
  if (p.has("agent")) state.agent = p.get("agent") || "";
  if (p.has("cwd")) state.cwd = p.get("cwd") || "";
  if (p.get("view") === "dash") state.view = "dash";
  if (p.get("sort")) state.sort = p.get("sort");
  if (p.has("q")) state.q = p.get("q") || "";
  if (p.has("day")) state.day = p.get("day") || "";
}

function writeURL() {
  const p = new URLSearchParams();
  if (state.agent) p.set("agent", state.agent);
  if (state.cwd) p.set("cwd", state.cwd);
  if (state.view !== "list") p.set("view", state.view);
  if (state.sort !== "updated") p.set("sort", state.sort);
  if (state.q) p.set("q", state.q);
  if (state.day) p.set("day", state.day);
  const qs = p.toString();
  try {
    history.replaceState(null, "", qs ? "?" + qs : location.pathname);
  } catch (e) {}
}

/* ---------- motion preference ---------- */
const reduceMotion = matchMedia("(prefers-reduced-motion: reduce)").matches;
const scrollBehavior = () => (reduceMotion ? "auto" : "smooth");

// copyText writes to the clipboard, falling back to a hidden textarea.
async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch (e) {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    let ok = false;
    try {
      ok = document.execCommand("copy");
    } catch (e2) {}
    document.body.removeChild(ta);
    return ok;
  }
}

function applyTheme() {
  const pref = state.theme;
  const dark = pref === "dark" || (pref === "auto" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.setAttribute("data-theme", dark ? "dark" : "light");
  document.querySelectorAll("#theme-switch button").forEach((b) => b.classList.toggle("active", b.dataset.pref === pref));
}

/* ---------- helpers ---------- */
function fmtNum(n) {
  n = Number(n) || 0;
  if (n >= 1e9) return (n / 1e9).toFixed(2) + "B";
  if (n >= 1e6) return (n / 1e6).toFixed(2) + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1) + "k";
  return String(n);
}

function fmtTime(iso) {
  if (!iso || iso.startsWith("0001")) return "—";
  const d = new Date(iso);
  if (isNaN(d)) return "—";
  const diff = (Date.now() - d.getTime()) / 1000;
  if (diff < 60) return "刚刚";
  if (diff < 3600) return Math.floor(diff / 60) + " 分钟前";
  if (diff < 86400) return Math.floor(diff / 3600) + " 小时前";
  if (diff < 86400 * 30) return Math.floor(diff / 86400) + " 天前";
  return d.toLocaleDateString();
}

function fmtDate(iso) {
  const d = new Date(iso);
  return isNaN(d) ? "—" : d.toLocaleString();
}

function fmtClock(d) {
  return [d.getHours(), d.getMinutes(), d.getSeconds()].map((n) => String(n).padStart(2, "0")).join(":");
}

function fmtDayLabel(d) {
  return d.toLocaleDateString(undefined, { year: "numeric", month: "2-digit", day: "2-digit", weekday: "short" });
}

// displayTitle prefers a real title, then the first user-message preview.
function displayTitle(s) {
  const t = (s.title || "").trim();
  const named = t && !UUID_RE.test(t) && !/^new session\b/i.test(t);
  if (named) return t;
  const p = (s.preview || "").trim();
  if (p) return p.split("\n")[0].slice(0, 90);
  return "未命名会话";
}

// shortPath renders a directory as "parent/last".
function shortPath(cwd) {
  if (!cwd) return "(未知目录)";
  const parts = cwd.split("/").filter(Boolean);
  if (parts.length <= 2) return "/" + parts.join("/");
  return parts.slice(-2).join("/");
}

const cwdKey = (cwd) => (cwd === "" ? "__none__" : cwd);

// hl escapes text then wraps case-insensitive matches of q in <mark>.
function hl(text, q) {
  const e = esc(text);
  if (!q) return e;
  const eq = esc(q);
  const lower = e.toLowerCase();
  const lq = eq.toLowerCase();
  let out = "", i = 0;
  for (;;) {
    const idx = lower.indexOf(lq, i);
    if (idx < 0) return out + e.slice(i);
    out += e.slice(i, idx) + "<mark>" + e.slice(idx, idx + eq.length) + "</mark>";
    i = idx + eq.length;
  }
}

/* ---------- stats & tabs ---------- */
async function loadStats() {
  try {
    const p = scopeParams();
    if (state.day) p.set("day", state.day);
    const d = await (await fetch("/api/stats?" + p)).json();
    const counts = {};
    (d.agents || []).forEach((a) => (counts[a.agent] = a.sessions));
    const t = d.total || {};
    const tin = t.tokensIn || 0;
    const tout = t.tokensOut || 0;
    const cache = t.cacheRead || 0;
    $("stats").innerHTML = [
      `<span class="stat">共 <b>${t.sessions || 0}</b> 会话</span>`,
      `<span class="stat" title="输入 ${fmtNum(tin)} · 输出 ${fmtNum(tout)} · 缓存读 ${fmtNum(cache)}">Tokens <b class="up">↑${fmtNum(tin)}</b> <b class="down">↓${fmtNum(tout)}</b> <b class="cache">⚡${fmtNum(cache)}</b></span>`,
      `<span class="stat">花费 <b>$${(t.cost || 0).toFixed(2)}</b></span>`,
    ].join("");
    state.lastScan = d.lastScan || "";
    renderScanTime();
    // The agent breakdown is directory-scoped, so the "全部" tab sums it while
    // the other tabs read their own count.
    const all = (d.agents || []).reduce((n, a) => n + (a.sessions || 0), 0);
    renderTabs(counts, all);
    state.agents = d.agents || [];
    renderAgentCompare($("agent-body"), state.agents);
  } catch (e) {
    $("stats").innerHTML = `<span class="stat">统计加载失败</span>`;
  }
}

// renderScanTime shows how long ago the index was last refreshed.
function renderScanTime() {
  const el = $("scan-time");
  if (!el) return;
  el.textContent = state.lastScan ? "更新于 " + fmtTime(state.lastScan) : "";
  el.title = state.lastScan ? "上次扫描：" + fmtDate(state.lastScan) : "";
}

function renderTabs(counts, total) {
  document.querySelectorAll("#tabs .tab").forEach((b) => {
    const a = b.dataset.agent;
    const n = a ? counts[a] || 0 : total;
    b.innerHTML = esc(TAB_LABEL[a]) + `<span class="n">${n}</span>`;
  });
}

/* ---------- projects ---------- */
async function loadProjects() {
  const p = new URLSearchParams();
  if (state.agent) p.set("agent", state.agent);
  try {
    state.projects = (await (await fetch("/api/projects?" + p)).json()) || [];
  } catch (e) {
    state.projects = [];
  }
  if (state.cwd && !state.projects.some((x) => cwdKey(x.cwd) === cwdKey(state.cwd))) {
    state.cwd = "";
  }
  renderProjects();
  renderCrumb();
}

function renderProjects() {
  const total = state.projects.reduce((n, x) => n + x.count, 0);
  $("proj-count").textContent = state.projects.length;
  const items = [
    `<div class="proj ${state.cwd === "" ? "active" : ""}" data-cwd="" tabindex="0" role="button">
       <span class="proj-name">全部项目</span><span class="proj-count">${total}</span>
     </div>`,
  ];
  for (const p of state.projects) {
    const active = cwdKey(state.cwd) === cwdKey(p.cwd) ? "active" : "";
    items.push(
      `<div class="proj ${active}" data-cwd="${esc(cwdKey(p.cwd))}" title="${esc(p.cwd || "(未知目录)")}" tabindex="0" role="button">
         <span class="proj-name">${esc(shortPath(p.cwd))}</span><span class="proj-count">${p.count}</span>
       </div>`
    );
  }
  $("projects").innerHTML = items.join("");

  const sel = $("project-select");
  sel.innerHTML =
    `<option value="">全部项目 (${total})</option>` +
    state.projects
      .map((p) => `<option value="${esc(cwdKey(p.cwd))}" ${cwdKey(state.cwd) === cwdKey(p.cwd) ? "selected" : ""}>${esc(shortPath(p.cwd))} (${p.count})</option>`)
      .join("");
}

// renderCrumb shows the active single-day filter (set from the dashboard
// heatmap) as a small chip above the list. The project path is already shown
// in the project header and the sidebar, so it is not repeated here.
function renderCrumb() {
  const el = $("crumb");
  if (!state.day) {
    el.hidden = true;
    el.innerHTML = "";
    return;
  }
  el.hidden = false;
  el.innerHTML = `<span class="chip">日期 <b>${esc(state.day)}</b><button type="button" data-clear="day" title="清除日期筛选">✕</button></span>`;
}

$("crumb").addEventListener("click", (e) => {
  if (!e.target.closest("[data-clear]")) return;
  state.day = "";
  renderCrumb();
  loadStats();
  loadSessions();
  writeURL();
});

/* ---------- session list ---------- */
function showEmpty(text, hint) {
  $("list").innerHTML = "";
  $("empty").innerHTML = esc(text) + (hint ? `<div class="hint">${esc(hint)}</div>` : "");
  $("empty").hidden = false;
}

let sessionsAbort = null;

async function loadSessions() {
  showEmpty("加载中…");
  // Cancel any in-flight list request so a slow earlier response cannot
  // overwrite the newest filter (rapid typing, fast tab switching).
  if (sessionsAbort) sessionsAbort.abort();
  sessionsAbort = new AbortController();
  const p = new URLSearchParams();
  if (state.agent) p.set("agent", state.agent);
  if (state.cwd) p.set("cwd", state.cwd);
  if (state.q) p.set("q", state.q);
  if (state.day) p.set("day", state.day);
  p.set("sort", state.sort);
  try {
    const rows = (await (await fetch("/api/sessions?" + p, { signal: sessionsAbort.signal })).json()) || [];
    state.sessions = rows;
    renderList(rows);
  } catch (e) {
    if (e && e.name === "AbortError") return;
    showEmpty("加载失败", "请确认服务仍在运行，然后点击「刷新」重试");
  }
}

// resumeCommand returns the CLI command that reopens a session in its agent.
function resumeCommand(s) {
  if (s.agent === "claude") return `claude --resume ${s.id}`;
  if (s.agent === "codex") return `codex resume ${s.id}`;
  return `opencode --session ${s.id}`;
}

// midPath shortens a long path in the middle so the tail — the part that
// identifies the project — stays readable. The full path stays on the title.
function midPath(p, max) {
  max = max || 42;
  if (!p || p.length <= max) return p;
  const head = 12;
  return p.slice(0, head) + "…" + p.slice(p.length - (max - head - 1));
}

// shortModel keeps the identifying tail of a provider-qualified model id.
function shortModel(m) {
  const last = String(m || "").split("/").pop();
  return last.length > 18 ? last.slice(0, 17) + "…" : last;
}

function renderList(rows) {
  // Subagent sessions never appear as their own cards; they are reachable
  // from the main session's detail page. The parent card carries a count tag.
  const subCount = new Map();
  const visible = [];
  for (const s of rows) {
    if (s.relation === "subagent") {
      const pk = s.agent + "|" + s.parentId;
      subCount.set(pk, (subCount.get(pk) || 0) + 1);
    } else {
      visible.push(s);
    }
  }
  if (!visible.length) {
    const hint = state.q || state.cwd ? "试试清除搜索或项目筛选" : "点击右上角「刷新」重新扫描";
    showEmpty("没有匹配的会话", hint);
    return;
  }
  $("empty").hidden = true;
  const showCwd = !state.cwd;

  $("list").innerHTML = visible
    .map((s) => {
      const tokens = (s.tokensIn || 0) + (s.tokensOut || 0) + (s.cacheRead || 0);
      const title = displayTitle(s);
      const previewText = (s.preview || "").trim();
      const showPreview = previewText && previewText !== title;
      const subs = subCount.get(s.agent + "|" + s.id) || 0;
      const cmd = resumeCommand(s);
      return `<div class="card agent-${esc(s.agent)}" data-agent="${esc(s.agent)}" data-id="${esc(s.id)}" tabindex="0" role="button">
        <div class="card-title" title="${esc(title)}">${hl(title, state.q)}</div>
        ${showPreview ? `<div class="card-preview">${hl(previewText, state.q)}</div>` : ""}
        ${showCwd ? `<div class="card-cwd" title="${esc(s.cwd)}">${esc(midPath(s.cwd || "(未知目录)"))}</div>` : ""}
        <div class="card-foot">
          <div class="card-meta">
            <span class="badge ${esc(s.agent)}">${esc(s.agent)}</span>
            ${s.model ? `<span class="card-model" title="${esc(s.model)}">${esc(shortModel(s.model))}</span>` : ""}
            ${subs ? `<span class="tag-sub" title="包含 ${subs} 个 subagent 会话，打开详情页查看">subagent ×${subs}</span>` : ""}
            <span>${s.msgCount || 0} 条</span>
            <span class="tok">${fmtNum(tokens)} tokens</span>
            ${s.cost ? `<span>$${Number(s.cost).toFixed(2)}</span>` : ""}
            <span class="time" title="${esc(fmtDate(s.updatedAt))}">${fmtTime(s.updatedAt)}</span>
          </div>
          <button type="button" class="card-resume" data-cmd="${esc(cmd)}" title="复制到剪贴板：${esc(cmd)}">⧉ resume</button>
        </div>
      </div>`;
    })
    .join("");
}

/* ---------- dashboard: heatmap / tools / relations ---------- */
async function fetchJSON(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error("HTTP " + r.status);
  return r.json();
}

// scopeParams mirrors the current agent tab and directory filter.
function scopeParams() {
  const p = new URLSearchParams();
  if (state.agent) p.set("agent", state.agent);
  if (state.cwd) p.set("cwd", state.cwd);
  return p;
}

// dayKey formats a Date as YYYY-MM-DD in local time (matches the Go side).
function dayKey(d) {
  return d.getFullYear() + "-" + String(d.getMonth() + 1).padStart(2, "0") + "-" + String(d.getDate()).padStart(2, "0");
}

// longestStreak counts consecutive active days between start and end.
function longestStreak(map, start, end) {
  let cur = 0;
  let best = 0;
  for (const d = new Date(start); d <= end; d.setDate(d.getDate() + 1)) {
    const v = map.get(dayKey(d));
    if (v && v.msgs > 0) {
      cur++;
      if (cur > best) best = cur;
    } else {
      cur = 0;
    }
  }
  return best;
}

// heatLevel maps a value onto the 0-4 colour scale (log scale, so a single
// busy day does not flatten the rest of the year).
function heatLevel(v, max) {
  if (!v) return 0;
  const t = Math.log(1 + v) / Math.log(1 + max);
  return Math.min(4, 1 + Math.floor(t * 4));
}

// metricValue reads one record's value for a heatmap / trend metric.
function metricValue(rec, metric) {
  if (!rec) return 0;
  if (metric === "sessions") return rec.sessions || 0;
  if (metric === "tokens") return rec.tokens || 0;
  if (metric === "cost") return rec.cost || 0;
  return rec.msgs || 0;
}

// metricText formats a metric value for labels and tooltips.
function metricText(metric, v) {
  if (metric === "sessions") return v + " 个会话";
  if (metric === "tokens") return fmtNum(v) + " tokens";
  if (metric === "cost") return "$" + Number(v).toFixed(2);
  return fmtNum(v) + " 条消息";
}

// renderActivity draws a GitHub-style heatmap covering the last `span` days,
// coloured by the selected metric. Cells carry a data-tip for the shared
// tooltip and can be clicked to filter the list to that day.
function renderActivity(el, days, span, metric, withLegend) {
  if (!el) return;
  metric = metric || "msgs";
  const map = new Map();
  for (const d of days || []) map.set(d.day, d);

  const end = new Date();
  end.setHours(0, 0, 0, 0);
  const start = new Date(end);
  start.setDate(start.getDate() - (span - 1));

  let max = 1;
  for (const [k, rec] of map) {
    const d = new Date(k + "T00:00:00");
    if (d >= start && d <= end) max = Math.max(max, metricValue(rec, metric));
  }

  const gridStart = new Date(start);
  gridStart.setDate(gridStart.getDate() - ((gridStart.getDay() + 6) % 7)); // back to Monday

  const cols = [];
  for (const w = new Date(gridStart); w <= end; w.setDate(w.getDate() + 7)) {
    const col = [];
    for (let i = 0; i < 7; i++) {
      const day = new Date(w);
      day.setDate(day.getDate() + i);
      col.push(day);
    }
    cols.push(col);
  }

  let prevMonth = -1;
  const html = cols
    .map((col) => {
      const first = col[0];
      const showMonth = first.getMonth() !== prevMonth && first >= start;
      prevMonth = first.getMonth();
      const label = showMonth ? `<span class="hm-mon">${first.getMonth() + 1}月</span>` : `<span class="hm-mon"></span>`;
      const cells = col
        .map((day) => {
          const key = dayKey(day);
          const rec = map.get(key);
          const outside = day < start || day > end;
          if (outside) return `<span class="hm-cell out"></span>`;
          const v = metricValue(rec, metric);
          const msgs = rec ? rec.msgs || 0 : 0;
          const sessions = rec ? rec.sessions || 0 : 0;
          const detail = `${metricText(metric, v)} · ${msgs} 条 · ${sessions} 个会话`;
          const sel = state.day === key ? " sel" : "";
          return `<span class="hm-cell lv${heatLevel(v, max)}${sel}" data-day="${key}" data-tip="${esc(detail)}"></span>`;
        })
        .join("");
      return `<div class="hm-col">${label}${cells}</div>`;
    })
    .join("");

  let active = 0;
  let totalMsgs = 0;
  for (const [k, rec] of map) {
    const d = new Date(k + "T00:00:00");
    if (d < start || d > end) continue;
    if ((rec.msgs || 0) > 0) active++;
    totalMsgs += rec.msgs || 0;
  }
  const streak = longestStreak(map, start, end);

  el.innerHTML = `<div class="hm">
      <div class="hm-days"><span>一</span><span></span><span>三</span><span></span><span>五</span><span></span><span>日</span></div>
      <div class="hm-cols">${html}</div>
    </div>
    ${withLegend ? `<div class="hm-legend"><span>少</span><i class="lv0"></i><i class="lv1"></i><i class="lv2"></i><i class="lv3"></i><i class="lv4"></i><span>多</span></div>` : ""}`;

  const sub = $("activity-sub");
  if (sub) {
    sub.textContent = `近 ${span} 天 · 活跃 ${active} 天 · 最长连续 ${streak} 天 · ${fmtNum(totalMsgs)} 条消息`;
  }
}

// barRows renders a compact horizontal bar ranking into el.
function barRows(el, rows) {
  el.innerHTML = rows
    .map(
      (r) => `<div class="bar-row"${r.tip ? ` data-tip="${esc(r.tip)}"` : ""}>
        <span class="bar-name" title="${esc(r.title || r.name)}">${esc(r.name)}</span>
        <span class="bar-track"><i class="bar-fill" style="width:${Math.max(3, Math.round(r.pct))}%"></i></span>
        <span class="bar-val">${esc(r.value)}</span>
      </div>`
    )
    .join("");
}

function emptyPanel(el, subId, msg) {
  el.innerHTML = `<div class="mini-empty">${esc(msg)}</div>`;
  const s = $(subId);
  if (s) s.textContent = "";
}

// renderTrend draws a per-day bar chart of the selected metric.
function renderTrend(el, days, span, metric) {
  if (!el) return;
  const map = new Map((days || []).map((d) => [d.day, d]));
  const end = new Date();
  end.setHours(0, 0, 0, 0);
  const start = new Date(end);
  start.setDate(start.getDate() - (span - 1));

  const vals = [];
  let sum = 0;
  for (const d = new Date(start); d <= end; d.setDate(d.getDate() + 1)) {
    const rec = map.get(dayKey(d));
    const v = metricValue(rec, metric);
    vals.push({ day: dayKey(d), v });
    sum += v;
  }
  const max = Math.max(1, ...vals.map((x) => x.v));
  el.innerHTML = `<div class="trend">
      <div class="trend-bars">${vals
        .map(
          (x) =>
            `<i class="trend-bar" style="height:${Math.max(1, Math.round((x.v / max) * 100))}%" data-tip="${esc(x.day + " · " + metricText(metric, x.v))}"></i>`
        )
        .join("")}</div>
      <div class="trend-axis"><span>${esc(dayKey(start))}</span><span>${esc(dayKey(end))}</span></div>
    </div>`;
  const sub = $("trend-sub");
  if (sub) sub.textContent = `近 ${span} 天合计 ${metricText(metric, sum)}`;
}

// renderModels ranks models by token volume.
function renderModels(el, models) {
  if (!el) return;
  const list = models || [];
  if (!list.length) return emptyPanel(el, "models-sub", "暂无模型记录");
  const top = list.slice(0, 8);
  const max = Math.max(...top.map((m) => m.tokens || 1));
  barRows(
    el,
    top.map((m) => ({
      name: m.model || "(未知模型)",
      title: m.model || "(未知模型)",
      pct: (m.tokens / max) * 100,
      value: `${m.sessions} 会话 · ${fmtNum(m.tokens)}`,
      tip: `${m.model || "(未知模型)"} · ${m.sessions} 会话 · ${fmtNum(m.messages)} 消息 · ${fmtNum(m.tokens)} tokens${m.cost ? " · $" + m.cost.toFixed(2) : ""}`,
    }))
  );
  const sub = $("models-sub");
  if (sub) sub.textContent = `${list.length} 个模型`;
}

// renderProjectRanking ranks projects by token volume.
function renderProjectRanking(el, projects) {
  if (!el) return;
  const list = projects || [];
  if (!list.length) return emptyPanel(el, "ranking-sub", "暂无项目");
  const top = list.slice(0, 8);
  const max = Math.max(...top.map((p) => p.tokens || 1));
  barRows(
    el,
    top.map((p) => ({
      name: shortPath(p.cwd),
      title: p.cwd || "(未知目录)",
      pct: (p.tokens / max) * 100,
      value: `${p.count} 会话 · ${fmtNum(p.tokens)}`,
      tip: `${p.cwd || "(未知目录)"} · ${p.count} 会话 · ${fmtNum(p.messages)} 消息 · ${fmtNum(p.tokens)} tokens${p.cost ? " · $" + p.cost.toFixed(2) : ""}`,
    }))
  );
  const sub = $("ranking-sub");
  if (sub) sub.textContent = "按 Token · 当前标签";
}

// renderToolsFull shows the whole tool ranking (not just the project-page top 6).
function renderToolsFull(el, st) {
  if (!el) return;
  const tools = (st && st.tools) || [];
  if (!tools.length) return emptyPanel(el, "tools-sub", "暂无工具记录");
  const total = st.total || tools.reduce((n, t) => n + t.count, 0);
  const max = tools[0].count || 1;
  barRows(
    el,
    tools.slice(0, 20).map((t) => ({
      name: t.tool,
      pct: (t.count / max) * 100,
      value: `${fmtNum(t.count)} · ${total ? Math.round((t.count / total) * 100) : 0}%`,
      tip: `${t.tool} · ${t.count} 次调用 · ${t.sessions} 个会话`,
    }))
  );
  const sub = $("tools-sub");
  if (sub) sub.textContent = `共 ${fmtNum(total)} 次调用`;
}

// renderAgentCompare is the per-agent overview table in the dashboard.
function renderAgentCompare(el, agents) {
  if (!el) return;
  const list = agents || [];
  if (!list.length) return emptyPanel(el, "agent-sub", "暂无数据");
  const max = Math.max(...list.map((a) => a.sessions || 1));
  barRows(
    el,
    list.map((a) => ({
      name: TAB_LABEL[a.agent] || a.agent,
      title: a.agent,
      pct: ((a.sessions || 0) / max) * 100,
      value: `${a.sessions} 会话 · $${(a.cost || 0).toFixed(2)}`,
      tip: `${a.agent} · ${a.sessions} 会话 · ${fmtNum(a.messages)} 消息 · ${fmtNum((a.tokensIn || 0) + (a.tokensOut || 0) + (a.cacheRead || 0))} tokens · $${(a.cost || 0).toFixed(2)}`,
    }))
  );
  const sub = $("agent-sub");
  if (sub) sub.textContent = `${list.length} 个 agent`;
}

async function loadRelations() {
  try {
    state.relations = (await fetchJSON("/api/relations")) || [];
  } catch (e) {
    state.relations = [];
  }
  if (state.current) renderDetailRel(state.current);
}

// loadPanels refreshes the scoped aggregates and repaints whichever panel
// containers are currently visible.
async function loadPanels() {
  const p = scopeParams();
  const [trend, tools, models] = await Promise.all([
    fetchJSON("/api/trend?days=366&" + p).catch(() => []),
    fetchJSON("/api/tools?limit=30&" + p).catch(() => ({ tools: [], total: 0 })),
    fetchJSON("/api/models?" + p).catch(() => []),
  ]);
  state.trend = trend || [];
  state.tools = tools || { tools: [], total: 0 };
  state.models = models || [];
  paintPanels();
}

function paintPanels() {
  renderActivity($("activity-body"), state.trend, state.hmRange, state.hmMetric, true);
  renderTrend($("trend-body"), state.trend, state.hmRange, state.trendMetric);
  renderModels($("models-body"), state.models);
  renderProjectRanking($("ranking-body"), state.projects);
  renderToolsFull($("tools-body"), state.tools);
  renderAgentCompare($("agent-body"), state.agents);
}

/* ---------- view switching ---------- */
function applyView() {
  const dash = state.view === "dash";
  document.querySelectorAll("#view-switch button").forEach((b) => b.classList.toggle("active", b.dataset.view === state.view));
  $("dash").hidden = !dash;
  $("list").hidden = dash;
  $("search").hidden = dash;
  $("sort").hidden = dash;
  if (dash) {
    $("empty").hidden = true;
  } else {
    renderList(state.sessions);
  }
}

function setView(v) {
  if (v !== "list" && v !== "dash") return;
  state.view = v;
  applyView();
  if (v === "dash") loadPanels();
  writeURL();
}

/* ---------- detail ---------- */
const NOISE_ROLES = ["reasoning", "tool"];
const isNoise = (role) => NOISE_ROLES.indexOf(role) >= 0;

// msgBodyHTML renders one transcript row: markdown for conversation content,
// plain pre-wrapped text for tool output and system payloads.
function msgBodyHTML(m) {
  const role = m.role || "system";
  const text = m.text || "";
  if (role === "tool" || role === "system") {
    return `<div class="msg-body">${esc(text)}</div>`;
  }
  const html = window.md ? md.render(text) : esc(text);
  return `<div class="msg-body md">${html}</div>`;
}

function renderMessages(msgs) {
  let prevDay = "";
  const html = msgs
    .map((m, i) => {
      const role = m.role || "system";
      const ts = m.timestamp ? new Date(m.timestamp) : null;
      const valid = ts && !isNaN(ts) && ts.getFullYear() > 2000;
      const day = valid ? dayKey(ts) : "";
      const divider = valid && day !== prevDay ? `<div class="msg-day"><span>${esc(fmtDayLabel(ts))}</span></div>` : "";
      if (valid) prevDay = day;
      const time = valid ? `<span class="msg-time">${esc(fmtClock(ts))}</span>` : "";
      return `${divider}<div class="msg ${esc(role)}" data-i="${i}">
        <div class="msg-role">${esc(role)}${time}</div>
        ${msgBodyHTML(m)}
      </div>`;
    })
    .join("");
  $("detail-body").innerHTML = html;
  $("detail-body").classList.toggle("big", msgs.length > 300);
  buildRail(msgs);
}

// railSnippet flattens a message into a one-line outline entry.
function railSnippet(text) {
  const s = String(text || "")
    .replace(/```[\s\S]*?```/g, " [code] ")
    .replace(/[#>*`|_-]/g, " ")
    .replace(/\s+/g, " ")
    .trim();
  if (!s) return "(空)";
  return s.length > 46 ? s.slice(0, 46) + "…" : s;
}

// buildRail fills the outline rail, skipping messages hidden by the noise
// toggle so every entry maps to a scrollable row.
function buildRail(msgs) {
  const rail = $("rail");
  const list = $("rail-list");
  const items = [];
  msgs.forEach((m, i) => {
    const role = m.role || "system";
    if (!state.noise && isNoise(role)) return;
    items.push({ i, role, text: railSnippet(m.text) });
  });
  if (items.length < 3) {
    rail.hidden = true;
    list.innerHTML = "";
    return;
  }
  rail.hidden = false;
  $("rail-count").textContent = String(items.length);
  list.innerHTML = items
    .map(
      (it) => `<button type="button" class="rail-item ${esc(it.role)}" data-i="${it.i}" title="${esc(it.role)}">
        <span class="rail-dot"></span><span class="rail-text">${esc(it.text)}</span>
      </button>`
    )
    .join("");
  onMsgScroll();
}

function updateRailActive(idx) {
  const list = $("rail-list");
  if (list.hidden) return;
  const next = list.querySelector(`.rail-item[data-i="${idx}"]`);
  const cur = list.querySelector(".rail-item.active");
  if (cur === next) return;
  if (cur) cur.classList.remove("active");
  if (!next) return;
  next.classList.add("active");
  const top = next.offsetTop - list.clientHeight / 2 + next.offsetHeight / 2;
  if (list.scrollTo) list.scrollTo({ top: Math.max(0, top), behavior: scrollBehavior() });
  else list.scrollTop = Math.max(0, top);
}

// onMsgScroll keeps the rail highlight in sync with the reading position: the
// active entry is the message occupying the top of the viewport (or, when
// scrolled to the very bottom, the last message).
// railLock keeps the clicked outline entry highlighted while the transcript
// is scrolled programmatically; it is released by a real user scroll.
function onMsgScroll() {
  if (state.railLock) return;
  const box = $("detail-body");
  const nodes = box.children;
  if (!nodes.length) return;
  const top = box.scrollTop + 48;
  let active = null;
  for (let i = 0; i < nodes.length; i++) {
    const n = nodes[i];
    if (n.dataset.i === undefined) continue; // day divider, not a message
    if (!n.offsetParent) continue; // hidden by the noise toggle
    active = n.dataset.i;
    if (n.offsetTop + n.offsetHeight > top) break;
  }
  if (active !== null) updateRailActive(Number(active));
}

// renderDetailRel links a session to its parent and children, if any.
function renderDetailRel(s) {
  const el = $("detail-rel");
  if (!el) return;
  const parent = s.parentId ? state.relations.find((r) => r.id === s.parentId && r.agent === s.agent) : null;
  const kids = state.relations.filter((r) => r.parentId === s.id && r.agent === s.agent);
  if (!parent && !kids.length) {
    el.hidden = true;
    el.innerHTML = "";
    return;
  }
  const link = (x) =>
    `<button type="button" class="rel-link" data-agent="${esc(x.agent)}" data-id="${esc(x.id)}" title="${esc(x.id)}">${esc(displayTitle(x))}</button>`;
  const parts = [];
  if (parent) {
    // A subagent's parent is its main session; everything else is a lineage.
    parts.push(`<span class="rel-label">${s.relation === "subagent" ? "主会话" : "续接自"}</span>${link(parent)}`);
  }
  if (kids.length) {
    const label = kids.every((k) => k.relation === "subagent") ? `${kids.length} 个 subagent` : `后续 ${kids.length} 个`;
    parts.push(`<span class="rel-label">${label}</span>` + kids.map(link).join("、"));
  }
  el.hidden = false;
  el.innerHTML = parts.join("　·　");
}

function openDetail(s) {
  state.current = s;
  const overlay = $("overlay");
  overlay.hidden = false;
  $("detail-title").innerHTML =
    (s.relation === "subagent"
      ? `<button type="button" class="tag-sub" data-agent="${esc(s.agent)}" data-parent="${esc(s.parentId)}" title="点击查看主会话">subagent</button>`
      : "") + esc(displayTitle(s));
  $("detail-meta").textContent = [s.agent, s.cwd || "(未知目录)", s.model].filter(Boolean).join("  ·  ");
  $("detail-stats").textContent = "加载中…";
  $("detail-body").innerHTML = "";
  $("detail-body").scrollTop = 0;
  state.railLock = false;
  $("rail-list").innerHTML = "";
  $("rail").hidden = true;
  $("detail-body").classList.toggle("show-noise", state.noise);
  renderDetailRel(s);

  fetch(`/api/sessions/${encodeURIComponent(s.agent)}/${encodeURIComponent(s.id)}`)
    .then((r) => r.json())
    .then((msgs) => {
      state.msgs = msgs || [];
      const noise = state.msgs.filter((m) => isNoise(m.role)).length;
      const reasoning = state.msgs.filter((m) => m.role === "reasoning").length;
      const toolMsgs = state.msgs.filter((m) => m.role === "tool").length;
      const talk = state.msgs.length - noise;
      const tokens = (s.tokensIn || 0) + (s.tokensOut || 0) + (s.cacheRead || 0);
      $("detail-stats").textContent = [
        `${talk} 条对话`,
        reasoning ? `${reasoning} 条思考` : "",
        toolMsgs ? `${toolMsgs} 条工具` : "",
        `${fmtNum(tokens)} tokens`,
        s.cost ? `$${Number(s.cost).toFixed(2)}` : "",
        fmtDate(s.createdAt),
        s.id,
      ]
        .filter(Boolean)
        .join("  ·  ");
      renderMessages(state.msgs);
    })
    .catch(() => {
      $("detail-stats").textContent = "加载失败";
    });
}

function closeDetail() {
  $("overlay").hidden = true;
  state.current = null;
}

function debounce(fn, ms) {
  let t;
  return (...a) => {
    clearTimeout(t);
    t = setTimeout(() => fn(...a), ms);
  };
}

/* ---------- events ---------- */
$("tabs").addEventListener("click", async (e) => {
  const b = e.target.closest(".tab");
  if (!b) return;
  document.querySelectorAll(".tab").forEach((t) => t.classList.toggle("active", t === b));
  state.agent = b.dataset.agent;
  await loadProjects();
  await Promise.all([loadStats(), loadSessions(), loadPanels()]);
  writeURL();
});

$("view-switch").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (b) setView(b.dataset.view);
});

/* dashboard controls: heatmap metric / range, trend metric */
function setSeg(sel, btn) {
  document.querySelectorAll(sel + " button").forEach((x) => x.classList.toggle("active", x === btn));
}
$("hm-metric").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.hmMetric = b.dataset.m;
  setSeg("#hm-metric", b);
  renderActivity($("activity-body"), state.trend, state.hmRange, state.hmMetric, true);
});
$("hm-range").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.hmRange = Number(b.dataset.r) || 180;
  setSeg("#hm-range", b);
  paintPanels();
});
$("trend-metric").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.trendMetric = b.dataset.m;
  setSeg("#trend-metric", b);
  renderTrend($("trend-body"), state.trend, state.hmRange, state.trendMetric);
});

// toggleDay filters the list to (or away from) one local day.
function toggleDay(day) {
  if (!day) return;
  state.day = state.day === day ? "" : day;
  renderCrumb();
  if (state.view !== "list") setView("list");
  loadStats();
  loadSessions();
  writeURL();
}

document.addEventListener("click", (e) => {
  const cell = e.target.closest && e.target.closest(".hm-cell[data-day]");
  if (cell) toggleDay(cell.dataset.day);
});

$("projects").addEventListener("click", async (e) => {
  const c = e.target.closest(".proj");
  if (!c) return;
  state.cwd = c.dataset.cwd || "";
  state.day = "";
  renderProjects();
  renderCrumb();
  applyView();
  await Promise.all([loadStats(), loadSessions(), loadPanels()]);
  writeURL();
});

$("project-select").addEventListener("change", async (e) => {
  state.cwd = e.target.value || "";
  state.day = "";
  renderProjects();
  renderCrumb();
  applyView();
  await Promise.all([loadStats(), loadSessions(), loadPanels()]);
  writeURL();
});

$("search").addEventListener("input", debounce((e) => {
  state.q = e.target.value.trim();
  loadSessions();
  writeURL();
}, 250));

$("sort").addEventListener("change", (e) => {
  state.sort = e.target.value;
  loadSessions();
  writeURL();
});

// copyResume copies a session's resume command to the clipboard, with a
// textarea fallback for when clipboard access is denied.
async function copyResume(btn) {
  if (btn.dataset.busy) return;
  const cmd = btn.dataset.cmd || "";
  const ok = await copyText(cmd);
  btn.dataset.busy = "1";
  const old = btn.textContent;
  btn.textContent = ok ? "已复制" : "复制失败";
  setTimeout(() => {
    btn.textContent = old;
    delete btn.dataset.busy;
  }, 1300);
}

// transcriptMarkdown renders a whole conversation as Markdown for export.
function transcriptMarkdown(s, msgs) {
  const meta = [`Agent: ${s.agent}`, `ID: ${s.id}`];
  if (s.cwd) meta.push(`CWD: ${s.cwd}`);
  if (s.model) meta.push(`Model: ${s.model}`);
  if (s.createdAt) meta.push(`Created: ${fmtDate(s.createdAt)}`);
  meta.push(`Updated: ${fmtDate(s.updatedAt)}`);
  const parts = [`# ${displayTitle(s)}`, "", ...meta.map((m) => "- " + m), ""];
  for (const m of msgs) {
    const role = m.role || "system";
    const ts = m.timestamp ? new Date(m.timestamp) : null;
    const when = ts && !isNaN(ts) && ts.getFullYear() > 2000 ? ` — ${fmtDate(ts)}` : "";
    if (role === "tool" || role === "system") {
      parts.push(`## ${role}${when}`, "", "```", m.text || "", "```", "");
    } else {
      parts.push(`## ${role}${when}`, "", m.text || "", "");
    }
  }
  return parts.join("\n");
}

$("detail-export").addEventListener("click", async () => {
  const btn = $("detail-export");
  if (!state.current || !state.msgs.length) return;
  const ok = await copyText(transcriptMarkdown(state.current, state.msgs));
  const old = btn.textContent;
  btn.textContent = ok ? "已复制" : "复制失败";
  setTimeout(() => {
    btn.textContent = old;
  }, 1300);
});

// findSession resolves a session by agent + id from the loaded list or the
// relations payload (parents referenced by a subagent live in the latter).
function findSession(agent, id) {
  return (
    state.sessions.find((x) => x.agent === agent && x.id === id) ||
    state.relations.find((x) => x.agent === agent && x.id === id) ||
    null
  );
}

$("list").addEventListener("click", (e) => {
  const resume = e.target.closest(".card-resume");
  if (resume) {
    copyResume(resume);
    return;
  }
  const c = e.target.closest(".card");
  if (!c) return;
  const s = findSession(c.dataset.agent, c.dataset.id);
  if (s) openDetail(s);
});

// Enter / Space activate a focused card, project row or tree node.
function activateKeys(container, resolve) {
  container.addEventListener("keydown", (e) => {
    if (e.key !== "Enter" && e.key !== " " && e.key !== "Spacebar") return;
    if (e.target.closest("button")) return; // inner buttons handle themselves
    const el = e.target.closest('[role="button"]');
    if (!el) return;
    e.preventDefault();
    resolve(el);
  });
}
activateKeys($("list"), (el) => {
  const s = findSession(el.dataset.agent, el.dataset.id);
  if (s) openDetail(s);
});
activateKeys($("projects"), (el) => el.click());

// From the detail modal, the subagent tag jumps to the main session.
$("detail-title").addEventListener("click", (e) => {
  const tag = e.target.closest(".tag-sub");
  if (!tag || !tag.dataset.parent) return;
  const s = findSession(tag.dataset.agent, tag.dataset.parent);
  if (s) openDetail(s);
});

$("show-noise").addEventListener("change", (e) => {
  state.noise = e.target.checked;
  $("detail-body").classList.toggle("show-noise", state.noise);
  // The rail mirrors what is visible, so rebuild it and re-anchor the highlight.
  if (state.msgs.length) {
    buildRail(state.msgs);
    onMsgScroll();
  }
});

/* outline rail: click to jump, scroll keeps the highlight in sync */
$("rail-list").addEventListener("click", (e) => {
  const b = e.target.closest(".rail-item");
  if (!b) return;
  const box = $("detail-body");
  const el = box.querySelector(`.msg[data-i="${b.dataset.i}"]`);
  if (!el) return;
  // A short message near the end cannot reach the top of the viewport, so
  // trust the click until the user scrolls again.
  state.railLock = true;
  updateRailActive(Number(b.dataset.i));
  const top = Math.max(0, el.offsetTop - 8);
  if (box.scrollTo) box.scrollTo({ top, behavior: scrollBehavior() });
  else box.scrollTop = top;
});
$("detail-body").addEventListener("scroll", onMsgScroll);
["wheel", "touchmove"].forEach((ev) =>
  $("detail-body").addEventListener(ev, () => {
    state.railLock = false;
  }, { passive: true })
);
document.addEventListener("keydown", (e) => {
  if (["PageUp", "PageDown", "Home", "End", "ArrowUp", "ArrowDown", " "].includes(e.key)) state.railLock = false;
});

/* detail cross-links open the target session */
$("detail-rel").addEventListener("click", (e) => {
  const b = e.target.closest(".rel-link");
  if (!b) return;
  const s = state.relations.find((x) => x.agent === b.dataset.agent && x.id === b.dataset.id);
  if (s) openDetail(s);
});

/* copy button on rendered code blocks */
document.addEventListener("click", async (e) => {
  const b = e.target.closest(".code-copy");
  if (!b) return;
  const code = b.parentElement && b.parentElement.querySelector("code");
  if (!code) return;
  try {
    await navigator.clipboard.writeText(code.textContent);
    b.textContent = "已复制";
    setTimeout(() => {
      b.textContent = "复制";
    }, 1200);
  } catch (err) {}
});

/* shared tooltip: heatmap cells and any element opting in via data-tip */
function showTip(tip, e) {
  tip.hidden = false;
  const x = Math.min(e.clientX + 14, window.innerWidth - tip.offsetWidth - 10);
  const y = Math.max(8, e.clientY - tip.offsetHeight - 14);
  tip.style.left = x + "px";
  tip.style.top = y + "px";
}

document.addEventListener("mousemove", (e) => {
  const tip = $("tip");
  const t = e.target;
  if (!t || !t.closest) {
    tip.hidden = true;
    return;
  }
  const cell = t.closest(".hm-cell[data-day]");
  if (cell) {
    tip.innerHTML = `<b>${esc(cell.dataset.day)}</b><br>${esc(cell.dataset.tip || "")}`;
    showTip(tip, e);
    return;
  }
  const el = t.closest("[data-tip]");
  if (el) {
    tip.textContent = el.dataset.tip;
    showTip(tip, e);
    return;
  }
  tip.hidden = true;
});

$("close").addEventListener("click", closeDetail);
$("overlay").addEventListener("click", (e) => {
  if (e.target.id === "overlay") closeDetail();
});
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") closeDetail();
});

$("refresh").addEventListener("click", async () => {
  const btn = $("refresh");
  btn.disabled = true;
  btn.textContent = "扫描中…";
  try {
    await fetch("/api/rescan", { method: "POST" });
    await Promise.all([loadStats(), loadProjects(), loadSessions(), loadPanels(), loadRelations()]);
  } finally {
    btn.disabled = false;
    btn.textContent = "刷新";
  }
});

$("theme-switch").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.theme = b.dataset.pref;
  try {
    localStorage.setItem(THEME_KEY, state.theme);
  } catch (err) {}
  applyTheme();
});

matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
  if (state.theme === "auto") applyTheme();
});

/* ---------- boot ---------- */
applyTheme();
readURL();
$("show-noise").checked = state.noise;
$("sort").value = state.sort;
$("search").value = state.q;
document.querySelectorAll("#tabs .tab").forEach((b) => b.classList.toggle("active", b.dataset.agent === state.agent));
applyView();
// Keep the relative "更新于…" label honest without refetching.
setInterval(renderScanTime, 60000);
loadStats();
loadRelations();
loadProjects().then(async () => {
  await Promise.all([loadSessions(), loadPanels()]);
  writeURL();
});
