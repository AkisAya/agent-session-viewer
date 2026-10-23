// live.js — the status chip a list card / row shows while its agent process is
// running (运行中 / 待处理 / 等待输入), and the top-bar counts.
//
// /api/live reads the agents' own status files on every request (no index, no
// scan), so it is polled every few seconds while the page is visible. A change
// patches the chips in place instead of re-rendering the list, which keeps
// scroll position and keyboard focus.

import { $, esc, fmtDate, sessionKey, notify } from "./util.js";
import { state } from "./state.js";
import { api } from "./api.js";

const POLL_MS = 5000;
const RETRY_MS = 15000;

// STATUS maps an agent's raw status to a label, a style class and a rank
// (lower needs the user sooner). Claude Code writes busy | shell | waiting |
// idle; an unknown value is shown verbatim.
const STATUS = {
  waiting: { label: "待处理", cls: "st-waiting", rank: 0 },
  busy: { label: "运行中", cls: "st-busy", rank: 1 },
  shell: { label: "运行中", cls: "st-busy", rank: 1 },
  idle: { label: "等待输入", cls: "st-idle", rank: 2 },
};
const statusOf = (s) => STATUS[s] || { label: s || "运行中", cls: "st-busy", rank: 3 };

// WAITING_FOR is what a "waiting" session is blocked on.
const WAITING_FOR = {
  "dialog open": "等待确认",
  "input needed": "需要回答",
  "sandbox request": "沙箱授权",
  "worker request": "子任务请求",
  "goal proposal": "确认目标",
};

// liveLabel is the short text of a status chip.
function liveLabel(row) {
  if (row.status === "waiting" && row.waitingFor) return WAITING_FOR[row.waitingFor] || row.waitingFor;
  return statusOf(row.status).label;
}

function liveTitle(row) {
  const bits = [statusOf(row.status).label];
  if (row.status === "waiting" && row.waitingFor) bits[0] += "：" + liveLabel(row);
  if (row.since && !String(row.since).startsWith("0001")) bits.push("自 " + fmtDate(row.since));
  if (row.pid) bits.push("PID " + row.pid);
  return bits.join(" · ");
}

// liveChip is the status pill a list card / row shows while its agent runs.
export function liveChip(agent, id) {
  const row = state.live.get(sessionKey(agent, id));
  if (!row) return "";
  return `<span class="live-chip ${statusOf(row.status).cls}" title="${esc(liveTitle(row))}"><span class="live-dot" aria-hidden="true"></span>${esc(liveLabel(row))}</span>`;
}

// liveSlot is the placeholder the list renders around a chip so a later poll
// can patch it without rebuilding the list.
export function liveSlot(agent, id) {
  return `<span class="live-slot" data-live="${esc(sessionKey(agent, id))}">${liveChip(agent, id)}</span>`;
}

let summaryHTML = "";

// renderSummary fills the top-bar counts. It is display only: the sessions
// themselves are found by their card tags.
function renderSummary() {
  const el = $("live-summary");
  if (!el) return;
  const n = { busy: 0, waiting: 0, idle: 0 };
  for (const row of state.live.values()) {
    const cls = statusOf(row.status).cls;
    if (cls === "st-waiting") n.waiting++;
    else if (cls === "st-idle") n.idle++;
    else n.busy++;
  }
  const total = n.busy + n.waiting + n.idle;
  el.hidden = !total;
  const part = (k, label) =>
    n[k] ? `<span class="lp st-${k}"><span class="live-dot" aria-hidden="true"></span><span class="lp-label">${label} </span>${n[k]}</span>` : "";
  const html = part("busy", "运行") + part("waiting", "待处理") + part("idle", "等待输入");
  // Rewriting identical markup would restart the dots' pulse on every poll.
  if (html !== summaryHTML) {
    summaryHTML = html;
    el.innerHTML = html;
  }
  el.title = `${total} 个会话的 agent 进程正在运行：运行中 ${n.busy}，待处理 ${n.waiting}，等待输入 ${n.idle}`;
}

function paintChips() {
  document.querySelectorAll(".live-slot[data-live]").forEach((el) => {
    const key = el.dataset.live || "";
    const at = key.indexOf("|");
    const html = at > 0 ? liveChip(key.slice(0, at), key.slice(at + 1)) : "";
    if (el.innerHTML !== html) el.innerHTML = html;
  });
}

// setLive installs a new /api/live payload and repaints the chips and counts.
export function setLive(rows) {
  // One session can be open in two terminals; its chip shows the more urgent.
  const map = new Map();
  for (const r of rows || []) {
    const key = sessionKey(r.agent, r.id);
    const prev = map.get(key);
    if (!prev || statusOf(r.status).rank < statusOf(prev.status).rank) map.set(key, r);
  }
  state.live = map;
  renderSummary();
  paintChips();
}

/* ---------- polling ---------- */
let timer = 0;
let inflight = false;
let errShown = "";

async function poll() {
  clearTimeout(timer);
  if (inflight) return;
  inflight = true;
  let delay = POLL_MS;
  try {
    const d = (await api.live()) || {};
    setLive(d.sessions);
    if (d.error && d.error !== errShown) notify("读取运行状态失败：" + d.error);
    errShown = d.error || "";
  } catch (e) {
    // The scan poll already reports an unreachable server; just back off.
    delay = RETRY_MS;
  } finally {
    inflight = false;
  }
  if (!document.hidden) timer = setTimeout(poll, delay);
}

// startLive polls while the page is visible and catches up as soon as it is
// shown again.
export function startLive() {
  document.addEventListener("visibilitychange", () => {
    clearTimeout(timer);
    if (!document.hidden) poll();
  });
  poll();
}
