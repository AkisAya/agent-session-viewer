// stats.js — the top-bar totals, agent tabs and scan status.

import { $, esc, fmtNum, fmtTime, fmtDate, TAB_LABEL, notify } from "./util.js";
import { state } from "./state.js";
import { api, scopeParams } from "./api.js";
import { renderAgentCompare, renderKPI, cacheRate } from "./dashboard.js";

let statsSeq = 0;

export async function loadStats() {
  const seq = ++statsSeq;
  try {
    const d = (await api.stats(scopeParams(state.day ? { day: state.day } : undefined))) || {};
    if (seq !== statsSeq) return;
    const counts = {};
    (d.agents || []).forEach((a) => (counts[a.agent] = a.sessions));
    const t = d.total || {};
    const tin = t.tokensIn || 0;
    const tout = t.tokensOut || 0;
    const cache = t.cacheRead || 0;
    const rate = (cacheRate(t) * 100).toFixed(1);
    $("stats").innerHTML = [
      `<span class="stat">共 <b>${t.sessions || 0}</b> 会话</span>`,
      `<span class="stat">输入 <b class="up">↑${fmtNum(tin)}</b></span>`,
      `<span class="stat">输出 <b class="down">↓${fmtNum(tout)}</b></span>`,
      `<span class="stat" title="缓存读 ${fmtNum(cache)} · 缓存命中率 ${rate}%（缓存读 / (输入 + 缓存读)）">缓存 <b class="cache">⚡${fmtNum(cache)}</b> <b class="cache">${rate}%</b></span>`,
      `<span class="stat">花费 <b>$${(t.cost || 0).toFixed(2)}</b></span>`,
    ].join("");
    state.lastScan = d.lastScan || "";
    state.totalStats = t;
    // The agent breakdown is directory-scoped, so the "全部" tab sums it while
    // the other tabs read their own count.
    const all = (d.agents || []).reduce((n, a) => n + (a.sessions || 0), 0);
    renderTabs(counts, all);
    state.agents = d.agents || [];
    renderAgentCompare($("agent-body"), state.agents);
    renderKPI($("kpi-body"), state.trend, state.totalStats);
    renderScanTime();
  } catch (e) {
    if (seq !== statsSeq) return;
    $("stats").innerHTML = `<span class="stat">统计加载失败</span>`;
    notify(e.message);
  }
}

// renderScanTime shows the scan state when known, otherwise how long ago the
// index was last refreshed.
export function renderScanTime() {
  const el = $("scan-time");
  if (!el) return;
  const st = state.scan;
  if (st && st.scanning) {
    const src = st.source ? ` ${st.source}` : "";
    el.textContent = `扫描中${src} (${st.done}/${st.total})`;
    el.title = "正在增量扫描本机会话";
    return;
  }
  if (st && st.lastError) {
    el.textContent = "上次扫描失败";
    el.title = st.lastError;
    return;
  }
  const last = (st && st.lastScan) || state.lastScan;
  if (!last || String(last).startsWith("0001")) {
    el.textContent = "";
    el.title = "";
    return;
  }
  el.textContent = "更新于 " + fmtTime(last);
  el.title = "上次扫描：" + fmtDate(last);
}

function renderTabs(counts, total) {
  document.querySelectorAll("#tabs .tab").forEach((b) => {
    const a = b.dataset.agent;
    const n = a ? counts[a] || 0 : total;
    b.innerHTML = esc(TAB_LABEL[a]) + `<span class="n">${n}</span>`;
  });
}
