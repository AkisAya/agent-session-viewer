// projects.js — the project directory sidebar, its filter and the active-day
// chip above the list.

import { $, esc, shortPath, cwdKey, notify } from "./util.js";
import { state } from "./state.js";
import { api } from "./api.js";

let projectsSeq = 0;

export async function loadProjects() {
  const seq = ++projectsSeq;
  const p = new URLSearchParams();
  if (state.agent) p.set("agent", state.agent);
  try {
    const list = (await api.projects(p)) || [];
    if (seq !== projectsSeq) return;
    state.projects = list;
  } catch (e) {
    if (seq !== projectsSeq) return;
    state.projects = [];
    notify(e.message);
  }
  if (state.cwd && !state.projects.some((x) => cwdKey(x.cwd) === cwdKey(state.cwd))) {
    state.cwd = "";
  }
  renderProjects();
  renderCrumb();
}

export function renderProjects() {
  const total = state.projects.reduce((n, x) => n + x.count, 0);
  $("proj-count").textContent = state.projects.length;
  const q = state.projectFilter.trim().toLowerCase();
  const list = (q ? state.projects.filter((p) => (p.cwd || "").toLowerCase().includes(q)) : state.projects).slice();
  const order = state.projectOrder;
  if (order === "name") list.sort((a, b) => shortPath(a.cwd).localeCompare(shortPath(b.cwd)));
  else if (order === "recent") {
    const t = (p) => (p.updatedAt ? new Date(p.updatedAt).getTime() : 0);
    list.sort((a, b) => t(b) - t(a));
  } else list.sort((a, b) => (b.count || 0) - (a.count || 0));
  const items = [
    `<div class="proj ${state.cwd === "" ? "active" : ""}" data-cwd="" tabindex="0" role="button">
       <span class="proj-name">全部项目</span><span class="proj-count">${total}</span>
     </div>`,
  ];
  for (const p of list) {
    const active = cwdKey(state.cwd) === cwdKey(p.cwd) ? "active" : "";
    items.push(
      `<div class="proj ${active}" data-cwd="${esc(cwdKey(p.cwd))}" title="${esc(p.cwd || "(未知目录)")}" tabindex="0" role="button">
         <span class="proj-name">${esc(shortPath(p.cwd))}</span><span class="proj-count">${p.count}</span>
       </div>`
    );
  }
  if (q && !list.length) items.push(`<div class="proj-empty">没有匹配的项目</div>`);
  $("projects").innerHTML = items.join("");

  const sel = $("project-select");
  if (sel) {
    sel.innerHTML =
      `<option value="">全部项目 (${total})</option>` +
      state.projects
        .map((p) => `<option value="${esc(cwdKey(p.cwd))}" ${cwdKey(state.cwd) === cwdKey(p.cwd) ? "selected" : ""}>${esc(shortPath(p.cwd))} (${p.count})</option>`)
        .join("");
  }
}

// renderCrumb shows the active single-day filter (set from the dashboard
// heatmap) as a small chip above the list. The project path is already shown
// in the project header and the sidebar, so it is not repeated here.
export function renderCrumb() {
  const el = $("crumb");
  if (!state.day) {
    el.hidden = true;
    el.innerHTML = "";
    return;
  }
  el.hidden = false;
  el.innerHTML = `<span class="chip">日期 <b>${esc(state.day)}</b><button type="button" data-clear="day" title="清除日期筛选">✕</button></span>`;
}
