// detail.js — the session transcript modal: messages, the outline rail,
// parent/child relations, markdown export and the modal's own event wiring.

import { $, esc, fmtNum, fmtDate, fmtClock, fmtDayLabel, displayTitle, dayKey, scrollBehavior, copyText, notify } from "./util.js";
import { state, findSession } from "./state.js";
import { api } from "./api.js";

export async function loadRelations() {
  try {
    state.relations = (await api.relations()) || [];
  } catch (e) {
    state.relations = [];
    notify(e.message);
  }
  if (state.current) renderDetailRel(state.current);
}

/* ---------- transcript rendering ---------- */
const NOISE_ROLES = ["reasoning", "tool"];
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

function renderMessages(msgs) {
  let prevDay = "";
  const html = msgs
    .map((m, i) => {
      const role = m.role || "system";
      const ts = m.timestamp ? new Date(m.timestamp) : null;
      const valid = ts && !isNaN(ts) && ts.getFullYear() > 2000;
      const day = valid ? dayKey(ts) : "";
      const divider = valid && day !== prevDay ? `<div class="msg-day"><span>${esc(fmtDayLabel(ts))}</span></div>` : "";
      if (valid) prevDay = day;
      const time = valid ? `<span class="msg-time">${esc(fmtClock(ts))}</span>` : "";
      return `${divider}<div class="msg ${esc(role)}" data-i="${i}">
        <div class="msg-role">${esc(role)}${time}</div>
        ${msgBodyHTML(m)}
      </div>`;
    })
    .join("");
  $("detail-body").innerHTML = html;
  $("detail-body").classList.toggle("big", msgs.length > 300);
  buildRail(msgs);
}

// railSnippet flattens a message into a one-line outline entry.
function railSnippet(text) {
  const s = String(text || "")
    .replace(/```[\s\S]*?```/g, " [code] ")
    .replace(/[#>*`|_-]/g, " ")
    .replace(/\s+/g, " ")
    .trim();
  if (!s) return "(空)";
  return s.length > 46 ? s.slice(0, 46) + "…" : s;
}

// buildRail fills the outline rail, skipping messages hidden by the noise
// toggle so every entry maps to a scrollable row.
function buildRail(msgs) {
  const rail = $("rail");
  const list = $("rail-list");
  const items = [];
  msgs.forEach((m, i) => {
    const role = m.role || "system";
    if (!state.noise && isNoise(role)) return;
    items.push({ i, role, text: railSnippet(m.text) });
  });
  if (items.length < 3) {
    rail.hidden = true;
    list.innerHTML = "";
    return;
  }
  rail.hidden = false;
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
  if (list.hidden) return;
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
// active entry is the message occupying the top of the viewport (or, when
// scrolled to the very bottom, the last message).
// railLock keeps the clicked outline entry highlighted while the transcript
// is scrolled programmatically; it is released by a real user scroll.
function onMsgScroll() {
  if (state.railLock) return;
  const box = $("detail-body");
  const nodes = box.children;
  if (!nodes.length) return;
  const top = box.scrollTop + 48;
  let active = null;
  for (let i = 0; i < nodes.length; i++) {
    const n = nodes[i];
    if (n.dataset.i === undefined) continue; // day divider, not a message
    if (!n.offsetParent) continue; // hidden by the noise toggle
    active = n.dataset.i;
    if (n.offsetTop + n.offsetHeight > top) break;
  }
  if (active !== null) updateRailActive(Number(active));
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
  const link = (x) =>
    `<button type="button" class="rel-link" data-agent="${esc(x.agent)}" data-id="${esc(x.id)}" title="${esc(x.id)}">${esc(displayTitle(x))}</button>`;
  const parts = [];
  if (parent) {
    // A subagent's parent is its main session; everything else is a lineage.
    parts.push(`<span class="rel-label">${s.relation === "subagent" ? "主会话" : "续接自"}</span>${link(parent)}`);
  }
  if (kids.length) {
    const label = kids.every((k) => k.relation === "subagent") ? `${kids.length} 个 subagent` : `后续 ${kids.length} 个`;
    parts.push(`<span class="rel-label">${label}</span>` + kids.map(link).join("、"));
  }
  el.hidden = false;
  el.innerHTML = parts.join("　·　");
}

export function openDetail(s) {
  state.current = s;
  const overlay = $("overlay");
  overlay.hidden = false;
  $("detail-title").innerHTML =
    (s.relation === "subagent"
      ? `<button type="button" class="tag-sub" data-agent="${esc(s.agent)}" data-parent="${esc(s.parentId)}" title="点击查看主会话">subagent</button>`
      : "") + esc(displayTitle(s));
  $("detail-meta").textContent = [s.agent, s.cwd || "(未知目录)", s.model].filter(Boolean).join("  ·  ");
  $("detail-stats").textContent = "加载中…";
  $("detail-body").innerHTML = "";
  $("detail-body").scrollTop = 0;
  state.railLock = false;
  $("rail-list").innerHTML = "";
  $("rail").hidden = true;
  $("detail-body").classList.toggle("show-noise", state.noise);
  renderDetailRel(s);

  api
    .transcript(s.agent, s.id)
    .then((msgs) => {
      state.msgs = msgs || [];
      const noise = state.msgs.filter((m) => isNoise(m.role)).length;
      const reasoning = state.msgs.filter((m) => m.role === "reasoning").length;
      const toolMsgs = state.msgs.filter((m) => m.role === "tool").length;
      const talk = state.msgs.length - noise;
      const tokens = (s.tokensIn || 0) + (s.tokensOut || 0) + (s.cacheRead || 0);
      $("detail-stats").textContent = [
        `${talk} 条对话`,
        reasoning ? `${reasoning} 条思考` : "",
        toolMsgs ? `${toolMsgs} 条工具` : "",
        `${fmtNum(tokens)} tokens`,
        s.cost ? `$${Number(s.cost).toFixed(2)}` : "",
        fmtDate(s.createdAt),
        s.id,
      ]
        .filter(Boolean)
        .join("  ·  ");
      renderMessages(state.msgs);
    })
    .catch((e) => {
      $("detail-stats").textContent = "加载失败";
      notify(e.message);
    });
}

export function closeDetail() {
  $("overlay").hidden = true;
  state.current = null;
}

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
    } else {
      parts.push(`## ${role}${when}`, "", m.text || "", "");
    }
  }
  return parts.join("\n");
}

/* ---------- modal wiring ---------- */
$("detail-export").addEventListener("click", async () => {
  const btn = $("detail-export");
  if (!state.current || !state.msgs.length) return;
  const ok = await copyText(transcriptMarkdown(state.current, state.msgs));
  const old = btn.textContent;
  btn.textContent = ok ? "已复制" : "复制失败";
  setTimeout(() => {
    btn.textContent = old;
  }, 1300);
});

// From the detail modal, the subagent tag jumps to the main session.
$("detail-title").addEventListener("click", (e) => {
  const tag = e.target.closest(".tag-sub");
  if (!tag || !tag.dataset.parent) return;
  const s = findSession(tag.dataset.agent, tag.dataset.parent);
  if (s) openDetail(s);
});

$("show-noise").addEventListener("change", (e) => {
  state.noise = e.target.checked;
  $("detail-body").classList.toggle("show-noise", state.noise);
  // The rail mirrors what is visible, so rebuild it and re-anchor the highlight.
  if (state.msgs.length) {
    buildRail(state.msgs);
    onMsgScroll();
  }
});

/* outline rail: click to jump, scroll keeps the highlight in sync */
$("rail-list").addEventListener("click", (e) => {
  const b = e.target.closest(".rail-item");
  if (!b) return;
  const box = $("detail-body");
  const el = box.querySelector(`.msg[data-i="${b.dataset.i}"]`);
  if (!el) return;
  // A short message near the end cannot reach the top of the viewport, so
  // trust the click until the user scrolls again.
  state.railLock = true;
  updateRailActive(Number(b.dataset.i));
  const top = Math.max(0, el.offsetTop - 8);
  if (box.scrollTo) box.scrollTo({ top, behavior: scrollBehavior() });
  else box.scrollTop = top;
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

/* detail cross-links open the target session */
$("detail-rel").addEventListener("click", (e) => {
  const b = e.target.closest(".rel-link");
  if (!b) return;
  const s = state.relations.find((x) => x.agent === b.dataset.agent && x.id === b.dataset.id);
  if (s) openDetail(s);
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

$("close").addEventListener("click", closeDetail);
$("overlay").addEventListener("click", (e) => {
  if (e.target.id === "overlay") closeDetail();
});
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") closeDetail();
});
