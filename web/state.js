// state.js — the single mutable application state, URL <-> state sync, theming
// and cross-view lookups. Modules import `state` directly and mutate its fields.

export const state = {
  agent: "",
  cwd: "",
  q: "",
  sort: "updated",
  view: "list",
  projects: [],
  sessions: [],
  theme: "auto",
  noise: false,
  lastScan: "",
  // dashboard data (scoped by agent + cwd)
  trend: [],
  models: [],
  agents: [],
  total: {},
  tools: { tools: [], total: 0 },
  // heatmap / trend display settings
  hmRange: 180,
  hmMetric: "msgs",
  // active single-day filter (YYYY-MM-DD), set by clicking a heatmap cell
  day: "",
  // every session involved in a parent/child link, fetched once and filtered
  // client side so the detail view can link across scopes.
  relations: [],
  // transcript of the session currently open in the detail modal
  msgs: [],
  current: null,
  // true while the outline rail highlight follows a click instead of the
  // scroll position (a jump that cannot reach the top of the viewport).
  railLock: false,
};

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
  if (p.has("agent")) state.agent = p.get("agent") || "";
  if (p.has("cwd")) state.cwd = p.get("cwd") || "";
  if (p.get("view") === "dash") state.view = "dash";
  if (p.get("sort")) state.sort = p.get("sort");
  if (p.has("q")) state.q = p.get("q") || "";
  if (p.has("day")) state.day = p.get("day") || "";
}

export function writeURL() {
  const p = new URLSearchParams();
  if (state.agent) p.set("agent", state.agent);
  if (state.cwd) p.set("cwd", state.cwd);
  if (state.view !== "list") p.set("view", state.view);
  if (state.sort !== "updated") p.set("sort", state.sort);
  if (state.q) p.set("q", state.q);
  if (state.day) p.set("day", state.day);
  const qs = p.toString();
  try {
    history.replaceState(null, "", qs ? "?" + qs : location.pathname);
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
