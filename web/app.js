// app.js — entry point: cross-feature view switching, event wiring and boot.
//
// The frontend is plain ES modules served straight from the Go binary; there
// is no build step. Feature modules own their own rendering (list, dashboard,
// projects, stats, detail) and share `state` from state.js.

import { $, esc, debounce, notify } from "./util.js";
import { state, readURL, writeURL, applyTheme, setTheme, findSession } from "./state.js";
import { api } from "./api.js";
import { loadStats, renderScanTime } from "./stats.js";
import { loadProjects, renderProjects, renderCrumb } from "./projects.js";
import { loadSessions, renderList, copyResume } from "./list.js";
import { loadPanels, paintPanels, renderActivity, renderTrend } from "./dashboard.js";
import { loadRelations, openDetail } from "./detail.js";

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

/* ---------- shared view events ---------- */
$("crumb").addEventListener("click", (e) => {
  if (!e.target.closest("[data-clear]")) return;
  state.day = "";
  renderCrumb();
  loadStats();
  loadSessions();
  writeURL();
});

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
  renderTrend($("trend-body"), state.trend, state.hmRange, state.hmMetric);
});
$("hm-range").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.hmRange = Number(b.dataset.r) || 180;
  setSeg("#hm-range", b);
  paintPanels();
});
/* comparison tables: a row click switches the agent tab / project scope */
$("agent-body").addEventListener("click", (e) => {
  const tr = e.target.closest("tr[data-agent]");
  if (!tr) return;
  const target = tr.dataset.agent === state.agent ? "" : tr.dataset.agent;
  const tab = document.querySelector(`#tabs .tab[data-agent="${target}"]`);
  if (tab) tab.click();
});
$("ranking-body").addEventListener("click", async (e) => {
  const tr = e.target.closest("tr[data-cwd]");
  if (!tr) return;
  state.cwd = tr.dataset.cwd;
  await scopeChanged();
});

document.addEventListener("click", (e) => {
  const cell = e.target.closest && e.target.closest(".hm-cell[data-day]");
  if (cell) toggleDay(cell.dataset.day);
});

/* project filters */
async function scopeChanged() {
  state.day = "";
  renderProjects();
  renderCrumb();
  applyView();
  await Promise.all([loadStats(), loadSessions(), loadPanels()]);
  writeURL();
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

/* session list */
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

// Enter / Space activate a focused card or project row.
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

/* refresh / theme */
$("refresh").addEventListener("click", async () => {
  const btn = $("refresh");
  btn.disabled = true;
  btn.textContent = "扫描中…";
  try {
    await api.rescan();
    await Promise.all([loadStats(), loadProjects(), loadSessions(), loadPanels(), loadRelations()]);
  } catch (e) {
    notify(e.message);
  } finally {
    btn.disabled = false;
    btn.textContent = "刷新";
  }
});

$("theme-switch").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  setTheme(b.dataset.pref);
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
