import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "../api";
import type { Dataset, Score, Span } from "../types";
import { depthOf, metaLine, ms, orderTree } from "../utils";
import { navigate } from "../App";

type DotKey = "faithful" | "accurate";

type QueuePos = { index: number; total: number; next?: string };

// Shows only the latest llm_judge per trace; older runs stay in scores.
// The visibleScores memo must run before any early return to keep hook order stable.
export default function Case({
  traceId,
  queueMode = false,
}: {
  traceId: string;
  queueMode?: boolean;
}) {
  const [spans, setSpans] = useState<Span[]>([]);
  const [scores, setScores] = useState<Score[]>([]);
  const [active, setActive] = useState("");
  const [dots, setDots] = useState<Record<DotKey, number | null>>({
    faithful: null,
    accurate: null,
  });
  const [reason, setReason] = useState("");
  const [stamp, setStamp] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [queuePos, setQueuePos] = useState<QueuePos | null>(null);
  const [judging, setJudging] = useState(false);
  const [judgeNote, setJudgeNote] = useState("");
  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [targetDs, setTargetDs] = useState("");
  const [collecting, setCollecting] = useState(false);
  const [collectNote, setCollectNote] = useState("");

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    setStamp("");
    setReason("");
    setDots({ faithful: null, accurate: null });
    api
      .trace(traceId)
      .then((d) => {
        if (!alive) return;
        setSpans(d.spans ?? []);
        setScores(d.scores ?? []);
        const pref =
          d.spans.find((s) => s.type === "generation") ||
          d.spans.find((s) => s.type !== "trace") ||
          d.spans[0];
        setActive(pref?.spanId ?? "");
      })
      .catch((e) => alive && setError(String((e as Error).message || e)))
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
    };
  }, [traceId]);

  useEffect(() => {
    if (!queueMode) {
      setQueuePos(null);
      return;
    }
    let alive = true;
    api
      .queue(100)
      .then((q) => {
        if (!alive) return;
        const index = q.items.findIndex((t) => t.traceId === traceId);
        setQueuePos({
          index,
          total: q.items.length,
          next: index >= 0 ? q.items[index + 1]?.traceId : undefined,
        });
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [queueMode, traceId]);

  const depth = useMemo(() => depthOf(spans), [spans]);
  const ordered = useMemo(() => orderTree(spans), [spans]);
  const current: Span | undefined = spans.find((s) => s.spanId === active) ?? spans[0];
  const rootName = spans.find((s) => !s.parentSpanId)?.name || current?.name || traceId;

  const submit = useCallback(
    async (v: number) => {
      if (busy) return;
      if (!reason.trim()) {
        window.alert("判词需要理由：没有依据的判定不可追溯。");
        return;
      }
      setBusy(true);
      try {
        for (const [key, val] of Object.entries(dots)) {
          if (val === null) continue;
          await api.addScore({
            traceId,
            spanId: current?.spanId,
            name: key,
            value: val,
            comment: reason,
          });
        }
        await api.addScore({
          traceId,
          spanId: current?.spanId,
          name: "verdict",
          value: v,
          comment: reason,
        });
        setStamp(v === 1 ? "通 过" : v === 0 ? "不 通 过" : "已 跳 过");

        const next = queuePos?.next;
        window.setTimeout(() => {
          if (queueMode) {
            navigate(next ? `/queue/${encodeURIComponent(next)}` : "/queue");
          } else {
            api
              .trace(traceId)
              .then((d) => setScores(d.scores ?? []))
              .catch(() => undefined);
          }
        }, 760);
      } catch (e) {
        window.alert(`判词写入失败：${(e as Error).message}`);
      } finally {
        setBusy(false);
      }
    },
    [busy, reason, dots, traceId, current?.spanId, queuePos, queueMode],
  );

  useEffect(() => {
    api
      .datasets()
      .then((d) => {
        setDatasets(d.datasets ?? []);
        if (d.datasets?.length) setTargetDs((cur) => cur || d.datasets[0].id);
      })
      .catch(() => {
        });
  }, []);

  const collect = useCallback(async () => {
    if (!targetDs || collecting) return;
    setCollecting(true);
    try {
      const res = await api.addDatasetItems(targetDs, [traceId]);
      setCollectNote(res.added ? "已加入" : "本来就在集里");
      const d = await api.datasets();
      setDatasets(d.datasets ?? []);
    } catch (e) {
      setCollectNote((e as Error).message);
    } finally {
      setCollecting(false);
    }
  }, [targetDs, collecting, traceId]);

  const runJudge = useCallback(async () => {
    if (judging) return;
    setJudging(true);
    setJudgeNote("");
    try {
      const res = await api.judge(traceId, current?.spanId);
      setJudgeNote(`机判 ${res.score} / 3 · ${res.reason}（${res.model}）`);
      const d = await api.trace(traceId);
      setScores(d.scores ?? []);
    } catch (e) {
      setJudgeNote(`机判不可用：${(e as Error).message}`);
    } finally {
      setJudging(false);
    }
  }, [judging, traceId, current?.spanId]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === "TEXTAREA" || tag === "INPUT") return;
      const i = ordered.findIndex((s) => s.spanId === active);
      if (e.key === "j" && i >= 0 && i + 1 < ordered.length) {
        setActive(ordered[i + 1].spanId);
      } else if (e.key === "k" && i > 0) {
        setActive(ordered[i - 1].spanId);
      } else if (e.key === " ") {
        e.preventDefault();
        void submit(1);
      } else if (e.key === "x" || e.key === "X") {
        void submit(0);
      } else if (e.key === "s" || e.key === "S") {
        void submit(-1);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [ordered, active, submit]);

  const visibleScores = useMemo(() => {
    const seen = new Set<string>();
    return scores.filter((s) => {
      const key = s.name === "llm_judge" ? "llm_judge" : `${s.name}:${s.id}`;
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
  }, [scores]);
  const judgeRuns = scores.filter((s) => s.name === "llm_judge").length;

  if (loading) {
    return (
      <main className="desk">
        <div className="loading">正在展开卷宗…</div>
      </main>
    );
  }

  if (error || !spans.length) {
    return (
      <main className="desk">
        <div className="error">{error || "卷宗未找到。"}</div>
      </main>
    );
  }

  return (
    <main className="desk">
      <div className="masthead">
        <h1>{rootName}</h1>
        <div className="tail">
          {queueMode && queuePos && queuePos.index >= 0
            ? `QUEUE ${queuePos.index + 1}/${queuePos.total} · `
            : ""}
          {traceId}
        </div>
      </div>

      <div className="case">
        <section className="col">
          <h2>证据</h2>
          <div className="spine">
            {ordered.map((s) => (
              <div
                key={s.spanId}
                className={`node ${s.spanId === active ? "active" : ""}`}
                style={{ marginLeft: (depth.get(s.spanId) ?? 0) * 14 }}
                onClick={() => setActive(s.spanId)}
              >
                <div className="n">
                  {s.name || s.type}
                  {s.level === "ERROR" && <span className="e">⚠ 异常</span>}
                </div>
                <div className="t">
                  {s.type} · {s.endTime ? ms(s.endTime - s.startTime) : "—"}
                </div>
              </div>
            ))}
          </div>
        </section>

        <section className="col">
          <h2>原文</h2>
          <div className="pages">
            <div className="page">
              <span className="lbl">输入</span>
              {current?.input || "—"}
            </div>
            <div className="page">
              <span className="lbl">输出</span>
              {current?.output || "—"}
            </div>
            <div className="meta">{current && metaLine(current)}</div>
          </div>
        </section>

        <section className="col">
          <div className="verdict">
            <h2>判词</h2>

            <div className="rubric">
              <div className="rk">
                <span>忠实度</span>
                <span>{dots.faithful ?? "—"}</span>
              </div>
              <div className="dots">
                {[0, 1, 2, 3].map((v) => (
                  <button
                    key={v}
                    className={dots.faithful === v ? "on" : ""}
                    onClick={() => setDots((d) => ({ ...d, faithful: v }))}
                    aria-label={`忠实度 ${v}`}
                  />
                ))}
              </div>
            </div>

            <div className="rubric">
              <div className="rk">
                <span>准确性</span>
                <span>{dots.accurate ?? "—"}</span>
              </div>
              <div className="dots">
                {[0, 1, 2, 3].map((v) => (
                  <button
                    key={v}
                    className={dots.accurate === v ? "on" : ""}
                    onClick={() => setDots((d) => ({ ...d, accurate: v }))}
                    aria-label={`准确性 ${v}`}
                  />
                ))}
              </div>
            </div>

            <div className="rk" style={{ fontSize: 12.5, margin: "14px 0 7px", display: "flex", justifyContent: "space-between" }}>
              <span>理由（必填）</span>
            </div>
            <textarea
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="写下你判定的依据…"
            />

            <div className="acts">
              <button className="btn primary" disabled={busy} onClick={() => void submit(1)}>
                通 过
              </button>
              <button className="btn reject" disabled={busy} onClick={() => void submit(0)}>
                不 通 过
              </button>
              <button className="btn" disabled={busy} onClick={() => void submit(-1)}>
                跳 过
              </button>
              <button className="btn judge" disabled={judging || busy} onClick={() => void runJudge()}>
                {judging ? "机判进行中…" : "模 型 初 判"}
              </button>
            </div>

            {judgeNote && <div className="judge-note">{judgeNote}</div>}

            <div className="kbd">J/K 切换节点 · Space 通过 · X 不通过 · S 跳过</div>

            {scores.length > 0 && (
              <div className="scores">
                {judgeRuns > 1 && (
                  <div className="scores-note">机判记录 {judgeRuns} 条 · 仅显示最新一次</div>
                )}
                {visibleScores.map((s) => (
                  <div className="score" key={s.id}>
                    <div className="score-head">
                      <span>{s.name}</span>
                      <span className="v">{s.value ?? s.stringValue ?? "—"}</span>
                    </div>
                    {s.comment && <div className="score-comment">{s.comment}</div>}
                    {s.rawResponse && (
                      <details className="raw" open>
                        <summary>原始响应</summary>
                        <pre>{s.rawResponse}</pre>
                      </details>
                    )}
                  </div>
                ))}
              </div>
            )}

            <div className={`stamped ${stamp ? "on" : ""}`}>{stamp || "已 判 定"}</div>

            <div className="collect">
              <div className="rk">
                <span>收入回归集</span>
                <span>{collectNote}</span>
              </div>
              {datasets.length === 0 ? (
                <div className="dim small">还没有回归集，去「回归集」页建一个。</div>
              ) : (
                <div className="collect-row">
                  <select value={targetDs} onChange={(e) => setTargetDs(e.target.value)}>
                    {datasets.map((d) => (
                      <option key={d.id} value={d.id}>
                        {d.name}（{d.items}）
                      </option>
                    ))}
                  </select>
                </div>
              )}
              {datasets.length > 0 && (
                <button className="btn" disabled={collecting} onClick={() => void collect()}>
                  {collecting ? "加入中…" : "加 入 当 前 卷 宗"}
                </button>
              )}
            </div>
          </div>
        </section>
      </div>
    </main>
  );
}
