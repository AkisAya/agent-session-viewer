// stats.js — the top-bar totals plus the agent tabs they update.

import { $, esc, fmtNum, fmtTime, fmtDate, TAB_LABEL, notify } from "./util.js";
import { state } from "./state.js";
import { api, scopeParams } from "./api.js";
import { renderAgentCompare, renderKPI, cacheRate } from "./dashboard.js";

export async function loadStats() {
  try {
    const d = await api.stats(scopeParams(state.day ? { day: state.day } : undefined));
    const counts = {};
    (d.agents || []).forEach((a) => (counts[a.agent] = a.sessions));
    const t = d.total || {};
    const tin = t.tokensIn || 0;
    const tout = t.tokensOut || 0;
    const cache = t.cacheRead || 0;
    const rate = (cacheRate(t) * 100).toFixed(1);
    $("stats").innerHTML = [
      `<span class="stat">共 <b>${t.sessions || 0}</b> 会话</span>`,
      `<span class="stat" title="输入 ${fmtNum(tin)} · 输出 ${fmtNum(tout)} · 缓存读 ${fmtNum(cache)} · 缓存命中率 ${rate}%（缓存读 / (输入 + 缓存读)）">Tokens <b class="up">↑${fmtNum(tin)}</b> <b class="down">↓${fmtNum(tout)}</b> <b class="cache">⚡${fmtNum(cache)}</b> <b class="cache">${rate}%</b></span>`,
      `<span class="stat">花费 <b>$${(t.cost || 0).toFixed(2)}</b></span>`,
    ].join("");
    state.lastScan = d.lastScan || "";
    renderScanTime();
    // The agent breakdown is directory-scoped, so the "全部" tab sums it while
    // the other tabs read their own count.
    const all = (d.agents || []).reduce((n, a) => n + (a.sessions || 0), 0);
    renderTabs(counts, all);
    state.agents = d.agents || [];
    state.total = t;
    renderAgentCompare($("agent-body"), state.agents);
    renderKPI($("kpi-body"), state.trend, state.total);
  } catch (e) {
    $("stats").innerHTML = `<span class="stat">统计加载失败</span>`;
    notify(e.message);
  }
}

// renderScanTime shows how long ago the index was last refreshed.
export function renderScanTime() {
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
