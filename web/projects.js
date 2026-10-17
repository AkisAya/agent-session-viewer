// projects.js — the project directory sidebar and the active-day chip.

import { $, esc, shortPath, cwdKey, notify } from "./util.js";
import { state } from "./state.js";
import { api } from "./api.js";

export async function loadProjects() {
  const p = new URLSearchParams();
  if (state.agent) p.set("agent", state.agent);
  try {
    state.projects = (await api.projects(p)) || [];
  } catch (e) {
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
