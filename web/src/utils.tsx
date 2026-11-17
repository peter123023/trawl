import type { Span } from "./types";

export function ms(value: number): string {
  const d = Number(value) || 0;
  return d >= 1000 ? `${(d / 1000).toFixed(2)}s` : `${d}ms`;
}

export function money(value: number): string {
  const n = Number(value || 0);
  if (!n) return "—";
  const s = n < 1 ? n.toPrecision(3) : n.toFixed(2);
  return `$${String(Number(s))}`;
}

export function when(t: number): string {
  return new Date(Number(t)).toLocaleString("zh-CN", { hour12: false });
}

export function dayLabel(): string {
  return new Date().toLocaleDateString("zh-CN");
}

export function depthOf(spans: Span[]): Map<string, number> {
  const byId = new Map(spans.map((s) => [s.spanId, s]));
  const depth = new Map<string, number>();
  spans.forEach((s) => {
    let d = 0;
    let cur: Span | undefined = s;
    while (cur?.parentSpanId && byId.has(cur.parentSpanId) && d < 32) {
      d += 1;
      cur = byId.get(cur.parentSpanId);
    }
    depth.set(s.spanId, d);
  });
  return depth;
}

export function orderTree(spans: Span[]): Span[] {
  const byId = new Map(spans.map((s) => [s.spanId, s]));
  const kids = new Map<string, Span[]>();
  const roots: Span[] = [];
  spans.forEach((s) => {
    if (s.parentSpanId && byId.has(s.parentSpanId)) {
      const list = kids.get(s.parentSpanId) ?? [];
      list.push(s);
      kids.set(s.parentSpanId, list);
    } else {
      roots.push(s);
    }
  });
  const out: Span[] = [];
  const seen = new Set<string>();
  const walk = (s: Span) => {
    if (seen.has(s.spanId)) return;
    seen.add(s.spanId);
    out.push(s);
    (kids.get(s.spanId) ?? [])
      .sort((a, b) => a.startTime - b.startTime)
      .forEach(walk);
  };
  roots.sort((a, b) => a.startTime - b.startTime).forEach(walk);
  spans.forEach((s) => {
    if (!seen.has(s.spanId)) {
      seen.add(s.spanId);
      out.push(s);
    }
  });
  return out;
}

export function metaLine(span: Span) {
  let usage: Record<string, number> = {};
  let cost: Record<string, number> = {};
  try {
    usage = JSON.parse(span.usageDetails || "{}") ?? {};
  } catch {
  }
  try {
    cost = JSON.parse(span.costDetails || "{}") ?? {};
  } catch {
  }
  const total = Number(usage.total || 0);
  const inout = Number(usage.input || 0)
    ? ` (in ${usage.input} / out ${usage.output || 0})`
    : "";
  return (
    <>
      <span>model {span.model || "—"}</span>
      <span>
        tokens {total}
        {inout}
      </span>
      <span>cost {money(cost.total)}</span>
      <span>level {span.level}</span>
      {span.promptName && (
        <span>
          prompt {span.promptName} v{span.promptVersion ?? 0}
        </span>
      )}
    </>
  );
}
