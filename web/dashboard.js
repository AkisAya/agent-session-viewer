// dashboard.js — the "看板" view: heatmap, trend, rankings and the per-agent
// comparison table, plus the scoped aggregate fetches that feed them.

import { $, esc, fmtNum, fmtTime, TAB_LABEL, shortPath, cwdKey, dayKey, notify } from "./util.js";
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
  let totalMetric = 0;
  let peak = null;
  for (const [k, rec] of map) {
    const d = new Date(k + "T00:00:00");
    if (d < start || d > end) continue;
    if ((rec.msgs || 0) > 0) active++;
    const v = metricValue(rec, metric);
    totalMetric += v;
    if (v > 0 && (!peak || v > peak.v)) peak = { day: k, v };
  }
  const streak = longestStreak(map, start, end);

  el.innerHTML = `<div class="hm">
      <div class="hm-days"><span>一</span><span></span><span>三</span><span></span><span>五</span><span></span><span>日</span></div>
      <div class="hm-cols">${html}</div>
    </div>
    ${withLegend ? `<div class="hm-legend"><span>少</span><i class="lv0"></i><i class="lv1"></i><i class="lv2"></i><i class="lv3"></i><i class="lv4"></i><span>多</span></div>` : ""}`;

  const sub = $("activity-sub");
  if (sub) {
    // Per-day session counts overlap across days, so their sum is meaningless.
    const parts = [`活跃 ${active} 天`, `最长连续 ${streak} 天`];
    if (metric !== "sessions") parts.push("合计 " + metricText(metric, totalMetric));
    if (peak) parts.push(`峰值 ${peak.day.slice(5)} ${metricText(metric, peak.v)}`);
    sub.textContent = parts.join(" · ");
  }
}

// periodSum totals msgs / tokens / cost over the `len` days ending `endOffset`
// days before today (0 = ending today).
function periodSum(map, len, endOffset) {
  const out = { msgs: 0, tokens: 0, cost: 0 };
  const d = new Date();
  d.setHours(0, 0, 0, 0);
  d.setDate(d.getDate() - endOffset);
  for (let i = 0; i < len; i++, d.setDate(d.getDate() - 1)) {
    const rec = map.get(dayKey(d));
    if (!rec) continue;
    out.msgs += rec.msgs || 0;
    out.tokens += rec.tokens || 0;
    out.cost += rec.cost || 0;
  }
  return out;
}

// deltaHTML renders the change against the previous period of equal length.
function deltaHTML(cur, prev) {
  if (!prev) return "";
  const pct = Math.round(((cur - prev) / prev) * 100);
  const cls = pct > 0 ? "up" : pct < 0 ? "down" : "";
  return `<span class="kpi-delta ${cls}" title="对比上一周期">${pct > 0 ? "+" : ""}${pct}%</span>`;
}

// cacheRate is the share of prompt tokens served from cache.
export function cacheRate(a) {
  const denom = (a.tokensIn || 0) + (a.cacheRead || 0);
  return denom ? (a.cacheRead || 0) / denom : 0;
}

// renderKPI draws the summary cards: today / 7d / 30d from the trend series
// (tokens and cost there are apportioned by message share), plus the cache
// hit rate of the current scope.
export function renderKPI(el, days, total) {
  if (!el) return;
  const map = new Map((days || []).map((d) => [d.day, d]));
  const card = (label, len) => {
    const cur = periodSum(map, len, 0);
    const prev = periodSum(map, len, len);
    return `<div class="kpi">
        <div class="kpi-label">${label}</div>
        <div class="kpi-val">${fmtNum(cur.tokens)} <small>tokens</small>${deltaHTML(cur.tokens, prev.tokens)}</div>
        <div class="kpi-sub">${fmtNum(cur.msgs)} 条消息 · $${cur.cost.toFixed(2)}</div>
      </div>`;
  };
  const t = total || {};
  const rate = cacheRate(t);
  el.innerHTML =
    card("今日", 1) +
    card("近 7 天", 7) +
    card("近 30 天", 30) +
    `<div class="kpi" title="缓存读 / (未缓存输入 + 缓存读)">
        <div class="kpi-label">缓存命中率</div>
        <div class="kpi-val">${(rate * 100).toFixed(1)}%</div>
        <div class="kpi-sub">缓存读 ${fmtNum(t.cacheRead || 0)} · 输入 ${fmtNum(t.tokensIn || 0)}</div>
      </div>`;
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

// trendBuckets turns the last `span` days into chart bars. Leading idle days
// are trimmed (keeping at least 14) so a short history fills the chart, and
// ranges over 92 days are grouped by week to keep bars readable. Weekly
// "sessions" take the busiest day because per-day session counts overlap.
export function trendBuckets(days, span, metric) {
  const map = new Map((days || []).map((d) => [d.day, d]));
  const end = new Date();
  end.setHours(0, 0, 0, 0);
  const vals = [];
  for (let i = span - 1; i >= 0; i--) {
    const d = new Date(end);
    d.setDate(d.getDate() - i);
    vals.push({ d, v: metricValue(map.get(dayKey(d)), metric) });
  }
  const first = vals.findIndex((x) => x.v > 0);
  const trimmed = first < 0 ? vals.slice(-14) : vals.slice(Math.min(first, Math.max(0, vals.length - 14)));
  if (trimmed.length <= 92) return { weekly: false, bars: trimmed.map((x) => ({ label: dayKey(x.d), v: x.v })) };

  const bars = [];
  for (const x of trimmed) {
    const monday = new Date(x.d);
    monday.setDate(monday.getDate() - ((monday.getDay() + 6) % 7));
    const label = dayKey(monday);
    const last = bars[bars.length - 1];
    if (last && last.label === label) last.v = metric === "sessions" ? Math.max(last.v, x.v) : last.v + x.v;
    else bars.push({ label, v: x.v });
  }
  return { weekly: true, bars };
}

// renderTrend draws the bar chart next to the heatmap, sharing its metric
// and range.
export function renderTrend(el, days, span, metric) {
  if (!el) return;
  const { weekly, bars } = trendBuckets(days, span, metric);
  const max = Math.max(...bars.map((x) => x.v), 0);
  const unit = weekly ? (metric === "sessions" ? "周内单日峰值 " : "当周 ") : "";
  const mid = bars[Math.floor(bars.length / 2)];
  el.innerHTML = `<div class="trend">
      <div class="trend-head"><span>${weekly ? "按周" : "按天"}</span><span>峰值 ${esc(metricText(metric, max))}</span></div>
      <div class="trend-bars">${bars
        .map(
          (x) =>
            `<i class="trend-bar${x.v ? "" : " zero"}" style="height:${max ? Math.max(2, Math.round((x.v / max) * 100)) : 2}%" data-tip="${esc((weekly ? x.label + " 起" : x.label) + " · " + unit + metricText(metric, x.v))}"></i>`
        )
        .join("")}</div>
      <div class="trend-axis"><span>${esc(bars[0].label.slice(5))}</span><span>${esc(mid.label.slice(5))}</span><span>${esc(bars[bars.length - 1].label.slice(5))}</span></div>
    </div>`;
}

// renderModels ranks models by token volume.
function renderModels(el, models) {
  if (!el) return;
  const list = models || [];
  if (!list.length) return emptyPanel(el, "models-sub", "暂无模型记录");
  const top = [...list].sort((a, b) => (b.tokens || 0) - (a.tokens || 0)).slice(0, 8);
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
  if (sub) sub.textContent = `${list.length} 个模型 · 按 Token`;
}

// dataTable renders a comparison table whose rows open a scope via the
// button in their first cell. The last column is a share bar.
function dataTable(head, rows) {
  return `<div class="dt-wrap"><table class="dt">
      <thead><tr>${head.map((h, i) => `<th${i ? ' class="num"' : ""}>${esc(h)}</th>`).join("")}<th class="dt-share">占比</th></tr></thead>
      <tbody>${rows
        .map(
          (r) => `<tr class="${r.active ? "active" : ""}" ${r.attrs}>
            <td><button type="button" class="dt-name" title="${esc(r.title)}">${esc(r.name)}</button></td>
            ${r.cells.map((c) => `<td class="num">${esc(c)}</td>`).join("")}
            <td class="dt-share"><span class="dt-bar"><span class="bar-track"><i class="bar-fill" style="width:${Math.max(2, Math.round(r.pct))}%"></i></span><span class="dt-pct">${Math.round(r.pct)}%</span></span></td>
          </tr>`
        )
        .join("")}</tbody>
    </table></div>`;
}

const money = (v) => (v ? "$" + v.toFixed(2) : "—");

// renderProjectRanking compares projects within the active agent tab. It is a
// cross-project view, so it hides while a single project is selected.
function renderProjectRanking(el, projects) {
  if (!el) return;
  const panel = $("ranking-panel");
  if (panel) panel.hidden = !!state.cwd;
  if (state.cwd) return;
  const list = projects || [];
  if (!list.length) return emptyPanel(el, "ranking-sub", "暂无项目");
  const sum = list.reduce((n, p) => n + (p.tokens || 0), 0) || 1;
  const top = [...list].sort((a, b) => (b.tokens || 0) - (a.tokens || 0)).slice(0, 10);
  el.innerHTML = dataTable(
    ["项目", "会话", "消息", "Tokens", "花费", "最近活跃"],
    top.map((p) => ({
      attrs: `data-cwd="${esc(cwdKey(p.cwd))}"`,
      name: shortPath(p.cwd),
      title: (p.cwd || "(未知目录)") + " · 点击筛选该项目",
      cells: [p.count, fmtNum(p.messages), fmtNum(p.tokens), money(p.cost), fmtTime(p.updatedAt)],
      pct: ((p.tokens || 0) / sum) * 100,
    }))
  );
  const sub = $("ranking-sub");
  if (sub) sub.textContent = `${list.length} 个项目 · 按 Token · ${TAB_LABEL[state.agent] || state.agent} · 点击行筛选`;
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
    // Same row cap as the models panel so the two side-by-side cards line up.
    tools.slice(0, 8).map((t) => ({
      name: t.tool,
      pct: (t.count / max) * 100,
      value: `${fmtNum(t.count)} · ${total ? Math.round((t.count / total) * 100) : 0}%`,
      tip: `${t.tool} · ${t.count} 次调用 · ${t.sessions} 个会话`,
    }))
  );
  const sub = $("tools-sub");
  if (sub) sub.textContent = `共 ${fmtNum(total)} 次调用 · ${tools.length} 种工具 · 前 8`;
}

// renderAgentCompare is the per-agent overview table in the dashboard.
export function renderAgentCompare(el, agents) {
  if (!el) return;
  const list = agents || [];
  if (!list.length) return emptyPanel(el, "agent-sub", "暂无数据");
  const tok = (a) => (a.tokensIn || 0) + (a.tokensOut || 0) + (a.cacheRead || 0);
  const sum = list.reduce((n, a) => n + tok(a), 0) || 1;
  el.innerHTML = dataTable(
    ["Agent", "会话", "消息", "输入", "输出", "缓存读", "缓存命中", "花费"],
    list.map((a) => ({
      attrs: `data-agent="${esc(a.agent)}"`,
      active: a.agent === state.agent,
      name: TAB_LABEL[a.agent] || a.agent,
      title: a.agent === state.agent ? "再次点击回到全部" : "点击切换到该 agent",
      cells: [a.sessions, fmtNum(a.messages), fmtNum(a.tokensIn), fmtNum(a.tokensOut), fmtNum(a.cacheRead), (cacheRate(a) * 100).toFixed(1) + "%", money(a.cost)],
      pct: (tok(a) / sum) * 100,
    }))
  );
  const sub = $("agent-sub");
  if (sub) sub.textContent = `${list.length} 个 agent · 占比按 Token${state.cwd ? " · 当前项目" : ""} · 点击行切换`;
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
  renderKPI($("kpi-body"), state.trend, state.total);
  renderActivity($("activity-body"), state.trend, state.hmRange, state.hmMetric, true);
  renderTrend($("trend-body"), state.trend, state.hmRange, state.hmMetric);
  renderModels($("models-body"), state.models);
  renderProjectRanking($("ranking-body"), state.projects);
  renderToolsFull($("tools-body"), state.tools);
  renderAgentCompare($("agent-body"), state.agents);
}
