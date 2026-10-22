// list.js — the session list view: loading/paging, empty/error states, the
// card and table renderings, day separators and favorites.

import { $, esc, fmtNum, fmtTime, fmtDate, displayTitle, hl, midPath, shortModel, shortPath, sessionKey, dayKey, dayLabel, copyText, notify } from "./util.js";
import { state, isStarred, tagsOf } from "./state.js";
import { api } from "./api.js";
import { costOf, fmtCost } from "./pricing.js";

export function showEmpty(text, hint, retry, extra) {
  $("list").innerHTML = "";
  $("list").classList.remove("table-mode");
  $("empty").innerHTML =
    esc(text) +
    (hint ? `<div class="hint">${esc(hint)}</div>` : "") +
    (extra || "") +
    (retry ? `<button type="button" class="btn" data-retry>重试</button>` : "");
  $("empty").hidden = state.view !== "list";
  if ($("load-more")) $("load-more").hidden = true;
  if ($("list-info")) $("list-info").hidden = true;
}

// showSkeleton fills the grid with placeholder cards while the first page loads.
export function showSkeleton() {
  $("empty").hidden = true;
  if ($("list-info")) $("list-info").hidden = true;
  if ($("load-more")) $("load-more").hidden = true;
  $("list").classList.remove("table-mode");
  $("list").innerHTML = Array.from({ length: 6 })
    .map(
      () => `<div class="card skeleton-card" aria-hidden="true">
        <div class="sk-line w40"></div>
        <div class="sk-line w90"></div>
        <div class="sk-line w70"></div>
        <div class="sk-line w50"></div>
      </div>`
    )
    .join("");
}

let sessionsAbort = null;
let sessionsSeq = 0;

export async function loadSessions(reset = true, opts = {}) {
  const seq = ++sessionsSeq;
  // A quiet refresh (auto refresh / scan completion) keeps the current page,
  // scroll position and rendered list instead of flashing the skeleton.
  const quiet = reset && !!opts.quiet;
  const keepCount = quiet ? state.sessions.length : 0;
  const scrollY = quiet && typeof window !== "undefined" ? window.scrollY : 0;
  if (reset && !quiet) {
    showSkeleton();
    state.sessions = [];
    state.total = 0;
    state.allLoaded = false;
  }
  if (sessionsAbort) sessionsAbort.abort();
  sessionsAbort = new AbortController();
  const p = new URLSearchParams();
  if (state.agent) p.set("agent", state.agent);
  if (state.cwd) p.set("cwd", state.cwd);
  if (state.q) p.set("q", state.q);
  if (state.day) p.set("day", state.day);
  p.set("sort", state.sort);
  p.set("limit", String(quiet ? Math.min(1000, Math.max(state.pageSize, keepCount)) : state.pageSize));
  p.set("offset", String(reset ? 0 : state.sessions.length));
  try {
    const page = (await api.sessions(p, sessionsAbort.signal)) || {};
    if (seq !== sessionsSeq) return;
    const rows = page.sessions || [];
    state.sessions = reset ? rows : state.sessions.concat(rows);
    state.total = page.total || state.sessions.length;
    state.allLoaded = state.sessions.length >= state.total;
    renderList(state.sessions);
    if (quiet && typeof window.scrollTo === "function") {
      requestAnimationFrame(() => window.scrollTo(0, scrollY));
    }
  } catch (e) {
    if (e && e.name === "AbortError") return;
    if (seq !== sessionsSeq) return;
    if (reset && !quiet) showEmpty("加载失败", "请确认服务仍在运行，然后重试", true);
    else notify("加载失败：" + e.message);
  }
}

// resumeCommand returns the CLI command that reopens a session in its agent.
// DSH has no stable command-line resume entry point, so its cards omit the
// button instead of copying a command that cannot work.
function resumeCommand(s) {
  switch (s.agent) {
    case "claude": return `claude --resume ${s.id}`;
    case "codex": return `codex resume ${s.id}`;
    case "opencode": return `opencode --session ${s.id}`;
    default: return "";
  }
}

const tokensOf = (s) => (s.tokensIn || 0) + (s.tokensOut || 0) + (s.cacheRead || 0);
const COPY_ICON = `<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="11" height="11" rx="2"></rect><path d="M5 15V6a2 2 0 0 1 2-2h9"></path></svg>`;
const dayOf = (s) => {
  const d = s.updatedAt ? new Date(s.updatedAt) : null;
  return d && !isNaN(d) && d.getFullYear() > 2000 ? dayKey(d) : "";
};

// cardHTML renders one session as a three-band card.
function cardHTML(s, subCount) {
  const tokens = tokensOf(s);
  const title = displayTitle(s);
  const previewText = (s.preview || "").trim();
  const showPreview = previewText && previewText !== title;
  const subs = subCount.get(sessionKey(s.agent, s.id)) || 0;
  const cmd = resumeCommand(s);
  const starred = isStarred(s.agent, s.id);
  const cost = fmtCost(costOf(s));
  return `<div class="card agent-${esc(s.agent)}${starred ? " starred" : ""}" data-agent="${esc(s.agent)}" data-id="${esc(s.id)}" tabindex="0" role="button">
    <div class="card-top">
      <span class="badge ${esc(s.agent)}">${esc(s.agent)}</span>
      ${s.model ? `<span class="card-model" title="${esc(s.model)}">${esc(shortModel(s.model))}</span>` : ""}
      ${s.relation === "subagent" ? `<span class="tag-sub">subagent</span>` : ""}
      <button type="button" class="card-star${starred ? " on" : ""}" data-star="${esc(sessionKey(s.agent, s.id))}" title="${starred ? "取消收藏" : "收藏"}">${starred ? "★" : "☆"}</button>
      <span class="time" title="${esc(fmtDate(s.updatedAt))}">${fmtTime(s.updatedAt)}</span>
    </div>
    <div class="card-title" title="${esc(title)}">${hl(title, state.q)}</div>
    ${showPreview ? `<div class="card-preview">${hl(previewText, state.q)}</div>` : ""}
    ${!state.cwd ? `<div class="card-cwd-row">
      <span class="card-cwd" title="${esc(s.cwd)}">${esc(midPath(s.cwd || "(未知目录)"))}</span>
      ${s.cwd ? `<button type="button" class="card-cwd-copy" data-cwd="${esc(s.cwd)}" title="复制目录" aria-label="复制目录">${COPY_ICON}</button>` : ""}
    </div>` : ""}
    <div class="card-foot">
      <div class="card-meta">
        <span>${s.msgCount || 0} 条</span>
        ${tokens ? `<span class="tok">${fmtNum(tokens)} tokens</span>` : ""}
        ${cost !== "—" ? `<span title="${cost.startsWith("≈") ? "按参考单价估算" : "来源记录的花费"}">${esc(cost)}</span>` : ""}
        ${subs ? `<span class="tag-sub" title="包含 ${subs} 个 subagent 会话，打开详情页查看">subagent ×${subs}</span>` : ""}
        ${s.compacts ? `<span class="tag-sub tag-compact" title="上下文压缩 ${s.compacts} 次，打开详情页查看压缩摘要">compact ×${s.compacts}</span>` : ""}
        ${tagsOf(s.agent, s.id).map((t) => `<span class="tag">#${esc(t)}</span>`).join("")}
      </div>
      ${cmd ? `<button type="button" class="card-resume" data-cmd="${esc(cmd)}" title="复制到剪贴板：${esc(cmd)}">复制 resume</button>` : ""}
    </div>
  </div>`;
}

// tableHTML is the compact alternative for scanning many sessions at once.
function tableHTML(rows) {
  const body = rows
    .map((s) => {
      const starred = isStarred(s.agent, s.id);
      const cmd = resumeCommand(s);
      const cost = fmtCost(costOf(s));
      return `<tr class="session-row agent-${esc(s.agent)}${starred ? " starred" : ""}" data-agent="${esc(s.agent)}" data-id="${esc(s.id)}" tabindex="0" role="button">
        <td><button type="button" class="card-star${starred ? " on" : ""}" data-star="${esc(sessionKey(s.agent, s.id))}" title="${starred ? "取消收藏" : "收藏"}">${starred ? "★" : "☆"}</button></td>
        <td><span class="badge ${esc(s.agent)}">${esc(s.agent)}</span></td>
        <td class="st-title" title="${esc(displayTitle(s))} — ${esc(s.cwd || "")}">${hl(displayTitle(s), state.q)}${s.relation === "subagent" ? ` <span class="tag-sub">subagent</span>` : ""}${tagsOf(s.agent, s.id).map((t) => ` <span class="tag">#${esc(t)}</span>`).join("")}</td>
        <td class="st-cwd" title="${esc(s.cwd)}">${esc(shortPath(s.cwd))}</td>
        <td class="st-model" title="${esc(s.model)}">${esc(s.model)}</td>
        <td class="num">${s.msgCount || 0}</td>
        <td class="num">${fmtNum(tokensOf(s))}</td>
        <td class="num" title="${cost.startsWith("≈") ? "按参考单价估算" : ""}">${esc(cost)}</td>
        <td class="st-time" title="${esc(fmtDate(s.updatedAt))}">${fmtTime(s.updatedAt)}</td>
        <td>${cmd ? `<button type="button" class="card-resume" data-cmd="${esc(cmd)}" title="复制 resume 命令">复制 resume</button>` : ""}</td>
      </tr>`;
    })
    .join("");
  return `<div class="table-wrap"><table class="session-table">
    <thead><tr>
      <th title="收藏">★</th><th>Agent</th><th>标题</th><th class="st-cwd-col">项目</th><th>模型</th>
      <th class="num">消息</th><th class="num">Tokens</th><th class="num">花费</th><th>更新</th><th></th>
    </tr></thead><tbody>${body}</tbody>
  </table></div>`;
}

export function renderList(rows) {
  rows = rows || [];
  // Subagent sessions are hidden by default; the parent card carries a count
  // tag counted from the unscoped relations, so a filter that leaves the child
  // out of `rows` does not hide the parent's badge.
  const subCount = new Map();
  for (const s of state.relations) {
    if (s.relation !== "subagent") continue;
    const pk = sessionKey(s.agent, s.parentId);
    subCount.set(pk, (subCount.get(pk) || 0) + 1);
  }
  const all = rows.filter((s) => state.showSubagents || s.relation !== "subagent");
  if (!all.length) {
    if (state.scan === null || (state.scan && state.scan.scanning)) {
      showEmpty("正在加载会话…", "首次启动或扫描中需要一点时间，完成后会自动刷新");
      return;
    }
    const filters = [];
    if (state.agent) filters.push("agent");
    if (state.q) filters.push("搜索");
    if (state.cwd) filters.push("项目");
    if (state.day) filters.push("日期");
    const hint = filters.length
      ? `当前有 ${filters.join(" / ")} 筛选，试试清除`
      : "点击右上角的刷新图标重新扫描";
    const clear = filters.length ? `<button type="button" class="btn" data-clear-all>清除筛选</button>` : "";
    showEmpty("没有匹配的会话", hint, true, clear);
    return;
  }
  // Favorites float to the top without disturbing the chosen sort order.
  const starred = all.filter((s) => isStarred(s.agent, s.id));
  const visible = starred.length ? starred.concat(all.filter((s) => !isStarred(s.agent, s.id))) : all;

  $("empty").hidden = true;
  $("list").classList.toggle("table-mode", state.mode === "table");
  const listVisible = state.view === "list";

  if ($("list-info")) {
    const bits = [`共 ${state.total || visible.length} 个会话`];
    if (visible.length < (state.total || visible.length)) bits.push(`已显示 ${visible.length}`);
    if (state.q) bits.push(`搜索「${state.q}」`);
    if (state.day) bits.push(state.day);
    if (state.cwd) bits.push(shortPath(state.cwd));
    $("list-info").textContent = bits.join(" · ");
    $("list-info").hidden = !listVisible;
  }

  if (state.mode === "table") {
    $("list").innerHTML = tableHTML(visible);
  } else {
    const grouped = state.sort === "updated" || state.sort === "created";
    let prevDay = "";
    $("list").innerHTML = visible
      .map((s) => {
        const day = dayOf(s);
        const sep = grouped && day && day !== prevDay ? `<div class="day-sep"><span>${esc(dayLabel(day))}</span></div>` : "";
        if (grouped && day) prevDay = day;
        return sep + cardHTML(s, subCount);
      })
      .join("");
  }

  if ($("load-more")) {
    const remaining = Math.max(0, state.total - state.sessions.length);
    $("load-more").hidden = !listVisible || state.allLoaded || !state.sessions.length;
    $("load-more").textContent = remaining ? `加载更多（剩余 ${remaining}）` : "加载更多";
  }
}

// copyResume copies a session's resume command to the clipboard, with a
// textarea fallback for when clipboard access is denied.
export async function copyResume(btn) {
  if (btn.dataset.busy) return;
  const cmd = btn.dataset.cmd || "";
  const ok = await copyText(cmd);
  btn.dataset.busy = "1";
  const old = btn.textContent;
  btn.textContent = ok ? "已复制" : "复制失败";
  setTimeout(() => {
    btn.textContent = old;
    delete btn.dataset.busy;
  }, 1300);
}

// exportCSV returns the rows as CSV columns; callers may pass a full set.
export function exportCSV(rows) {
  const head = ["agent", "id", "title", "cwd", "model", "messages", "tokensIn", "tokensOut", "cacheRead", "cost", "compacts", "updated"];
  const data = (rows || state.sessions).map((s) => [
    s.agent, s.id, displayTitle(s), s.cwd || "", s.model || "",
    s.msgCount || 0, s.tokensIn || 0, s.tokensOut || 0, s.cacheRead || 0,
    (s.cost || 0).toFixed(6), s.compacts || 0, s.updatedAt || "",
  ]);
  return { head, rows: data };
}
