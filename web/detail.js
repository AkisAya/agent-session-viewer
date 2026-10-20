// detail.js — the session transcript modal: messages, the outline rail,
// parent/child relations, find/jump, export and the modal's own event wiring.

import { $, esc, fmtNum, fmtDate, fmtClock, fmtDayLabel, displayTitle, dayKey, midPath, scrollBehavior, copyText, notify, download, safeFilename, rafThrottle, store } from "./util.js";
import { state, findSession, writeURL, tagsOf, setTags } from "./state.js";
import { api } from "./api.js";
import { costOf, fmtCost } from "./pricing.js";
import { renderList } from "./list.js";

let relSeq = 0;

export async function loadRelations() {
  const seq = ++relSeq;
  try {
    const list = (await api.relations()) || [];
    if (seq !== relSeq) return;
    state.relations = list;
  } catch (e) {
    if (seq !== relSeq) return;
    state.relations = [];
    notify(e.message);
  }
  if (state.current) renderDetailRel(state.current);
}

/* ---------- transcript rendering ---------- */
const NOISE_ROLES = ["reasoning", "tool", "system"];
const CHUNK = 120;
const FOLD_AT = 1800;
const isNoise = (role) => NOISE_ROLES.indexOf(role) >= 0;

// msgBodyHTML renders one transcript row: markdown for conversation content,
// plain pre-wrapped text for tool output and system payloads.
function msgBodyHTML(m) {
  const role = m.role || "system";
  const text = m.text || "";
  if (role === "tool" || role === "system") {
    return `<div class="msg-body">${esc(text)}</div>`;
  }
  const html = window.md ? md.render(text) : esc(text);
  return `<div class="msg-body md">${html}</div>`;
}

// compactHTML renders a context-compaction marker: a divider that is always
// visible, with the carried-forward summary folded inside.
function compactHTML(m, i, time, n) {
  const text = (m.text || "").trim();
  const body = text
    ? `<details class="compact-sum">
        <summary><span class="compact-excerpt">${esc(railSnippet(text.slice(0, 600), 160))}</span><span class="compact-toggle">${fmtNum(text.length)} 字</span></summary>
        <div class="msg-body md">${window.md ? md.render(text) : esc(text)}</div></details>`
    : `<div class="compact-empty">该 agent 未记录摘要内容</div>`;
  return `<div class="msg compact" data-i="${i}">
      <div class="compact-line"><span>⇣ 上下文压缩 #${n}${time}</span></div>
      ${body}
    </div>`;
}

// msgHTML renders one user/assistant/reasoning/tool row. Long messages start
// folded so a wall of text does not bury the rest of the conversation.
function msgHTML(m, i, time) {
  const role = m.role || "system";
  const text = m.text || "";
  const long = text.length > FOLD_AT;
  const body = msgBodyHTML(m);
  const clamped = long ? body.replace('class="msg-body', 'class="msg-body clamped') : body;
  return `<div class="msg ${esc(role)}" data-i="${i}">
    <div class="msg-role">${esc(role)}${time}<button type="button" class="msg-copy" data-copy="${i}" title="复制这条消息">复制</button></div>
    ${clamped}
    ${long ? `<button type="button" class="msg-expand" data-expand="${i}">展开全部 · ${fmtNum(text.length)} 字</button>` : ""}
  </div>`;
}

// visibleMessages flattens the transcript into renderable rows, skipping noise
// unless it is requested. Day dividers are precomputed so chunks can append
// without carrying state across boundaries.
function visibleMessages(msgs) {
  const items = [];
  let prevDay = "";
  let nCompact = 0;
  msgs.forEach((m, i) => {
    const role = m.role || "system";
    if (!state.noise && isNoise(role)) return;
    const ts = m.timestamp ? new Date(m.timestamp) : null;
    const valid = ts && !isNaN(ts) && ts.getFullYear() > 2000;
    const day = valid ? dayKey(ts) : "";
    const divider = valid && day !== prevDay ? `<div class="msg-day"><span>${esc(fmtDayLabel(ts))}</span></div>` : "";
    if (valid) prevDay = day;
    const time = valid ? `<span class="msg-time">${esc(fmtClock(ts))}</span>` : "";
    const n = role === "compact" ? ++nCompact : 0;
    const full = m.text || "";
    items.push({
      i,
      role,
      full,
      text: role === "compact" ? "⇣ 上下文压缩" : railSnippet(full),
      divider,
      time,
      m,
      n,
    });
  });
  return items;
}

// renderItem defers markdown work until a chunk is actually appended, so the
// first screen appears quickly even for a very long transcript.
function renderItem(it) {
  const body = it.role === "compact" ? compactHTML(it.m, it.i, it.time, it.n) : msgHTML(it.m, it.i, it.time);
  return it.divider + body;
}

let offsets = [];
let chunkTimer = 0;

function cancelChunks() {
  if (chunkTimer) clearTimeout(chunkTimer);
  chunkTimer = 0;
  state.renderToken = (state.renderToken || 0) + 1;
}

function updateOffsets() {
  const box = $("detail-body");
  if (!box || !box.querySelectorAll) return;
  offsets = Array.from(box.querySelectorAll(".msg[data-i]")).map((n) => ({
    i: Number(n.dataset.i),
    top: n.offsetTop,
  }));
}

function renderMessages(msgs) {
  cancelChunks();
  const box = $("detail-body");
  const items = visibleMessages(msgs);
  state.visible = items;
  const keep = box.scrollTop || 0;
  box.innerHTML = "";
  offsets = [];
  buildRail(items);
  const token = state.renderToken;
  let pos = 0;
  const step = () => {
    if (token !== state.renderToken || state.current === null) return;
    const part = items.slice(pos, pos + CHUNK);
    if (!part.length) {
      updateOffsets();
      return;
    }
    box.insertAdjacentHTML("beforeend", part.map(renderItem).join(""));
    pos += part.length;
    updateOffsets();
    if (pos === part.length && keep) box.scrollTop = keep;
    if (pos < items.length) {
      chunkTimer = setTimeout(step, 0);
    } else {
      onMsgScroll();
    }
  };
  step();
}

// railSnippet flattens a message into a one-line outline entry.
function railSnippet(text, max = 46) {
  const s = String(text || "")
    .replace(/```[\s\S]*?```/g, " [code] ")
    .replace(/[#>*`|_-]/g, " ")
    .replace(/\s+/g, " ")
    .trim();
  if (!s) return "(空)";
  return s.length > max ? s.slice(0, max) + "…" : s;
}

// buildRail fills the outline rail from the visible rows so every entry maps
// to a scrollable message.
function buildRail(items) {
  const rail = $("rail");
  const list = $("rail-list");
  if (!rail || !list) return;
  if (items.length < 3) {
    rail.hidden = true;
    rail.classList.remove("open");
    const btn = $("detail-rail-toggle");
    if (btn) {
      btn.classList.remove("active");
      btn.setAttribute("aria-pressed", "false");
      btn.hidden = true;
    }
    list.innerHTML = "";
    return;
  }
  rail.hidden = false;
  if ($("detail-rail-toggle")) $("detail-rail-toggle").hidden = false;
  $("rail-count").textContent = String(items.length);
  list.innerHTML = items
    .map(
      (it) => `<button type="button" class="rail-item ${esc(it.role)}" data-i="${it.i}" title="${esc(it.role)}">
        <span class="rail-dot"></span><span class="rail-text">${esc(it.text)}</span>
      </button>`
    )
    .join("");
  onMsgScroll();
}

function updateRailActive(idx) {
  const list = $("rail-list");
  if (!list || list.hidden) return;
  const next = list.querySelector(`.rail-item[data-i="${idx}"]`);
  const cur = list.querySelector(".rail-item.active");
  if (cur === next) return;
  if (cur) cur.classList.remove("active");
  if (!next) return;
  next.classList.add("active");
  const top = next.offsetTop - list.clientHeight / 2 + next.offsetHeight / 2;
  if (list.scrollTo) list.scrollTo({ top: Math.max(0, top), behavior: scrollBehavior() });
  else list.scrollTop = Math.max(0, top);
}

// onMsgScroll keeps the rail highlight in sync with the reading position: the
// active entry is the message occupying the top of the viewport (or the last
// one when scrolled to the bottom). Offsets are cached and searched, so a long
// transcript does not walk every node on each scroll event.
function onMsgScrollNow() {
  if (state.railLock) return;
  const box = $("detail-body");
  if (!box || !offsets.length) return;
  const top = box.scrollTop + 48;
  let lo = 0;
  let hi = offsets.length - 1;
  let active = offsets[0].i;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (offsets[mid].top <= top) {
      active = offsets[mid].i;
      lo = mid + 1;
    } else {
      hi = mid - 1;
    }
  }
  updateRailActive(active);
}
const onMsgScroll = rafThrottle(onMsgScrollNow);

/* ---------- detail header ---------- */
function chip(label, value, cls) {
  return `<span class="ds${cls ? " " + cls : ""}"><i>${label}</i>${value}</span>`;
}

function renderDetailStats(s, msgs) {
  const noise = msgs.filter((m) => isNoise(m.role)).length;
  const reasoning = msgs.filter((m) => m.role === "reasoning").length;
  const toolMsgs = msgs.filter((m) => m.role === "tool").length;
  const compacts = msgs.filter((m) => m.role === "compact").length;
  const talk = msgs.length - noise - compacts;
  const prompt = (s.tokensIn || 0) + (s.cacheRead || 0);
  const c = costOf(s);
  $("detail-stats").innerHTML = [
    chip("对话", talk),
    reasoning ? chip("思考", reasoning) : "",
    toolMsgs ? chip("工具", toolMsgs) : "",
    compacts ? chip("压缩", compacts + " 次", "ds-compact") : "",
    `<span class="ds-sep"></span>`,
    chip("输入", fmtNum(s.tokensIn)),
    chip("输出", fmtNum(s.tokensOut)),
    chip("缓存读", fmtNum(s.cacheRead)),
    prompt ? chip("缓存命中", `${(((s.cacheRead || 0) / prompt) * 100).toFixed(1)}%`) : "",
    c.value ? chip(c.estimated ? "花费≈" : "花费", fmtCost(c), c.estimated ? "ds-est" : "") : "",
  ].join("");
}

// renderDetailRel links a session to its parent and children, if any.
export function renderDetailRel(s) {
  const el = $("detail-rel");
  if (!el) return;
  const parent = s.parentId ? state.relations.find((r) => r.id === s.parentId && r.agent === s.agent) : null;
  const kids = state.relations.filter((r) => r.parentId === s.id && r.agent === s.agent);
  if (!parent && !kids.length) {
    el.hidden = true;
    el.innerHTML = "";
    return;
  }
  const parts = [];
  if (parent) {
    parts.push(`<button type="button" class="rel-parent" data-agent="${esc(parent.agent)}" data-id="${esc(parent.id)}" title="${esc(parent.id)}">
        <span class="rel-label">${s.relation === "subagent" ? "↩ 主会话" : "↩ 续接自"}</span><span class="rel-name">${esc(displayTitle(parent))}</span>
      </button>`);
  }
  if (kids.length) {
    const label = kids.every((k) => k.relation === "subagent") ? "Subagent" : "后续会话";
    const open = kids.length <= 2 ? " open" : "";
    const rows = kids
      .map(
        (k, n) => `<button type="button" class="rel-row" data-agent="${esc(k.agent)}" data-id="${esc(k.id)}" title="${esc(k.id)}">
            <span class="rel-n">${n + 1}</span><span class="rel-name">${esc(displayTitle(k))}</span>
            <span class="rel-info">${k.msgCount || 0} 条 · ${fmtNum((k.tokensIn || 0) + (k.tokensOut || 0) + (k.cacheRead || 0))} tokens</span>
          </button>`
      )
      .join("");
    const first = esc(displayTitle(kids[0]));
    parts.push(`<details class="rel-kids"${open}>
        <summary class="rel-head">${label}<span class="rel-count">${kids.length}</span><span class="rel-peek">${first}${kids.length > 1 ? " 等" : ""}</span><span class="rel-toggle"></span></summary>
        <div class="rel-list">${rows}</div>
      </details>`);
  }
  el.hidden = false;
  el.innerHTML = parts.join("");
}

/* ---------- open / close ---------- */
let detailSeq = 0;
let abort = null;

// renderTags shows the local tags for a session with an inline editor.
function renderTags(s) {
  const el = $("detail-tags");
  if (!el) return;
  const tags = tagsOf(s.agent, s.id);
  el.innerHTML =
    tags.map((t) => `<span class="tag">#${esc(t)}</span>`).join("") +
    `<button type="button" class="tag-add" data-tag-edit title="编辑标签">＋ 标签</button>`;
}

function editTags(s) {
  const el = $("detail-tags");
  const tags = tagsOf(s.agent, s.id);
  el.innerHTML = `<input id="tag-input" class="tag-input" value="${esc(tags.join(", "))}" placeholder="逗号分隔，最多 8 个" /> <button type="button" class="btn mini" data-tag-save>保存</button>`;
  const input = $("tag-input");
  if (input) {
    input.focus();
    input.select();
  }
}

function saveTags(s) {
  const input = $("tag-input");
  if (!input) return;
  setTags(s.agent, s.id, input.value.split(/[,，]/));
  renderTags(s);
  if (state.sessions.length) renderList(state.sessions);
}

function skeletonHTML() {
  return `<div class="msg-skeleton" aria-hidden="true">
    <div class="sk-line w30"></div><div class="sk-line w90"></div><div class="sk-line w80"></div>
    <div class="sk-line w40"></div><div class="sk-line w70"></div>
  </div>`;
}

const FONT_LABELS = ["标准", "大", "特大"];

function setFont(n) {
  n = Number(n) || 0;
  const modal = $("modal");
  if (modal) modal.setAttribute("data-font", String(n));
  const b = $("detail-font");
  if (b) b.title = `字号：${FONT_LABELS[n] || "标准"}（点击切换）`;
}

function syncWide() {
  const b = $("detail-wide");
  if (!b) return;
  b.classList.toggle("active", !!state.transcriptWide);
  b.setAttribute("aria-pressed", state.transcriptWide ? "true" : "false");
  b.title = state.transcriptWide ? "切回标准宽度" : "宽屏显示";
}

export function openDetail(s, opts = {}) {
  if (!s) return;
  if (abort) abort.abort();
  cancelChunks();
  detailSeq++;
  const seq = detailSeq;
  state.current = s;
  state.msgs = [];
  state.visible = [];
  state.find = { q: "", hits: [], pos: -1 };
  state.userPos = -1;
  const overlay = $("overlay");
  if (overlay.hidden) state.lastFocus = document.activeElement;
  overlay.hidden = false;
  document.body.classList.add("modal-open");
  setFont(state.transcriptFont);
  const modal = $("modal");
  if (modal) modal.classList.toggle("wide", state.transcriptWide);
  syncWide();
  const findBtn = $("detail-find-toggle");
  if (findBtn) {
    findBtn.classList.remove("active");
    findBtn.setAttribute("aria-pressed", "false");
  }
  const railBtn = $("detail-rail-toggle");
  if (railBtn) {
    railBtn.classList.remove("active");
    railBtn.setAttribute("aria-pressed", "false");
    railBtn.hidden = true;
  }
  if ($("find-bar")) $("find-bar").hidden = true;
  if ($("find")) $("find").value = "";
  if ($("find-count")) $("find-count").textContent = "";

  $("detail-title").innerHTML =
    (s.relation === "subagent"
      ? `<button type="button" class="tag-sub" data-agent="${esc(s.agent)}" data-parent="${esc(s.parentId)}" title="点击查看主会话">subagent</button>`
      : "") + esc(displayTitle(s));
  $("detail-meta").innerHTML =
    `<span class="badge ${esc(s.agent)}">${esc(s.agent)}</span>` +
    `<span class="dm-cwd copyable" title="点击复制目录">${esc(midPath(s.cwd || "(未知目录)", 64))}</span>` +
    (s.model ? `<span class="dm-model">${esc(s.model)}</span>` : "");
  $("detail-stats").textContent = "加载中…";
  $("detail-id").textContent = [fmtDate(s.createdAt), s.id].join("  ·  ");
  $("detail-id").title = "点击复制会话 ID";
  renderTags(s);
  $("detail-body").innerHTML = skeletonHTML();
  $("detail-body").scrollTop = 0;
  offsets = [];
  state.railLock = false;
  $("rail-list").innerHTML = "";
  $("rail").hidden = true;
  $("rail").classList.remove("open");
  $("detail-body").classList.toggle("show-noise", state.noise);
  renderDetailRel(s);
  if (!opts.fromURL) writeURL(true);

  abort = new AbortController();
  const signal = abort.signal;
  api
    .transcript(s.agent, s.id, signal)
    .then((msgs) => {
      if (seq !== detailSeq || state.current !== s) return;
      state.msgs = msgs || [];
      renderDetailStats(s, state.msgs);
      renderMessages(state.msgs);
    })
    .catch((e) => {
      if (e && e.name === "AbortError") return;
      if (seq !== detailSeq) return;
      $("detail-stats").textContent = "加载失败";
      $("detail-body").innerHTML = `<div class="empty"><div>加载失败</div><div class="hint">${esc(e.message)}</div><button type="button" class="btn" data-detail-retry>重试</button></div>`;
      notify(e.message);
    });

  setTimeout(() => {
    const close = $("close");
    if (close && close.focus) close.focus();
  }, 0);
}

export function closeDetail(opts = {}) {
  detailSeq++;
  if (abort) abort.abort();
  cancelChunks();
  $("overlay").hidden = true;
  state.current = null;
  state.msgs = [];
  state.visible = [];
  document.body.classList.remove("modal-open");
  $("rail").classList.remove("open");
  if ($("detail-rail-toggle")) $("detail-rail-toggle").classList.remove("active");
  if (state.lastFocus && state.lastFocus.focus) state.lastFocus.focus();
  if (!opts.fromURL) writeURL(true);
}

/* ---------- export ---------- */
// transcriptMarkdown renders a whole conversation as Markdown for export.
function transcriptMarkdown(s, msgs) {
  const meta = [`Agent: ${s.agent}`, `ID: ${s.id}`];
  if (s.cwd) meta.push(`CWD: ${s.cwd}`);
  if (s.model) meta.push(`Model: ${s.model}`);
  if (s.createdAt) meta.push(`Created: ${fmtDate(s.createdAt)}`);
  meta.push(`Updated: ${fmtDate(s.updatedAt)}`);
  const parts = [`# ${displayTitle(s)}`, "", ...meta.map((m) => "- " + m), ""];
  for (const m of msgs) {
    const role = m.role || "system";
    const ts = m.timestamp ? new Date(m.timestamp) : null;
    const when = ts && !isNaN(ts) && ts.getFullYear() > 2000 ? ` — ${fmtDate(ts)}` : "";
    if (role === "tool" || role === "system") {
      parts.push(`## ${role}${when}`, "", "```", m.text || "", "```", "");
    } else if (role === "compact") {
      const quoted = (m.text || "(未记录摘要)").split("\n").map((l) => "> " + l).join("\n");
      parts.push(`## 上下文压缩${when}`, "", quoted, "");
    } else {
      parts.push(`## ${role}${when}`, "", m.text || "", "");
    }
  }
  return parts.join("\n");
}

function exportName(s, ext) {
  return safeFilename(displayTitle(s), s.id) + "-" + s.id.slice(0, 8) + "." + ext;
}

/* ---------- find / jump ---------- */
function clearFindHit() {
  document.querySelectorAll(".find-hit").forEach((n) => n.classList.remove("find-hit"));
}

function jumpTo(i) {
  const box = $("detail-body");
  const el = box && box.querySelector(`.msg[data-i="${i}"]`);
  if (!el) return;
  clearFindHit();
  el.classList.add("find-hit");
  const top = Math.max(0, el.offsetTop - 8);
  if (box.scrollTo) box.scrollTo({ top, behavior: scrollBehavior() });
  else box.scrollTop = top;
  updateRailActive(i);
}

function updateFindCount() {
  const el = $("find-count");
  if (!el) return;
  const f = state.find || { hits: [], pos: -1 };
  el.textContent = f.hits.length ? `${f.pos + 1}/${f.hits.length}` : (f.q ? "0/0" : "");
}

export function findInTranscript(q, dir) {
  q = String(q || "").trim();
  if (!q) {
    state.find = { q: "", hits: [], pos: -1 };
    clearFindHit();
    updateFindCount();
    return;
  }
  if (!state.find || state.find.q !== q) {
    const lq = q.toLowerCase();
    state.find = {
      q,
      hits: (state.visible || []).filter((x) => (x.full || "").toLowerCase().includes(lq)).map((x) => x.i),
      pos: -1,
    };
  }
  if (!state.find.hits.length) {
    updateFindCount();
    notify("没有找到「" + q + "」");
    return;
  }
  const step = dir < 0 ? -1 : 1;
  state.find.pos = (state.find.pos + step + state.find.hits.length) % state.find.hits.length;
  jumpTo(state.find.hits[state.find.pos]);
  updateFindCount();
}

// jumpUser moves to the next/previous user message in reading order.
export function jumpUser(dir) {
  const users = (state.visible || []).filter((x) => x.role === "user");
  if (!users.length) return;
  state.userPos = (state.userPos ?? -1) + (dir < 0 ? -1 : 1);
  if (state.userPos < 0) state.userPos = users.length - 1;
  if (state.userPos >= users.length) state.userPos = 0;
  jumpTo(users[state.userPos].i);
}

/* ---------- modal wiring ---------- */
$("detail-export").addEventListener("click", async () => {
  const btn = $("detail-export");
  if (!state.current || !state.msgs.length) return;
  const ok = await copyText(transcriptMarkdown(state.current, state.msgs));
  const label = btn.querySelector(".act-label") || btn;
  const old = label.textContent;
  label.textContent = ok ? "已复制" : "复制失败";
  setTimeout(() => {
    label.textContent = old;
  }, 1300);
});

$("detail-download-md").addEventListener("click", () => {
  if (!state.current || !state.msgs.length) return;
  download(exportName(state.current, "md"), transcriptMarkdown(state.current, state.msgs), "text/markdown;charset=utf-8");
});

$("detail-download-json").addEventListener("click", () => {
  if (!state.current) return;
  download(
    exportName(state.current, "json"),
    JSON.stringify({ session: state.current, messages: state.msgs }, null, 2),
    "application/json;charset=utf-8"
  );
});

// From the detail modal, the subagent tag jumps to the main session.
$("detail-title").addEventListener("click", (e) => {
  const tag = e.target.closest(".tag-sub");
  if (!tag || !tag.dataset.parent) return;
  const s = findSession(tag.dataset.agent, tag.dataset.parent);
  if (s) openDetail(s);
});

// Copy the directory or the session id from the header.
$("detail-meta").addEventListener("click", async (e) => {
  const b = e.target.closest(".dm-cwd");
  if (!b || !state.current) return;
  const ok = await copyText(state.current.cwd || "");
  if (ok) notify("目录已复制");
});
$("detail-id").addEventListener("click", async () => {
  if (!state.current) return;
  if (await copyText(state.current.id)) notify("会话 ID 已复制");
});

$("detail-tags").addEventListener("click", (e) => {
  if (!state.current) return;
  if (e.target.closest("[data-tag-edit]")) editTags(state.current);
  else if (e.target.closest("[data-tag-save]")) saveTags(state.current);
});
$("detail-tags").addEventListener("keydown", (e) => {
  if (!state.current || e.target.id !== "tag-input") return;
  if (e.key === "Enter") {
    e.preventDefault();
    e.stopPropagation();
    saveTags(state.current);
  } else if (e.key === "Escape") {
    e.preventDefault();
    e.stopPropagation();
    renderTags(state.current);
  }
});

$("show-noise").addEventListener("change", (e) => {
  state.noise = e.target.checked;
  store.set("as-noise", state.noise);
  $("detail-body").classList.toggle("show-noise", state.noise);
  // Noise is not rendered while hidden, so a toggle re-renders the rows and
  // rebuilds the rail; the scroll position is preserved by renderMessages.
  if (state.msgs.length) renderMessages(state.msgs);
  if ($("find")) $("find").value = "";
  state.find = { q: "", hits: [], pos: -1 };
  updateFindCount();
});

/* outline rail: click to jump, scroll keeps the highlight in sync */
$("rail-list").addEventListener("click", (e) => {
  const b = e.target.closest(".rail-item");
  if (!b) return;
  const el = $("detail-body").querySelector(`.msg[data-i="${b.dataset.i}"]`);
  if (!el) return;
  state.railLock = true;
  updateRailActive(Number(b.dataset.i));
  const top = Math.max(0, el.offsetTop - 8);
  const box = $("detail-body");
  if (box.scrollTo) box.scrollTo({ top, behavior: scrollBehavior() });
  else box.scrollTop = top;
  $("rail").classList.remove("open");
});
$("detail-body").addEventListener("scroll", onMsgScroll);
["wheel", "touchmove"].forEach((ev) =>
  $("detail-body").addEventListener(ev, () => {
    state.railLock = false;
  }, { passive: true })
);
document.addEventListener("keydown", (e) => {
  if (["PageUp", "PageDown", "Home", "End", "ArrowUp", "ArrowDown", " "].includes(e.key)) state.railLock = false;
});
window.addEventListener("resize", rafThrottle(updateOffsets));

/* detail cross-links open the target session */
$("detail-rel").addEventListener("click", (e) => {
  const b = e.target.closest(".rel-parent, .rel-row");
  if (!b) return;
  const s = state.relations.find((x) => x.agent === b.dataset.agent && x.id === b.dataset.id);
  if (s) openDetail(s);
});

/* message actions: copy, expand, reveal a blocked image */
document.addEventListener("click", async (e) => {
  const copy = e.target.closest(".msg-copy");
  if (copy) {
    const m = state.msgs[Number(copy.dataset.copy)];
    if (!m) return;
    const ok = await copyText(m.text || "");
    copy.textContent = ok ? "✓" : "✕";
    setTimeout(() => {
      copy.textContent = "复制";
    }, 1100);
    return;
  }
  const expand = e.target.closest(".msg-expand");
  if (expand) {
    const msg = expand.closest(".msg");
    const body = msg && msg.querySelector(".msg-body");
    if (!body) return;
    const open = body.classList.toggle("clamped") === false;
    expand.textContent = open ? "收起" : "展开全部";
    updateOffsets();
    return;
  }
  const img = e.target.closest(".md-img-blocked");
  if (img) {
    const src = img.dataset.src;
    if (!src) return;
    const el = document.createElement("img");
    el.className = "md-img";
    el.loading = "lazy";
    el.referrerPolicy = "no-referrer";
    el.src = src;
    el.alt = img.textContent;
    img.replaceWith(el);
    updateOffsets();
    return;
  }
  const retry = e.target.closest("[data-detail-retry]");
  if (retry && state.current) openDetail(state.current, { fromURL: true });
});

/* copy button on rendered code blocks */
document.addEventListener("click", async (e) => {
  const b = e.target.closest(".code-copy");
  if (!b) return;
  const code = b.parentElement && b.parentElement.querySelector("code");
  if (!code) return;
  try {
    await navigator.clipboard.writeText(code.textContent);
    b.textContent = "已复制";
    setTimeout(() => {
      b.textContent = "复制";
    }, 1200);
  } catch (err) {}
});

/* wide / font / rail controls */
$("detail-wide").addEventListener("click", () => {
  state.transcriptWide = !state.transcriptWide;
  store.set("as-wide", state.transcriptWide);
  $("modal").classList.toggle("wide", state.transcriptWide);
  syncWide();
  setTimeout(updateOffsets, 50);
});
$("detail-font").addEventListener("click", () => {
  state.transcriptFont = ((state.transcriptFont || 0) + 1) % 3;
  store.set("as-font", state.transcriptFont);
  setFont(state.transcriptFont);
  setTimeout(updateOffsets, 50);
});
$("detail-rail-toggle").addEventListener("click", () => {
  const open = $("rail").classList.toggle("open");
  const btn = $("detail-rail-toggle");
  btn.classList.toggle("active", open);
  btn.setAttribute("aria-pressed", open ? "true" : "false");
});

/* find bar */
$("detail-find-toggle").addEventListener("click", () => {
  const bar = $("find-bar");
  bar.hidden = !bar.hidden;
  const btn = $("detail-find-toggle");
  btn.classList.toggle("active", !bar.hidden);
  btn.setAttribute("aria-pressed", bar.hidden ? "false" : "true");
  if (!bar.hidden && $("find").focus) $("find").focus();
});
$("find").addEventListener("input", (e) => findInTranscript(e.target.value, 1));
$("find").addEventListener("keydown", (e) => {
  if (e.key === "Enter") {
    e.preventDefault();
    findInTranscript(e.target.value, e.shiftKey ? -1 : 1);
  } else if (e.key === "Escape") {
    e.preventDefault();
    e.stopPropagation();
    $("find-bar").hidden = true;
    const btn = $("detail-find-toggle");
    if (btn) {
      btn.classList.remove("active");
      btn.setAttribute("aria-pressed", "false");
    }
    state.find = { q: "", hits: [], pos: -1 };
    clearFindHit();
    updateFindCount();
  }
});
$("detail-body").addEventListener("keydown", (e) => {
  if (e.key !== "Enter" && e.key !== " ") return;
  const b = e.target.closest && e.target.closest(".md-img-blocked");
  if (!b) return;
  e.preventDefault();
  b.click();
});
$("find-prev").addEventListener("click", () => findInTranscript($("find").value, -1));
$("find-next").addEventListener("click", () => findInTranscript($("find").value, 1));
$("detail-prev-user").addEventListener("click", () => jumpUser(-1));
$("detail-next-user").addEventListener("click", () => jumpUser(1));

$("close").addEventListener("click", () => closeDetail());
$("overlay").addEventListener("click", (e) => {
  if (e.target.id === "overlay") closeDetail();
});
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && !$("overlay").hidden) closeDetail();
});

// Keep Tab inside the dialog while it is open.
$("modal").addEventListener("keydown", (e) => {
  if (e.key !== "Tab") return;
  const nodes = Array.from($("modal").querySelectorAll('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])')).filter(
    (n) => !n.disabled && n.offsetParent !== null
  );
  if (!nodes.length) return;
  const first = nodes[0];
  const last = nodes[nodes.length - 1];
  if (e.shiftKey && document.activeElement === first) {
    e.preventDefault();
    last.focus();
  } else if (!e.shiftKey && document.activeElement === last) {
    e.preventDefault();
    first.focus();
  }
});
