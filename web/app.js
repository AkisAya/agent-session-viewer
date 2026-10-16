const state = { agent: "", cwd: "", q: "", sort: "updated", projects: [], sessions: [], theme: "auto", noise: false };

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

const TAB_LABEL = { "": "全部", claude: "Claude", codex: "Codex", opencode: "OpenCode" };
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/* ---------- theme ---------- */
const THEME_KEY = "as-theme";
state.theme = localStorage.getItem(THEME_KEY) || "auto";

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
    const d = await (await fetch("/api/stats")).json();
    const counts = {};
    (d.agents || []).forEach((a) => (counts[a.agent] = a.sessions));
    const t = d.total || {};
    const tokens = (t.tokensIn || 0) + (t.tokensOut || 0) + (t.cacheRead || 0);
    $("stats").innerHTML = [
      `<span class="stat">共 <b>${t.sessions || 0}</b> 会话</span>`,
      `<span class="stat">Tokens <b>${fmtNum(tokens)}</b></span>`,
      `<span class="stat">花费 <b>$${(t.cost || 0).toFixed(2)}</b></span>`,
    ].join("");
    renderTabs(counts, t.sessions || 0);
  } catch (e) {
    $("stats").innerHTML = `<span class="stat">统计加载失败</span>`;
  }
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
  state.projects = (await (await fetch("/api/projects?" + p)).json()) || [];
  if (state.cwd && !state.projects.some((x) => cwdKey(x.cwd) === cwdKey(state.cwd))) {
    state.cwd = "";
  }
  renderProjects();
  renderCrumb();
}

function renderProjects() {
  const total = state.projects.reduce((n, x) => n + x.count, 0);
  const items = [
    `<div class="proj ${state.cwd === "" ? "active" : ""}" data-cwd="">
       <span class="proj-name">全部项目</span><span class="proj-count">${total}</span>
     </div>`,
  ];
  for (const p of state.projects) {
    const active = cwdKey(state.cwd) === cwdKey(p.cwd) ? "active" : "";
    items.push(
      `<div class="proj ${active}" data-cwd="${esc(cwdKey(p.cwd))}" title="${esc(p.cwd || "(未知目录)")}">
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

function renderCrumb() {
  const el = $("crumb");
  if (!state.cwd) {
    el.hidden = true;
    el.innerHTML = "";
    return;
  }
  el.hidden = false;
  el.innerHTML = `项目：<b>${esc(state.cwd === "__none__" ? "(未知目录)" : state.cwd)}</b> <button id="clear-proj">清除</button>`;
  $("clear-proj").addEventListener("click", () => {
    state.cwd = "";
    renderProjects();
    renderCrumb();
    loadSessions();
  });
}

/* ---------- session list ---------- */
function showEmpty(text, hint) {
  $("list").innerHTML = "";
  $("empty").innerHTML = esc(text) + (hint ? `<div class="hint">${esc(hint)}</div>` : "");
  $("empty").hidden = false;
}

async function loadSessions() {
  showEmpty("加载中…");
  const p = new URLSearchParams();
  if (state.agent) p.set("agent", state.agent);
  if (state.cwd) p.set("cwd", state.cwd);
  if (state.q) p.set("q", state.q);
  p.set("sort", state.sort);
  const rows = (await (await fetch("/api/sessions?" + p)).json()) || [];
  state.sessions = rows;
  renderList(rows);
}

function renderList(rows) {
  if (!rows.length) {
    const hint = state.q || state.cwd ? "试试清除搜索或项目筛选" : "点击右上角「刷新」重新扫描";
    showEmpty("没有匹配的会话", hint);
    return;
  }
  $("empty").hidden = true;
  const showCwd = !state.cwd;
  $("list").innerHTML = rows
    .map((s) => {
      const tokens = (s.tokensIn || 0) + (s.tokensOut || 0) + (s.cacheRead || 0);
      const title = displayTitle(s);
      const previewText = (s.preview || "").trim();
      const showPreview = previewText && previewText !== title;
      return `<div class="card agent-${esc(s.agent)}" data-agent="${esc(s.agent)}" data-id="${esc(s.id)}">
        <div class="card-title" title="${esc(title)}">${hl(title, state.q)}</div>
        ${showPreview ? `<div class="card-preview">${hl(previewText, state.q)}</div>` : ""}
        ${showCwd ? `<div class="card-cwd" title="${esc(s.cwd)}">${esc(s.cwd || "(未知目录)")}</div>` : ""}
        <div class="card-foot">
          <span class="badge ${esc(s.agent)}">${esc(s.agent)}</span>
          <span>${s.msgCount || 0} 条</span>
          <span class="tok">${fmtNum(tokens)} tokens</span>
          ${s.cost ? `<span>$${Number(s.cost).toFixed(2)}</span>` : ""}
          <span class="time" title="${esc(fmtDate(s.updatedAt))}">${fmtTime(s.updatedAt)}</span>
        </div>
      </div>`;
    })
    .join("");
}

/* ---------- detail ---------- */
function openDetail(s) {
  const overlay = $("overlay");
  overlay.hidden = false;
  $("detail-title").textContent = displayTitle(s);
  $("detail-meta").textContent = [s.agent, s.cwd || "(未知目录)", s.model].filter(Boolean).join("  ·  ");
  $("detail-stats").textContent = "加载中…";
  $("detail-body").innerHTML = "";
  $("detail-body").classList.toggle("show-noise", state.noise);

  fetch(`/api/sessions/${encodeURIComponent(s.agent)}/${encodeURIComponent(s.id)}`)
    .then((r) => r.json())
    .then((msgs) => {
      const noise = msgs.filter((m) => m.role === "reasoning" || m.role === "tool").length;
      const talk = msgs.length - noise;
      const tokens = (s.tokensIn || 0) + (s.tokensOut || 0) + (s.cacheRead || 0);
      $("detail-stats").textContent = [
        `${talk} 条对话`,
        noise ? `${noise} 条思考/工具` : "",
        `${fmtNum(tokens)} tokens`,
        s.cost ? `$${Number(s.cost).toFixed(2)}` : "",
        fmtDate(s.createdAt),
        s.id,
      ]
        .filter(Boolean)
        .join("  ·  ");
      $("detail-body").innerHTML = msgs
        .map(
          (m) => `<div class="msg ${esc(m.role)}">
            <div class="msg-role">${esc(m.role)}</div>
            <div class="msg-body">${esc(m.text)}</div>
          </div>`
        )
        .join("");
    })
    .catch(() => {
      $("detail-stats").textContent = "加载失败";
    });
}

function closeDetail() {
  $("overlay").hidden = true;
}

function debounce(fn, ms) {
  let t;
  return (...a) => {
    clearTimeout(t);
    t = setTimeout(() => fn(...a), ms);
  };
}

/* ---------- events ---------- */
$("tabs").addEventListener("click", (e) => {
  const b = e.target.closest(".tab");
  if (!b) return;
  document.querySelectorAll(".tab").forEach((t) => t.classList.toggle("active", t === b));
  state.agent = b.dataset.agent;
  Promise.all([loadProjects(), loadSessions()]);
});

$("projects").addEventListener("click", (e) => {
  const c = e.target.closest(".proj");
  if (!c) return;
  state.cwd = c.dataset.cwd || "";
  renderProjects();
  renderCrumb();
  loadSessions();
});

$("project-select").addEventListener("change", (e) => {
  state.cwd = e.target.value || "";
  renderProjects();
  renderCrumb();
  loadSessions();
});

$("search").addEventListener("input", debounce((e) => {
  state.q = e.target.value.trim();
  loadSessions();
}, 250));

$("sort").addEventListener("change", (e) => {
  state.sort = e.target.value;
  loadSessions();
});

$("list").addEventListener("click", (e) => {
  const c = e.target.closest(".card");
  if (c) {
    const s = state.sessions.find((x) => x.agent === c.dataset.agent && x.id === c.dataset.id);
    if (s) openDetail(s);
  }
});

$("show-noise").addEventListener("change", (e) => {
  state.noise = e.target.checked;
  $("detail-body").classList.toggle("show-noise", state.noise);
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
    await Promise.all([loadStats(), loadProjects(), loadSessions()]);
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
$("show-noise").checked = state.noise;
loadStats();
loadProjects().then(loadSessions);
