// util.js — shared, dependency-free helpers: DOM lookup, escaping, formatting,
// storage, clipboard, downloads and a small toast used to surface errors.
//
// Loaded as an ES module; every other frontend module imports from here.

export const $ = (id) => document.getElementById(id);

// esc escapes the characters that matter in HTML text and attribute values.
export const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

export const TAB_LABEL = { "": "全部", claude: "Claude", codex: "Codex", opencode: "OpenCode", dsh: "DSH" };
export const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/* ---------- motion preference ---------- */
const reduceMotion = matchMedia("(prefers-reduced-motion: reduce)").matches;
export const scrollBehavior = () => (reduceMotion ? "auto" : "smooth");

/* ---------- localStorage ---------- */
export const store = {
  get(key, fallback) {
    try {
      const v = localStorage.getItem(key);
      return v === null ? fallback : JSON.parse(v);
    } catch (e) {
      return fallback;
    }
  },
  set(key, value) {
    try {
      localStorage.setItem(key, JSON.stringify(value));
    } catch (e) {}
  },
};

/* ---------- clipboard ---------- */
// copyText writes to the clipboard, falling back to a hidden textarea.
export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch (e) {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    let ok = false;
    try {
      ok = document.execCommand("copy");
    } catch (e2) {}
    document.body.removeChild(ta);
    return ok;
  }
}

// download saves a text payload as a file through a temporary object URL.
export function download(name, text, type) {
  const blob = new Blob([text], { type: type || "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/* ---------- number / date formatting ---------- */
export function fmtNum(n) {
  n = Number(n) || 0;
  if (n >= 1e9) return (n / 1e9).toFixed(2) + "B";
  if (n >= 1e6) return (n / 1e6).toFixed(2) + "M";
  if (n >= 1e5) return Math.round(n / 1e3) + "k";
  if (n >= 1e3) return (n / 1e3).toFixed(1) + "k";
  return String(n);
}

export function fmtTime(iso) {
  if (!iso || iso.startsWith("0001")) return "—";
  const d = new Date(iso);
  if (isNaN(d)) return "—";
  const diff = (Date.now() - d.getTime()) / 1000;
  if (diff < 60) return "刚刚";
  if (diff < 3600) return Math.floor(diff / 60) + " 分钟前";
  if (diff < 86400) return Math.floor(diff / 3600) + " 小时前";
  if (diff < 86400 * 30) return Math.floor(diff / 86400) + " 天前";
  return d.toLocaleDateString();
}

export function fmtDate(iso) {
  if (!iso || iso.startsWith("0001")) return "—";
  const d = new Date(iso);
  return isNaN(d) ? "—" : d.toLocaleString();
}

export function fmtClock(d) {
  return [d.getHours(), d.getMinutes(), d.getSeconds()].map((n) => String(n).padStart(2, "0")).join(":");
}

export function fmtDayLabel(d) {
  return d.toLocaleDateString(undefined, { year: "numeric", month: "2-digit", day: "2-digit", weekday: "short" });
}

// dayKey formats a Date as YYYY-MM-DD in local time (matches the Go side).
export function dayKey(d) {
  return d.getFullYear() + "-" + String(d.getMonth() + 1).padStart(2, "0") + "-" + String(d.getDate()).padStart(2, "0");
}

// dayLabel turns a local day key into a short heading: 今天 / 昨天 / 09-28.
export function dayLabel(key) {
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const d = new Date(key + "T00:00:00");
  const diff = Math.round((today - d) / 86400000);
  if (diff === 0) return "今天";
  if (diff === 1) return "昨天";
  if (diff > 1 && diff < 7) return diff + " 天前";
  return key.slice(5);
}

// fmtVersion shortens a binary version for the top bar. A source build between
// tags reports a Go pseudo-version (v0.7.1-0.20261023093836-e2b24a911967),
// shown as "v0.7.1-dev (e2b24a9)"; everything else is shown as is.
export function fmtVersion(v) {
  v = String(v || "dev");
  if (/^\d/.test(v)) v = "v" + v;
  const m = /^(.*?)[-.](?:0\.)?\d{14}-([0-9a-f]{12})(\+dirty)?$/.exec(v);
  if (!m) return v;
  return `${m[1]}-dev (${m[2].slice(0, 7)}${m[3] ? ", dirty" : ""})`;
}

/* ---------- session / path formatting ---------- */
// displayTitle prefers a real title, then the first user-message preview.
export function displayTitle(s) {
  const t = (s.title || "").trim();
  const named = t && !UUID_RE.test(t) && !/^new session\b/i.test(t);
  if (named) return t;
  const p = (s.preview || "").trim();
  if (p) return p.split("\n")[0].slice(0, 90);
  return "未命名会话";
}

export const sessionKey = (agent, id) => agent + "|" + id;

// shortPath renders a directory as "parent/last" and accepts Windows paths.
export function shortPath(cwd) {
  if (!cwd) return "(未知目录)";
  const parts = String(cwd).replace(/\\/g, "/").split("/").filter(Boolean);
  if (!parts.length) return "(未知目录)";
  if (parts.length <= 2) return parts.join("/");
  return parts.slice(-2).join("/");
}

export const cwdKey = (cwd) => (cwd === "" ? "__none__" : cwd);

// midPath shortens a long path in the middle so the tail — the part that
// identifies the project — stays readable. The full path stays on the title.
export function midPath(p, max) {
  max = max || 42;
  if (!p || p.length <= max) return p;
  const head = 12;
  return p.slice(0, head) + "…" + p.slice(p.length - (max - head - 1));
}

// shortModel keeps the identifying tail of a provider-qualified model id.
export function shortModel(m) {
  const last = String(m || "").split("/").pop();
  return last.length > 18 ? last.slice(0, 17) + "…" : last;
}

// hl escapes text then wraps case-insensitive matches of q in <mark>.
export function hl(text, q) {
  const e = esc(text);
  if (!q) return e;
  const eq = esc(q);
  const lower = e.toLowerCase();
  const lq = eq.toLowerCase();
  let out = "", i = 0;
  for (;;) {
    const idx = lower.indexOf(lq, i);
    if (idx < 0) return out + e.slice(i);
    out += e.slice(i, idx) + "<mark>" + e.slice(idx, idx + eq.length) + "</mark>";
    i = idx + eq.length;
  }
}

/* ---------- misc ---------- */
export function debounce(fn, ms) {
  let t;
  return (...a) => {
    clearTimeout(t);
    t = setTimeout(() => fn(...a), ms);
  };
}

// rafThrottle coalesces bursts of events (scroll, resize) into one frame.
export function rafThrottle(fn) {
  let pending = false;
  return (...args) => {
    if (pending) return;
    pending = true;
    const run = () => {
      pending = false;
      fn(...args);
    };
    if (typeof requestAnimationFrame === "function") requestAnimationFrame(run);
    else setTimeout(run, 16);
  };
}

// csvCell quotes a value for CSV output.
export function csvCell(v) {
  const s = String(v ?? "");
  return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
}

export function toCSV(head, rows) {
  return [head, ...rows].map((r) => r.map(csvCell).join(",")).join("\n") + "\n";
}

// safeFilename strips characters that are illegal in a download name.
export function safeFilename(s, fallback) {
  const out = String(s || "").replace(/[\\/:*?"<>|\n\r]+/g, "_").trim().slice(0, 60);
  return out || fallback || "session";
}

/* ---------- toast ---------- */
// notify shows a transient message at the bottom of the viewport. Repeated
// messages within the same window collapse so a burst of failing requests
// (e.g. the service is gone) does not stack up.
let toastTimer = 0;
let toastMsg = "";
let toastAt = 0;

export function notify(msg) {
  const el = $("toast");
  if (!el || !msg) return;
  const now = Date.now();
  if (msg === toastMsg && now - toastAt < 4000) return;
  toastMsg = msg;
  toastAt = now;
  el.textContent = msg;
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    el.hidden = true;
  }, 4000);
}
