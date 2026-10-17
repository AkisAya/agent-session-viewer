// api.js — thin JSON client for the local HTTP API.
//
// Every call resolves with the decoded body, or rejects with an Error whose
// `message` is safe to show to the user (the server returns {"error": "..."}).

import { state } from "./state.js";

async function request(path, opts = {}) {
  const { params, signal, method } = opts;
  const qs = params && params.toString() ? "?" + params : "";
  let res;
  try {
    res = await fetch(path + qs, { method, signal });
  } catch (e) {
    if (e && e.name === "AbortError") throw e;
    throw new Error("无法连接到本地服务，请确认它仍在运行");
  }
  if (!res.ok) throw new Error(await errorText(res));
  return res.json();
}

// errorText prefers the server's JSON error message, falling back to the code.
async function errorText(res) {
  try {
    const body = await res.json();
    if (body && body.error) return body.error;
  } catch (e) {}
  return "请求失败（HTTP " + res.status + "）";
}

export const api = {
  stats: (params) => request("/api/stats", { params }),
  projects: (params) => request("/api/projects", { params }),
  sessions: (params, signal) => request("/api/sessions", { params, signal }),
  trend: (params) => request("/api/trend", { params }),
  tools: (params) => request("/api/tools", { params }),
  models: (params) => request("/api/models", { params }),
  relations: () => request("/api/relations"),
  transcript: (agent, id) => request(`/api/sessions/${encodeURIComponent(agent)}/${encodeURIComponent(id)}`),
  rescan: () => request("/api/rescan", { method: "POST" }),
};

// scopeParams mirrors the current agent tab and directory filter, merged with
// any extra query values.
export function scopeParams(extra) {
  const p = new URLSearchParams();
  if (state.agent) p.set("agent", state.agent);
  if (state.cwd) p.set("cwd", state.cwd);
  for (const k of Object.keys(extra || {})) p.set(k, String(extra[k]));
  return p;
}
