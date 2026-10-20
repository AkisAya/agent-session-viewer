// dashboard.js — the "看板" view: KPI cards, heatmap, trend, rankings, the
// per-agent comparison, top sessions and the weekday rhythm, plus the scoped
// aggregate fetches that feed them.

import { $, esc, fmtNum, fmtTime, shortPath, cwdKey, dayKey, displayTitle, TAB_LABEL, notify } from "./util.js";
import { state } from "./state.js";
import { api, scopeParams } from "./api.js";
import { costOf, fmtCost, estimateCost } from "./pricing.js";

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

const METRIC_LABEL = { msgs: "消息", sessions: "会话", tokens: "Token", cost: "花费", messages: "消息", compacts: "压缩", tools: "工具", count: "次数" };

// renderActivity draws a GitHub-style heatmap covering the last `span` days,
// coloured by the selected metric. Cells carry a data-tip for the shared
// tooltip and can be clicked (or activated with Enter) to filter that day.
export function renderActivity(el, days, span, metric, withLegend) {
  if (!el) return;
  metric = metric || "msgs";
  const map = new Map();
  for (const d of days || []) map.set(d.day, d);

  const end = new Date();
  end.setHours(0, 0, 0, 0);
  const start = new Date(end);
  start.setDate(start.getDate() - (span - 1));
  const today = dayKey(end);

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
          // Avoid repeating the selected metric in its own tooltip.
          const parts = [metricText(metric, v)];
          if (metric !== "msgs") parts.push(`${msgs} 条`);
          if (metric !== "sessions") parts.push(`${sessions} 个会话`);
          const detail = parts.join(" · ");
          const sel = state.day === key ? " sel" : "";
          const isToday = key === today ? " today" : "";
          return `<span class="hm-cell lv${heatLevel(v, max)}${sel}${isToday}" data-day="${key}" tabindex="0" role="button" aria-label="${esc(key + " " + detail)}" data-tip="${esc(detail)}"></span>`;
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
// Colour is applied by CSS (red = rise, green = fall for every metric).
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
        <div class="kpi-sub">${fmtNum(cur.msgs)} 条消息 · $${cur.cost.toFixed(2)}${deltaHTML(cur.cost, prev.cost)}</div>
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
      <div class="trend-bars" aria-hidden="true">${bars
        .map(
          (x) =>
            `<i class="trend-bar${x.v ? "" : " zero"}" style="height:${max ? Math.max(2, Math.round((x.v / max) * 100)) : 2}%" data-tip="${esc((weekly ? x.label + " 起" : x.label) + " · " + unit + metricText(metric, x.v))}"></i>`
        )
        .join("")}</div>
      <div class="trend-axis"><span>${esc(bars[0].label.slice(5))}</span><span>${esc(mid.label.slice(5))}</span><span>${esc(bars[bars.length - 1].label.slice(5))}</span></div>
    </div>`;
  const sub = $("activity-sub");
  if (sub) sub.title = "趋势图会裁掉开头的空白日；热力图展示完整范围";
}

// modelCost prefers a recorded cost, falling back to the pricing table.
function modelCost(m) {
  const real = Number(m.cost) || 0;
  if (real > 0) return { value: real, estimated: false };
  const est = estimateCost(m);
  return est > 0 ? { value: est, estimated: true } : { value: 0, estimated: false };
}

// renderModels ranks models by the selected metric.
function renderModels(el, models) {
  if (!el) return;
  const list = models || [];
  if (!list.length) return emptyPanel(el, "models-sub", "暂无模型记录");
  const metric = state.modelsMetric;
  const value = (m) => (metric === "cost" ? modelCost(m).value : metric === "sessions" ? m.sessions || 0 : m.tokens || 0);
  const sorted = [...list].sort((a, b) => value(b) - value(a));
  const top = state.showAllModels ? sorted : sorted.slice(0, 8);
  const max = Math.max(...top.map(value), 1);
  barRows(
    el,
    top.map((m) => {
      const c = modelCost(m);
      const costText = fmtCost(c);
      let val = `${m.sessions} 会话 · ${fmtNum(m.tokens)}`;
      if (metric === "sessions") val = `${fmtNum(m.messages)} 消息 · ${fmtNum(m.tokens)} tokens`;
      if (metric === "cost") val = `${m.sessions} 会话 · ${costText}`;
      return {
        name: m.model || "(未知模型)",
        title: m.model || "(未知模型)",
        pct: (value(m) / max) * 100,
        value: val,
        tip: `${m.model || "(未知模型)"} · ${m.sessions} 会话 · ${fmtNum(m.messages)} 消息 · ${fmtNum(m.tokens)} tokens${costText !== "—" ? " · " + costText : ""}`,
      };
    })
  );
  const sub = $("models-sub");
  if (sub) {
    sub.textContent = `${list.length} 个模型 · 按 ${METRIC_LABEL[metric] || "Token"}${metric === "cost" ? "（≈ 为估算）" : ""}`;
    sub.title = "按每个会话索引到的模型聚合；会话中途切换模型时归到最后一个模型";
  }
}

// dataTable renders a comparison table with clickable sort headers. The last
// column is a share bar.
function dataTable(table, head, rows) {
  const sort = table === "agent" ? state.agentSort : state.projectSort;
  const th = head
    .map((h) => {
      const arrow = sort.key === h.key ? (sort.dir < 0 ? " ↓" : " ↑") : "";
      const aria = sort.key === h.key ? ` aria-sort="${sort.dir < 0 ? "descending" : "ascending"}"` : "";
      return `<th${h.num ? ' class="num"' : ""}${aria}><button type="button" class="th-sort" data-sort-table="${table}" data-sort-key="${esc(h.key)}">${esc(h.label)}${arrow}</button></th>`;
    })
    .join("");
  const shareArrow = sort.key === "share" ? (sort.dir < 0 ? " ↓" : " ↑") : "";
  const shareAria = sort.key === "share" ? ` aria-sort="${sort.dir < 0 ? "descending" : "ascending"}"` : "";
  return `<div class="dt-wrap"><table class="dt">
      <thead><tr>${th}<th class="dt-share"${shareAria}><button type="button" class="th-sort" data-sort-table="${table}" data-sort-key="share">占比${shareArrow}</button></th></tr></thead>
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

// getter reads a sortable value from a row; strings sort with localeCompare.
function sortRows(rows, sort, getters, defaultKey) {
  const key = sort && sort.key ? sort.key : defaultKey;
  const dir = sort && sort.key ? sort.dir : -1;
  const fn = getters[key];
  if (!fn) return [...rows];
  return [...rows].sort((a, b) => {
    const va = fn(a);
    const vb = fn(b);
    if (typeof va === "string" || typeof vb === "string") return String(va).localeCompare(String(vb)) * dir;
    return (va < vb ? -1 : va > vb ? 1 : 0) * dir;
  });
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
  const sorted = sortRows(
    list,
    state.projectSort,
    {
      cwd: (p) => p.cwd || "(未知目录)",
      count: (p) => p.count || 0,
      messages: (p) => p.messages || 0,
      tokens: (p) => p.tokens || 0,
      cost: (p) => p.cost || 0,
      updated: (p) => (p.updatedAt ? new Date(p.updatedAt).getTime() : 0),
      share: (p) => p.tokens || 0,
    },
    "tokens"
  );
  const canExpand = list.length > 10;
  const allBtn = $("ranking-all");
  if (allBtn) allBtn.hidden = !canExpand;
  const top = state.showAllProjects || !canExpand ? sorted : sorted.slice(0, 10);
  el.innerHTML = dataTable(
    "project",
    [
      { key: "cwd", label: "项目" },
      { key: "count", label: "会话", num: true },
      { key: "messages", label: "消息", num: true },
      { key: "tokens", label: "Tokens", num: true },
      { key: "cost", label: "花费", num: true },
      { key: "updated", label: "最近活跃" },
    ],
    top.map((p) => ({
      attrs: `data-cwd="${esc(cwdKey(p.cwd))}"`,
      name: shortPath(p.cwd),
      title: (p.cwd || "(未知目录)") + " · 点击筛选该项目",
      cells: [p.count, fmtNum(p.messages), fmtNum(p.tokens), money(p.cost), fmtTime(p.updatedAt)],
      pct: ((p.tokens || 0) / sum) * 100,
    }))
  );
  const sub = $("ranking-sub");
  if (sub) sub.textContent = `${list.length} 个项目${canExpand ? (state.showAllProjects ? " · 全部" : " · 前 10") : ""} · 点击表头排序 · 点击行筛选`;
}

// renderToolsFull shows the whole tool ranking (not just the project-page top 6).
function renderToolsFull(el, st) {
  if (!el) return;
  const tools = (st && st.tools) || [];
  if (!tools.length) return emptyPanel(el, "tools-sub", "暂无工具记录");
  const total = st.total || tools.reduce((n, t) => n + t.count, 0);
  const metric = state.toolsMetric;
  const value = (t) => (metric === "sessions" ? t.sessions || 0 : t.count || 0);
  const sorted = [...tools].sort((a, b) => value(b) - value(a));
  const rows = state.showAllTools ? sorted : sorted.slice(0, 8);
  const max = Math.max(...rows.map(value), 1);
  barRows(
    el,
    rows.map((t) => ({
      name: t.tool,
      pct: (value(t) / max) * 100,
      value: metric === "sessions" ? `${t.sessions} 会话 · ${fmtNum(t.count)} 次` : `${fmtNum(t.count)} · ${total ? Math.round((t.count / total) * 100) : 0}%`,
      tip: `${t.tool} · ${t.count} 次调用 · ${t.sessions} 个会话`,
    }))
  );
  const sub = $("tools-sub");
  if (sub) sub.textContent = `共 ${fmtNum(total)} 次调用 · ${tools.length} 种工具 · 按 ${metric === "sessions" ? "会话" : "调用次数"}`;
}

// renderAgentCompare is the per-agent overview table in the dashboard.
export function renderAgentCompare(el, agents) {
  if (!el) return;
  const list = agents || [];
  if (!list.length) return emptyPanel(el, "agent-sub", "暂无数据");
  const tok = (a) => (a.tokensIn || 0) + (a.tokensOut || 0) + (a.cacheRead || 0);
  const sum = list.reduce((n, a) => n + tok(a), 0) || 1;
  const sorted = sortRows(
    list,
    state.agentSort,
    {
      agent: (a) => TAB_LABEL[a.agent] || a.agent,
      sessions: (a) => a.sessions || 0,
      messages: (a) => a.messages || 0,
      tokensIn: (a) => a.tokensIn || 0,
      tokensOut: (a) => a.tokensOut || 0,
      cacheRead: (a) => a.cacheRead || 0,
      rate: (a) => cacheRate(a),
      cost: (a) => a.cost || 0,
      share: tok,
    },
    "share"
  );
  el.innerHTML = dataTable(
    "agent",
    [
      { key: "agent", label: "Agent" },
      { key: "sessions", label: "会话", num: true },
      { key: "messages", label: "消息", num: true },
      { key: "tokensIn", label: "输入", num: true },
      { key: "tokensOut", label: "输出", num: true },
      { key: "cacheRead", label: "缓存读", num: true },
      { key: "rate", label: "缓存命中", num: true },
      { key: "cost", label: "花费", num: true },
    ],
    sorted.map((a) => ({
      attrs: `data-agent="${esc(a.agent)}"`,
      active: a.agent === state.agent,
      name: TAB_LABEL[a.agent] || a.agent,
      title: a.agent === state.agent ? "再次点击回到全部" : "点击切换到该 agent",
      cells: [a.sessions, fmtNum(a.messages), fmtNum(a.tokensIn), fmtNum(a.tokensOut), fmtNum(a.cacheRead), (cacheRate(a) * 100).toFixed(1) + "%", money(a.cost)],
      pct: (tok(a) / sum) * 100,
    }))
  );
  const sub = $("agent-sub");
  if (sub) sub.textContent = `${list.length} 个 agent · 占比按 Token${state.cwd ? " · 当前项目" : ""} · 点击行切换 · 点击表头排序`;
}

// renderTopSessions ranks the most expensive / largest sessions in scope.
export function renderTopSessions(el, sessions) {
  if (!el) return;
  const list = sessions || [];
  if (!list.length) return emptyPanel(el, "top-sub", "暂无会话");
  const metric = state.topMetric;
  const val = (s) => {
    if (metric === "cost") return costOf(s).value;
    if (metric === "tokens") return (s.tokensIn || 0) + (s.tokensOut || 0) + (s.cacheRead || 0);
    if (metric === "compacts") return s.compacts || 0;
    if (metric === "tools") return s.toolCount || 0;
    return s.msgCount || 0;
  };
  const fmt = (s) => {
    if (metric === "cost") return fmtCost(costOf(s));
    if (metric === "tokens") return fmtNum(val(s));
    if (metric === "compacts") return (s.compacts || 0) + " 次";
    if (metric === "tools") return fmtNum(val(s)) + " 次";
    return fmtNum(s.msgCount || 0) + " 条";
  };
  const top = [...list].sort((a, b) => val(b) - val(a)).slice(0, 8);
  el.innerHTML = top
    .map(
      (s, n) => `<div class="top-row agent-${esc(s.agent)}" data-agent="${esc(s.agent)}" data-id="${esc(s.id)}" tabindex="0" role="button">
        <span class="top-rank">${n + 1}</span>
        <span class="badge ${esc(s.agent)}">${esc(s.agent)}</span>
        <span class="top-title" title="${esc(displayTitle(s))}">${esc(displayTitle(s))}</span>
        <span class="top-val">${esc(fmt(s))}</span>
      </div>`
    )
    .join("");
  const sub = $("top-sub");
  if (sub) sub.textContent = `按 ${METRIC_LABEL[metric] || "花费"} · 前 ${top.length}`;
}

// renderWeekday sums messages by local weekday from the trend series.
export function renderWeekday(el, days) {
  if (!el) return;
  const names = ["一", "二", "三", "四", "五", "六", "日"];
  const sums = [0, 0, 0, 0, 0, 0, 0];
  let total = 0;
  for (const d of days || []) {
    const dt = new Date(d.day + "T00:00:00");
    if (isNaN(dt)) continue;
    const n = d.msgs || 0;
    sums[(dt.getDay() + 6) % 7] += n;
    total += n;
  }
  const max = Math.max(...sums, 1);
  el.innerHTML = `<div class="weekday">${sums
    .map(
      (v, i) => `<div class="wd-col" data-tip="${names[i]} · ${fmtNum(v)} 条消息">
        <span class="wd-val">${fmtNum(v)}</span>
        <span class="wd-bar"><i style="height:${Math.max(2, Math.round((v / max) * 100))}%"></i></span>
        <span class="wd-day">${names[i]}</span>
      </div>`
    )
    .join("")}</div>`;
  const sub = $("weekday-sub");
  if (sub) sub.textContent = `共 ${fmtNum(total)} 条消息 · 按本地日期`;
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

let panelsSeq = 0;

// loadPanels refreshes the scoped aggregates and repaints whichever panel
// containers are currently visible.
export async function loadPanels() {
  const seq = ++panelsSeq;
  const scope = scopeParams();
  const trendP = new URLSearchParams(scope);
  trendP.set("days", "366");
  const toolsP = new URLSearchParams(scope);
  toolsP.set("limit", "200");
  // shortcut: the top panel ranks the 500 most recently updated sessions, so a
  // scope larger than that can miss an older outlier. Upgrade if that matters.
  const topP = new URLSearchParams(scope);
  topP.set("sort", "updated");
  topP.set("limit", "500");

  const [trend, tools, models, top] = await Promise.all([
    settle(api.trend(trendP), "趋势"),
    settle(api.tools(toolsP), "工具"),
    settle(api.models(scope), "模型"),
    settle(api.sessions(topP), "会话"),
  ]);
  if (seq !== panelsSeq) return;
  // Keep the previous panel contents when a request fails (rather than
  // flashing "暂无数据") so a transient error does not wipe the view.
  if (trend.ok) state.trend = trend.v || [];
  if (tools.ok) state.tools = tools.v || { tools: [], total: 0 };
  if (models.ok) state.models = models.v || [];
  if (top.ok) state.top = (top.v && top.v.sessions) || [];
  paintPanels();
}

// loadTop refreshes only the top-sessions panel after its metric changes.
let topSeq = 0;

export async function loadTop() {
  const seq = ++topSeq;
  const p = scopeParams();
  p.set("sort", "updated");
  p.set("limit", "500");
  try {
    const page = await api.sessions(p);
    if (seq !== topSeq) return;
    state.top = (page && page.sessions) || [];
    renderTopSessions($("top-body"), state.top);
  } catch (e) {
    notify("Top 会话加载失败：" + e.message);
  }
}

// syncDashboardControls mirrors state into the segmented controls.
export function syncDashboardControls() {
  const set = (sel, match) => {
    document.querySelectorAll(sel + " button").forEach((b) => b.classList.toggle("active", match(b)));
  };
  set("#hm-metric", (b) => b.dataset.m === state.hmMetric);
  set("#hm-range", (b) => Number(b.dataset.r) === state.hmRange);
  set("#models-metric", (b) => b.dataset.m === state.modelsMetric);
  set("#tools-metric", (b) => b.dataset.m === state.toolsMetric);
  set("#top-metric", (b) => b.dataset.m === state.topMetric);
  const ma = $("models-all");
  if (ma) ma.textContent = state.showAllModels ? "收起" : "全部";
  const ta = $("tools-all");
  if (ta) ta.textContent = state.showAllTools ? "收起" : "全部";
  const ra = $("ranking-all");
  if (ra) ra.textContent = state.showAllProjects ? "收起" : "全部";
}

export function paintPanels() {
  renderKPI($("kpi-body"), state.trend, state.totalStats);
  renderActivity($("activity-body"), state.trend, state.hmRange, state.hmMetric, true);
  renderTrend($("trend-body"), state.trend, state.hmRange, state.hmMetric);
  renderModels($("models-body"), state.models);
  renderProjectRanking($("ranking-body"), state.projects);
  renderToolsFull($("tools-body"), state.tools);
  renderAgentCompare($("agent-body"), state.agents);
  renderTopSessions($("top-body"), state.top);
  renderWeekday($("weekday-body"), state.trend);
  syncDashboardControls();
}
