// dashboard.js — the "看板" view: heatmap, trend, rankings and the per-agent
// comparison table, plus the scoped aggregate fetches that feed them.

import { $, esc, fmtNum, TAB_LABEL, shortPath, dayKey, notify } from "./util.js";
import { state } from "./state.js";
import { api, scopeParams } from "./api.js";

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
export function renderActivity(el, days, span, metric, withLegend) {
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
export function renderTrend(el, days, span, metric) {
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
export function renderAgentCompare(el, agents) {
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

// settle turns a fetch promise into {ok, v} so one failing panel never blanks
// the others; the failure is surfaced through the shared toast.
function settle(p, label) {
  return p.then(
    (v) => ({ ok: true, v }),
    (e) => {
      notify(label + "加载失败：" + e.message);
      return { ok: false, v: null };
    }
  );
}

// loadPanels refreshes the scoped aggregates and repaints whichever panel
// containers are currently visible.
export async function loadPanels() {
  const scope = scopeParams();
  const trendP = new URLSearchParams(scope);
  trendP.set("days", "366");
  const toolsP = new URLSearchParams(scope);
  toolsP.set("limit", "30");

  const [trend, tools, models] = await Promise.all([
    settle(api.trend(trendP), "趋势"),
    settle(api.tools(toolsP), "工具"),
    settle(api.models(scope), "模型"),
  ]);
  // Keep the previous panel contents when a request fails (rather than
  // flashing "暂无数据") so a transient error does not wipe the view.
  if (trend.ok) state.trend = trend.v || [];
  if (tools.ok) state.tools = tools.v || { tools: [], total: 0 };
  if (models.ok) state.models = models.v || [];
  paintPanels();
}

export function paintPanels() {
  renderActivity($("activity-body"), state.trend, state.hmRange, state.hmMetric, true);
  renderTrend($("trend-body"), state.trend, state.hmRange, state.trendMetric);
  renderModels($("models-body"), state.models);
  renderProjectRanking($("ranking-body"), state.projects);
  renderToolsFull($("tools-body"), state.tools);
  renderAgentCompare($("agent-body"), state.agents);
}
