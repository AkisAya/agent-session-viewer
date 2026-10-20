// app.js — entry point: cross-feature view switching, URL/history, scan and
// auto-refresh state, keyboard shortcuts and boot.
//
// The frontend is plain ES modules served straight from the Go binary; there
// is no build step. Feature modules own their own rendering (list, dashboard,
// projects, stats, detail) and share `state` from state.js.

import { $, esc, debounce, notify, toCSV, download, store, copyText } from "./util.js";
import { state, readURL, writeURL, applyTheme, setTheme, findSession, toggleStar } from "./state.js";
import { api, scopeParams } from "./api.js";
import { loadStats, renderScanTime } from "./stats.js";
import { loadProjects, renderProjects, renderCrumb } from "./projects.js";
import { loadSessions, renderList, copyResume, exportCSV } from "./list.js";
import { loadPanels, paintPanels, loadTop, syncDashboardControls } from "./dashboard.js";
import { loadRelations, openDetail, closeDetail, jumpUser } from "./detail.js";

/* ---------- view switching ---------- */
function applyView() {
  const dash = state.view === "dash";
  document.querySelectorAll("#view-switch button").forEach((b) => b.classList.toggle("active", b.dataset.view === state.view));
  $("dash").hidden = !dash;
  $("list").hidden = dash;
  $("search").hidden = dash;
  $("sort").hidden = dash;
  if ($("view-mode")) $("view-mode").hidden = dash;
  if ($("sub-toggle")) $("sub-toggle").hidden = dash;
  if (dash) {
    $("empty").hidden = true;
    if ($("list-info")) $("list-info").hidden = true;
    if ($("load-more")) $("load-more").hidden = true;
  } else {
    renderList(state.sessions);
  }
}

function setView(v, opts = {}) {
  if (v !== "list" && v !== "dash") return;
  state.view = v;
  applyView();
  if (v === "dash") loadPanels();
  if (!opts.fromURL) writeURL(true);
}

// toggleDay filters the list to (or away from) one local day.
function toggleDay(day) {
  if (!day) return;
  state.day = state.day === day ? "" : day;
  renderCrumb();
  if (state.view !== "list") setView("list", { fromURL: true });
  loadStats();
  loadSessions();
  writeURL(true);
}

/* ---------- shared view events ---------- */
$("crumb").addEventListener("click", (e) => {
  if (!e.target.closest("[data-clear]")) return;
  state.day = "";
  renderCrumb();
  loadStats();
  loadSessions();
  writeURL(true);
});

$("tabs").addEventListener("click", async (e) => {
  const b = e.target.closest(".tab");
  if (!b) return;
  document.querySelectorAll(".tab").forEach((t) => t.classList.toggle("active", t === b));
  state.agent = b.dataset.agent;
  state.cursor = -1;
  await loadProjects();
  await Promise.all([loadStats(), loadSessions(), loadPanels()]);
  writeURL(true);
});

$("view-switch").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (b) setView(b.dataset.view);
});

$("view-mode").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.mode = b.dataset.mode === "table" ? "table" : "cards";
  store.set("as-mode", state.mode);
  syncControls();
  renderList(state.sessions);
});

$("show-subagents").addEventListener("change", (e) => {
  state.showSubagents = e.target.checked;
  store.set("as-subagents", state.showSubagents);
  renderList(state.sessions);
});

/* dashboard controls */
function setSeg(sel, btn) {
  document.querySelectorAll(sel + " button").forEach((x) => x.classList.toggle("active", x === btn));
}
$("hm-metric").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.hmMetric = b.dataset.m;
  store.set("as-hm-metric", state.hmMetric);
  setSeg("#hm-metric", b);
  paintPanels();
});
$("rhythm-metric").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.rhythmMetric = b.dataset.m;
  store.set("as-rhythm-metric", state.rhythmMetric);
  paintPanels();
});
$("hm-range").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.hmRange = Number(b.dataset.r) || 180;
  store.set("as-hm-range", state.hmRange);
  setSeg("#hm-range", b);
  paintPanels();
});
$("models-metric").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.modelsMetric = b.dataset.m;
  store.set("as-models-metric", state.modelsMetric);
  paintPanels();
});
$("tools-metric").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.toolsMetric = b.dataset.m;
  store.set("as-tools-metric", state.toolsMetric);
  paintPanels();
});
$("top-metric").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.topMetric = b.dataset.m;
  store.set("as-top-metric", state.topMetric);
  syncDashboardControls();
  loadTop();
});
$("models-all").addEventListener("click", () => {
  state.showAllModels = !state.showAllModels;
  paintPanels();
});
$("tools-all").addEventListener("click", () => {
  state.showAllTools = !state.showAllTools;
  paintPanels();
});
$("ranking-all").addEventListener("click", () => {
  state.showAllProjects = !state.showAllProjects;
  store.set("as-all-projects", state.showAllProjects);
  paintPanels();
});

// comparison tables: a row click switches the agent tab / project scope.
$("agent-body").addEventListener("click", (e) => {
  const sort = e.target.closest(".th-sort");
  if (sort) {
    sortTable("agent", sort.dataset.sortKey);
    return;
  }
  const tr = e.target.closest("tr[data-agent]");
  if (!tr) return;
  const target = tr.dataset.agent === state.agent ? "" : tr.dataset.agent;
  const tab = document.querySelector(`#tabs .tab[data-agent="${target}"]`);
  if (tab) tab.click();
});
$("ranking-body").addEventListener("click", async (e) => {
  const sort = e.target.closest(".th-sort");
  if (sort) {
    sortTable("project", sort.dataset.sortKey);
    return;
  }
  const tr = e.target.closest("tr[data-cwd]");
  if (!tr) return;
  state.cwd = tr.dataset.cwd;
  await scopeChanged();
});

function sortTable(table, key) {
  const s = table === "agent" ? state.agentSort : state.projectSort;
  if (s.key === key) s.dir = -s.dir;
  else {
    s.key = key;
    s.dir = key === "agent" || key === "cwd" ? 1 : -1;
  }
  paintPanels();
}

document.addEventListener("click", (e) => {
  const b = e.target.closest(".hm-cell[data-day]");
  if (b) toggleDay(b.dataset.day);
});

// Enter / Space on a heatmap cell behaves like a click.
document.addEventListener("keydown", (e) => {
  if (e.key !== "Enter" && e.key !== " ") return;
  const b = e.target.closest && e.target.closest(".hm-cell[data-day]");
  if (!b) return;
  e.preventDefault();
  toggleDay(b.dataset.day);
});

/* project filters */
async function scopeChanged() {
  state.day = "";
  state.cursor = -1;
  renderProjects();
  renderCrumb();
  applyView();
  await Promise.all([loadStats(), loadSessions(), loadPanels()]);
  writeURL(true);
}

$("projects").addEventListener("click", async (e) => {
  const c = e.target.closest(".proj");
  if (!c) return;
  state.cwd = c.dataset.cwd || "";
  await scopeChanged();
});

$("project-select").addEventListener("change", async (e) => {
  state.cwd = e.target.value || "";
  await scopeChanged();
});

$("project-search").addEventListener("input", debounce((e) => {
  state.projectFilter = e.target.value.trim();
  renderProjects();
}, 150));

$("project-sort").addEventListener("change", (e) => {
  state.projectOrder = e.target.value;
  store.set("as-project-order", state.projectOrder);
  renderProjects();
});

$("search").addEventListener("input", debounce((e) => {
  state.q = e.target.value.trim();
  state.cursor = -1;
  loadSessions();
  writeURL(false);
}, 250));

$("sort").addEventListener("change", (e) => {
  state.sort = e.target.value;
  state.cursor = -1;
  loadSessions();
  writeURL(false);
});

/* session list */
$("list").addEventListener("click", (e) => {
  const cwd = e.target.closest(".card-cwd-copy");
  if (cwd) {
    copyText(cwd.dataset.cwd || "").then((ok) => {
      notify(ok ? "目录已复制" : "复制失败");
    });
    return;
  }
  const star = e.target.closest(".card-star");
  if (star) {
    const at = star.dataset.star.indexOf("|");
    if (at > 0) {
      toggleStar(star.dataset.star.slice(0, at), star.dataset.star.slice(at + 1));
      renderList(state.sessions);
    }
    return;
  }
  const resume = e.target.closest(".card-resume");
  if (resume) {
    copyResume(resume);
    return;
  }
  const c = e.target.closest(".card, .session-row");
  if (!c) return;
  const s = findSession(c.dataset.agent, c.dataset.id);
  if (s) openDetail(s);
});

$("empty").addEventListener("click", (e) => {
  if (e.target.closest("[data-clear-all]")) {
    clearFilters();
    return;
  }
  if (e.target.closest("[data-retry]")) reloadCurrent();
});

async function clearFilters() {
  state.agent = "";
  state.cwd = "";
  state.q = "";
  state.day = "";
  state.cursor = -1;
  syncControls();
  renderCrumb();
  await Promise.all([loadProjects(), loadStats()]);
  await loadSessions();
  writeURL(true);
}

$("load-more").addEventListener("click", async () => {
  const btn = $("load-more");
  btn.disabled = true;
  try {
    await loadSessions(false);
  } finally {
    btn.disabled = false;
  }
});

$("top-body").addEventListener("click", (e) => {
  const row = e.target.closest(".top-row");
  if (!row) return;
  const s =
    findSession(row.dataset.agent, row.dataset.id) ||
    (state.top || []).find((x) => x.agent === row.dataset.agent && x.id === row.dataset.id);
  if (s) openDetail(s);
});

// Enter / Space activate a focused card, project row, session row or top row.
function activateKeys(container, resolve) {
  container.addEventListener("keydown", (e) => {
    if (e.key !== "Enter" && e.key !== " " && e.key !== "Spacebar") return;
    if (e.target.closest("button")) return;
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
activateKeys($("top-body"), (el) => {
  const s =
    findSession(el.dataset.agent, el.dataset.id) ||
    (state.top || []).find((x) => x.agent === el.dataset.agent && x.id === el.dataset.id);
  if (s) openDetail(s);
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

/* refresh / scan */
let scanTimer = 0;
let wasScanning = false;
let scanBusyUntil = 0;
let errShown = "";
let seenLastScan = "";
let lastErrSeen = "";

function setRefreshBusy(busy) {
  const btn = $("refresh");
  if (!btn) return;
  btn.disabled = busy;
  btn.textContent = busy ? "扫描中…" : "刷新";
}

async function refreshData() {
  await Promise.all([loadStats(), loadProjects(), loadRelations()]);
  await Promise.all([loadSessions(true, { quiet: true }), loadPanels()]);
}

function reloadCurrent() {
  refreshData();
}

// exportAllCSV fetches every page of the current filter (up to 10k rows) so the
// download is not limited to what happens to be rendered.
async function exportAllCSV() {
  const base = new URLSearchParams();
  if (state.agent) base.set("agent", state.agent);
  if (state.cwd) base.set("cwd", state.cwd);
  if (state.q) base.set("q", state.q);
  if (state.day) base.set("day", state.day);
  base.set("sort", state.sort);
  // shortcut: capped at 10k rows; stream the export if larger libraries matter.
  const all = [];
  for (let offset = 0; offset < 10000; ) {
    base.set("limit", "1000");
    base.set("offset", String(offset));
    const page = (await api.sessions(base)) || {};
    const rows = page.sessions || [];
    all.push(...rows);
    offset += rows.length;
    if (!rows.length || offset >= (page.total || 0)) break;
  }
  const { head, rows } = exportCSV(all);
  const day = new Date().toISOString().slice(0, 10);
  download(`asv-sessions-${day}.csv`, "\ufeff" + toCSV(head, rows), "text/csv;charset=utf-8");
  notify(`已导出 ${all.length} 个会话`);
}

$("export-csv").addEventListener("click", async () => {
  const btn = $("export-csv");
  btn.disabled = true;
  try {
    await exportAllCSV();
  } catch (e) {
    notify("导出失败：" + e.message);
  } finally {
    btn.disabled = false;
  }
});

async function pollScan(delay = 0) {
  clearTimeout(scanTimer);
  if (delay) {
    scanTimer = setTimeout(() => pollScan(), delay);
    return;
  }
  try {
    const st = await api.scan();
    const firstStatus = state.scan === null;
    state.scan = st;
    renderScanTime();
    if (firstStatus && !st.scanning && !state.sessions.length && state.view === "list") {
      renderList(state.sessions);
    }
    if (st.scanning) {
      wasScanning = true;
      setRefreshBusy(true);
      if (!state.sessions.length && state.view === "list") renderList(state.sessions);
      scanTimer = setTimeout(() => pollScan(), 1000);
      return;
    }
    if (Date.now() < scanBusyUntil) {
      // The POST may return just before the worker sets its flag.
      scanTimer = setTimeout(() => pollScan(), 400);
      return;
    }
    setRefreshBusy(false);
    if (st.lastError && st.lastError !== errShown) {
      errShown = st.lastError;
      notify("扫描失败：" + st.lastError);
    } else if (!st.lastError) {
      errShown = "";
    }
    const scanChanged = !!st.lastScan && st.lastScan !== seenLastScan;
    const errChanged = !!st.lastError && st.lastError !== lastErrSeen;
    lastErrSeen = st.lastError || "";
    if (wasScanning || scanChanged || errChanged) {
      wasScanning = false;
      seenLastScan = st.lastScan || seenLastScan;
      await refreshData();
    } else if (st.lastScan) {
      seenLastScan = st.lastScan;
    }
    scanTimer = setTimeout(() => pollScan(), 20000);
  } catch (e) {
    scanTimer = setTimeout(() => pollScan(), 5000);
  }
}

$("refresh").addEventListener("click", async () => {
  scanBusyUntil = Date.now() + 2000;
  setRefreshBusy(true);
  try {
    await api.rescan();
    pollScan();
  } catch (e) {
    notify(e.message);
    setRefreshBusy(false);
  }
});

/* auto scan: every 30s rescan the sources and refresh the current view */
let autoTimer = 0;

async function autoScanTick() {
  if (state.scan && state.scan.scanning) return;
  scanBusyUntil = Date.now() + 2000;
  try {
    await api.rescan();
    pollScan();
  } catch (e) {}
}

function setAutoRefresh(on, immediate) {
  state.autoRefresh = !!on;
  store.set("as-auto", state.autoRefresh);
  const btn = $("auto-refresh");
  if (btn) {
    btn.classList.toggle("active", state.autoRefresh);
    btn.setAttribute("aria-pressed", state.autoRefresh ? "true" : "false");
    btn.title = state.autoRefresh
      ? "自动刷新已开启：每 30 秒重新扫描并刷新页面（服务端 -watch-interval 会独立扫描）"
      : "开启后每 30 秒重新扫描并刷新页面；已用 -watch-interval 时通常不需要再开";
  }
  clearInterval(autoTimer);
  if (state.autoRefresh) {
    if (immediate) autoScanTick();
    autoTimer = setInterval(autoScanTick, 30000);
  }
}

$("auto-refresh").addEventListener("click", () => setAutoRefresh(!state.autoRefresh, true));

/* theme */
$("theme-switch").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  setTheme(b.dataset.pref);
});

matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
  if (state.theme === "auto") applyTheme();
});

/* ---------- keyboard ---------- */
const typing = (e) => {
  const t = e.target;
  return t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable);
};

function moveCursor(delta) {
  const nodes = Array.from($("list").querySelectorAll(".card[data-id], .session-row[data-id]")).filter((n) => n.offsetParent !== null);
  if (!nodes.length) return;
  state.cursor = Math.max(0, Math.min(nodes.length - 1, state.cursor + delta));
  nodes.forEach((n) => n.classList.remove("sel"));
  const el = nodes[state.cursor];
  el.classList.add("sel");
  if (el.scrollIntoView) el.scrollIntoView({ block: "nearest" });
}

function openCursor() {
  if (state.cursor < 0) return;
  const nodes = Array.from($("list").querySelectorAll(".card[data-id], .session-row[data-id]")).filter((n) => n.offsetParent !== null);
  const el = nodes[state.cursor];
  if (!el) return;
  const s = findSession(el.dataset.agent, el.dataset.id);
  if (s) openDetail(s);
}

function adjacentSession(dir) {
  const list = state.sessions.filter((s) => state.showSubagents || s.relation !== "subagent");
  if (!list.length) return;
  let i = state.current ? list.findIndex((s) => s.agent === state.current.agent && s.id === state.current.id) : -1;
  if (i < 0) i = dir > 0 ? -1 : list.length;
  const next = list[(i + dir + list.length) % list.length];
  if (next) openDetail(next);
}

function showHelp() {
  $("help").hidden = false;
  const c = $("help-close");
  if (c && c.focus) c.focus();
}
function hideHelp() {
  $("help").hidden = true;
}

$("help-close").addEventListener("click", hideHelp);
$("help").addEventListener("click", (e) => {
  if (e.target.id === "help") hideHelp();
});

document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") {
    if (!$("help").hidden) {
      hideHelp();
      return;
    }
    if (!$("overlay").hidden) {
      closeDetail();
      return;
    }
    if (document.activeElement === $("search") && $("search").value) {
      $("search").value = "";
      state.q = "";
      loadSessions();
      writeURL(false);
    }
    return;
  }
  if (typing(e)) return;
  if (!$("help").hidden) return;
  if (!$("overlay").hidden) {
    if (e.key === "n") jumpUser(1);
    if (e.key === "p") jumpUser(-1);
    if (e.key === "f") $("detail-find-toggle").click();
    if (e.key === "[") adjacentSession(-1);
    if (e.key === "]") adjacentSession(1);
    return;
  }
  if (e.key === "?") {
    e.preventDefault();
    showHelp();
    return;
  }
  if (state.view !== "list") return;
  if (e.key === "j") {
    e.preventDefault();
    moveCursor(1);
  } else if (e.key === "k") {
    e.preventDefault();
    moveCursor(-1);
  } else if (e.key === "Enter") {
    openCursor();
  } else if (e.key === "[") {
    adjacentSession(-1);
  } else if (e.key === "]") {
    adjacentSession(1);
  }
});

/* ---------- url / history ---------- */
function syncControls() {
  document.querySelectorAll("#tabs .tab").forEach((b) => b.classList.toggle("active", b.dataset.agent === state.agent));
  document.querySelectorAll("#view-switch button").forEach((b) => b.classList.toggle("active", b.dataset.view === state.view));
  document.querySelectorAll("#view-mode button").forEach((b) => b.classList.toggle("active", b.dataset.mode === state.mode));
  $("sort").value = state.sort;
  $("search").value = state.q;
  $("project-search").value = state.projectFilter;
  $("project-sort").value = state.projectOrder;
  $("show-subagents").checked = state.showSubagents;
  $("show-noise").checked = state.noise;
  $("auto-refresh").classList.toggle("active", state.autoRefresh);
  $("auto-refresh").setAttribute("aria-pressed", state.autoRefresh ? "true" : "false");
  renderProjects();
}

async function openPendingSession() {
  const p = state.pendingSession;
  state.pendingSession = null;
  if (!p) {
    if (!$("overlay").hidden) closeDetail({ fromURL: true });
    return;
  }
  if (state.current && state.current.agent === p.agent && state.current.id === p.id) return;
  const s = await resolveSession(p.agent, p.id);
  if (s) openDetail(s, { fromURL: true });
  else notify("会话不存在，或尚未被索引");
}

// resolveSession falls back to the metadata endpoint so a deep link can open a
// session outside the current page or filter.
async function resolveSession(agent, id) {
  const s = findSession(agent, id);
  if (s) return s;
  try {
    return await api.sessionMeta(agent, id);
  } catch (e) {
    return null;
  }
}

async function applyURLState() {
  readURL();
  syncControls();
  applyView();
  await Promise.all([loadStats(), loadProjects()]);
  await Promise.all([loadSessions(), loadPanels()]);
  await openPendingSession();
}

window.addEventListener("popstate", () => {
  applyURLState();
});

// Keep the relative "更新于…" label honest without refetching.
setInterval(renderScanTime, 60000);

let versionLoaded = false;
function loadVersion() {
  if (versionLoaded) return;
  versionLoaded = true;
  api
    .version()
    .then((d) => {
      const v = d && d.version && d.version !== "dev" ? "v" + String(d.version).replace(/^v/, "") : "";
      if (v) $("version").textContent = v;
    })
    .catch(() => {});
}

/* ---------- boot ---------- */
applyTheme();
readURL();
syncControls();
applyView();
setAutoRefresh(state.autoRefresh);
loadVersion();
loadStats();
loadRelations().then(() => {
  if (state.view === "list" && state.sessions.length) renderList(state.sessions);
});
loadProjects().then(async () => {
  await Promise.all([loadSessions(), loadPanels()]);
  await openPendingSession();
  writeURL(false);
});
pollScan();
