// state.js — the single mutable application state, URL <-> state sync, theming,
// small preferences and cross-view lookups. Modules import `state` directly
// and mutate its fields.

import { store } from "./util.js";

export const state = {
  agent: "",
  cwd: "",
  q: "",
  sort: "updated",
  view: "list",
  mode: store.get("as-mode", "cards"),
  showSubagents: store.get("as-subagents", false),
  projects: [],
  projectFilter: "",
  projectOrder: store.get("as-project-order", "count"),
  sessions: [],
  total: 0,
  pageSize: 100,
  allLoaded: false,
  theme: "auto",
  noise: store.get("as-noise", false),
  lastScan: "",
  scan: null,
  // dashboard data (scoped by agent + cwd)
  trend: [],
  hours: [],
  models: [],
  agents: [],
  totalStats: {},
  tools: { tools: [], total: 0 },
  top: [],
  // display settings
  hmRange: store.get("as-hm-range", 180),
  hmMetric: store.get("as-hm-metric", "msgs"),
  rhythmMetric: store.get("as-rhythm-metric", "msgs"),
  modelsMetric: store.get("as-models-metric", "tokens"),
  toolsMetric: store.get("as-tools-metric", "count"),
  topMetric: store.get("as-top-metric", "cost"),
  showAllModels: false,
  showAllTools: false,
  showAllProjects: store.get("as-all-projects", false),
  agentSort: { key: "share", dir: -1 },
  projectSort: { key: "tokens", dir: -1 },
  // active single-day filter (YYYY-MM-DD), set by clicking a heatmap cell
  day: "",
  // every session involved in a parent/child link, fetched once and filtered
  // client side so the detail view can link across scopes.
  relations: [],
  // transcript of the session currently open in the detail modal
  msgs: [],
  current: null,
  // deep link parsed from the URL before the session list has loaded
  pendingSession: null,
  transcriptWide: store.get("as-wide", false),
  transcriptFont: store.get("as-font", 0),
  autoRefresh: store.get("as-auto", false),
  starred: new Set(store.get("as-starred", []) || []),
  tags: store.get("as-tags", {}) || {},
  // true while the outline rail highlight follows a click instead of the
  // scroll position (a jump that cannot reach the top of the viewport).
  railLock: false,
  lastFocus: null,
};

/* ---------- preferences ---------- */
export function savePref(key, value) {
  store.set(key, value);
}

/* ---------- favorites ---------- */
export function isStarred(agent, id) {
  return state.starred.has(agent + "|" + id);
}

export function toggleStar(agent, id) {
  const key = agent + "|" + id;
  if (state.starred.has(key)) state.starred.delete(key);
  else state.starred.add(key);
  store.set("as-starred", [...state.starred]);
  return state.starred.has(key);
}

/* ---------- tags ---------- */
export function tagsOf(agent, id) {
  const v = state.tags[agent + "|" + id];
  return Array.isArray(v) ? v : [];
}

export function setTags(agent, id, tags) {
  const key = agent + "|" + id;
  const clean = [...new Set(tags.map((t) => String(t).trim()).filter(Boolean))].slice(0, 8);
  if (clean.length) state.tags[key] = clean;
  else delete state.tags[key];
  store.set("as-tags", state.tags);
  return clean;
}

/* ---------- theme ---------- */
const THEME_KEY = "as-theme";
try {
  state.theme = localStorage.getItem(THEME_KEY) || "auto";
} catch (e) {}

export function setTheme(pref) {
  state.theme = pref;
  try {
    localStorage.setItem(THEME_KEY, pref);
  } catch (e) {}
  applyTheme();
}

export function applyTheme() {
  const pref = state.theme;
  const dark = pref === "dark" || (pref === "auto" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.setAttribute("data-theme", dark ? "dark" : "light");
  document.querySelectorAll("#theme-switch button").forEach((b) => b.classList.toggle("active", b.dataset.pref === pref));
}

/* ---------- url state ---------- */
// The active filters live in the query string so a view can be shared or
// bookmarked and the browser back button works.
export function readURL() {
  const p = new URLSearchParams(location.search);
  state.agent = p.get("agent") || "";
  state.cwd = p.has("cwd") ? p.get("cwd") || "" : "";
  state.q = p.get("q") || "";
  state.day = p.get("day") || "";
  state.sort = p.get("sort") || "updated";
  state.view = p.get("view") === "dash" ? "dash" : "list";
  const session = p.get("session") || "";
  const at = session.indexOf(":");
  state.pendingSession = at > 0 ? { agent: session.slice(0, at), id: session.slice(at + 1) } : null;
}

export function writeURL(push) {
  const p = new URLSearchParams();
  if (state.agent) p.set("agent", state.agent);
  if (state.cwd) p.set("cwd", state.cwd);
  if (state.view !== "list") p.set("view", state.view);
  if (state.sort !== "updated") p.set("sort", state.sort);
  if (state.q) p.set("q", state.q);
  if (state.day) p.set("day", state.day);
  if (state.current) p.set("session", state.current.agent + ":" + state.current.id);
  const qs = p.toString();
  const url = qs ? "?" + qs : location.pathname;
  try {
    if (push && history.pushState) history.pushState(null, "", url);
    else history.replaceState(null, "", url);
  } catch (e) {}
}

/* ---------- lookups ---------- */
// findSession resolves a session by agent + id from the loaded list or the
// relations payload (parents referenced by a subagent live in the latter).
export function findSession(agent, id) {
  return (
    state.sessions.find((x) => x.agent === agent && x.id === id) ||
    state.relations.find((x) => x.agent === agent && x.id === id) ||
    null
  );
}
