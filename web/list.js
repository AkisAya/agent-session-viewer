// list.js — the session list view: loading, empty/error states and card markup.

import { $, esc, fmtNum, fmtTime, fmtDate, displayTitle, hl, midPath, shortModel, copyText, notify } from "./util.js";
import { state } from "./state.js";
import { api } from "./api.js";

export function showEmpty(text, hint) {
  $("list").innerHTML = "";
  $("empty").innerHTML = esc(text) + (hint ? `<div class="hint">${esc(hint)}</div>` : "");
  $("empty").hidden = false;
}

let sessionsAbort = null;

export async function loadSessions() {
  showEmpty("加载中…");
  // Cancel any in-flight list request so a slow earlier response cannot
  // overwrite the newest filter (rapid typing, fast tab switching).
  if (sessionsAbort) sessionsAbort.abort();
  sessionsAbort = new AbortController();
  const p = new URLSearchParams();
  if (state.agent) p.set("agent", state.agent);
  if (state.cwd) p.set("cwd", state.cwd);
  if (state.q) p.set("q", state.q);
  if (state.day) p.set("day", state.day);
  p.set("sort", state.sort);
  try {
    const rows = (await api.sessions(p, sessionsAbort.signal)) || [];
    state.sessions = rows;
    renderList(rows);
  } catch (e) {
    if (e && e.name === "AbortError") return;
    showEmpty("加载失败", "请确认服务仍在运行，然后点击「刷新」重试");
    notify(e.message);
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

export function renderList(rows) {
  // Subagent sessions never appear as their own cards; they are reachable
  // from the main session's detail page. The parent card carries a count tag.
  const subCount = new Map();
  const visible = [];
  for (const s of rows) {
    if (s.relation === "subagent") {
      const pk = s.agent + "|" + s.parentId;
      subCount.set(pk, (subCount.get(pk) || 0) + 1);
    } else {
      visible.push(s);
    }
  }
  if (!visible.length) {
    const hint = state.q || state.cwd ? "试试清除搜索或项目筛选" : "点击右上角「刷新」重新扫描";
    showEmpty("没有匹配的会话", hint);
    return;
  }
  $("empty").hidden = true;
  const showCwd = !state.cwd;

  $("list").innerHTML = visible
    .map((s) => {
      const tokens = (s.tokensIn || 0) + (s.tokensOut || 0) + (s.cacheRead || 0);
      const title = displayTitle(s);
      const previewText = (s.preview || "").trim();
      const showPreview = previewText && previewText !== title;
      const subs = subCount.get(s.agent + "|" + s.id) || 0;
      const cmd = resumeCommand(s);
      return `<div class="card agent-${esc(s.agent)}" data-agent="${esc(s.agent)}" data-id="${esc(s.id)}" tabindex="0" role="button">
        <div class="card-title" title="${esc(title)}">${hl(title, state.q)}</div>
        ${showPreview ? `<div class="card-preview">${hl(previewText, state.q)}</div>` : ""}
        ${showCwd ? `<div class="card-cwd" title="${esc(s.cwd)}">${esc(midPath(s.cwd || "(未知目录)"))}</div>` : ""}
        <div class="card-foot">
          <div class="card-meta">
            <span class="badge ${esc(s.agent)}">${esc(s.agent)}</span>
            ${s.model ? `<span class="card-model" title="${esc(s.model)}">${esc(shortModel(s.model))}</span>` : ""}
            ${subs ? `<span class="tag-sub" title="包含 ${subs} 个 subagent 会话，打开详情页查看">subagent ×${subs}</span>` : ""}
            <span>${s.msgCount || 0} 条</span>
            <span class="tok">${fmtNum(tokens)} tokens</span>
            ${s.cost ? `<span>$${Number(s.cost).toFixed(2)}</span>` : ""}
            <span class="time" title="${esc(fmtDate(s.updatedAt))}">${fmtTime(s.updatedAt)}</span>
          </div>
          ${cmd ? `<button type="button" class="card-resume" data-cmd="${esc(cmd)}" title="复制到剪贴板：${esc(cmd)}">⧉ resume</button>` : ""}
        </div>
      </div>`;
    })
    .join("");
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
