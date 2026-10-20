// pricing.js — rough per-million-token prices for models that do not record a
// cost themselves (Codex, DSH, some OpenCode providers). Shown as "≈" in the
// UI; real costs from the source always win. The table is a convenience, not
// billing data: edit it when a provider changes prices.
//
// cache is the cache-read price; tokensIn already excludes cacheRead.

const RATES = [
  { re: /claude.*opus/i, in: 5, out: 25, cache: 0.5 },
  { re: /claude.*sonnet/i, in: 3, out: 15, cache: 0.3 },
  { re: /claude.*haiku/i, in: 1, out: 5, cache: 0.1 },
  { re: /gpt-5/i, in: 1.25, out: 10, cache: 0.125 },
  { re: /^o[34]/i, in: 2, out: 8, cache: 0.5 },
  { re: /deepseek/i, in: 0.28, out: 0.42, cache: 0.028 },
  { re: /gemini.*pro/i, in: 1.25, out: 10, cache: 0.31 },
  { re: /gemini/i, in: 0.3, out: 2.5, cache: 0.075 },
];

export function ratesFor(model) {
  const m = String(model || "");
  return RATES.find((r) => r.re.test(m)) || null;
}

// estimateCost returns 0 when the model is unknown.
export function estimateCost(m) {
  const r = ratesFor(m && m.model);
  if (!r) return 0;
  return (
    ((m.tokensIn || 0) * r.in + (m.tokensOut || 0) * r.out + (m.cacheRead || 0) * r.cache) / 1e6
  );
}

// costOf prefers the cost recorded by the source, falling back to an estimate.
export function costOf(s) {
  const real = Number((s && s.cost) || 0);
  if (real > 0) return { value: real, estimated: false };
  const est = estimateCost(s);
  return est > 0 ? { value: est, estimated: true } : { value: 0, estimated: false };
}

// fmtCost renders a costOf result. Very small values stay visible.
export function fmtCost(c) {
  if (!c || !c.value) return "—";
  const v = c.value < 0.01 ? "<$0.01" : "$" + c.value.toFixed(2);
  return c.estimated ? "≈" + v : v;
}
