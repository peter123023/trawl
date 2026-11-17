import { useEffect, useMemo, useState } from "react";
import { api } from "../api";
import type { QueueResponse } from "../types";
import { dayLabel, when } from "../utils";
import { navigate } from "../App";

type Rubric = { faithful: number | null; accurate: number | null };

// Batch model judging runs serially so progress stays readable and upstream stays unpaced.
export default function Queue() {
  const [data, setData] = useState<QueueResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [rubric, setRubric] = useState<Rubric>({ faithful: null, accurate: null });
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [judgeBusy, setJudgeBusy] = useState(false);
  const [notice, setNotice] = useState("");

  const reload = () => {
    setLoading(true);
    api
      .queue(100)
      .then((d) => setData(d))
      .catch((e) => setError(String((e as Error).message || e)))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    let alive = true;
    api
      .queue(100)
      .then((d) => alive && setData(d))
      .catch((e) => alive && setError(String((e as Error).message || e)))
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
    };
  }, []);

  const stats = data?.stats ?? { pending: 0, judgedToday: 0, judgedTotal: 0 };
  const items = data?.items ?? [];
  const selectedItems = useMemo(
    () => items.filter((t) => selected.has(t.traceId)),
    [items, selected],
  );

  const toggle = (traceId: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(traceId)) next.delete(traceId);
      else next.add(traceId);
      return next;
    });
  };

  const toggleAll = () => {
    setSelected((prev) => {
      if (prev.size === items.length) return new Set();
      return new Set(items.map((t) => t.traceId));
    });
  };

  const clear = () => {
    setSelected(new Set());
    setRubric({ faithful: null, accurate: null });
    setReason("");
  };

  const submit = async (value: number) => {
    if (busy) return;
    if (selectedItems.length === 0) return;
    if (!reason.trim()) {
      window.alert("批量判词需要一个共同理由：没有依据的判定不可追溯。");
      return;
    }
    setBusy(true);
    setNotice("");
    try {
      const scores = selectedItems.flatMap((t) => {
        const rows = [
          { traceId: t.traceId, name: "verdict", value, comment: reason },
        ];
        if (rubric.faithful !== null) {
          rows.push({ traceId: t.traceId, name: "faithful", value: rubric.faithful, comment: reason });
        }
        if (rubric.accurate !== null) {
          rows.push({ traceId: t.traceId, name: "accurate", value: rubric.accurate, comment: reason });
        }
        return rows;
      });
      const res = await api.batchScores(scores);
      setNotice(`已写入 ${res.count} 条判词，覆盖 ${selectedItems.length} 篇卷宗`);
      clear();
      reload();
    } catch (e) {
      window.alert(`批量判词失败：${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  const runJudge = async () => {
    if (judgeBusy || busy || selectedItems.length === 0) return;
    setJudgeBusy(true);
    setNotice("");
    const ids = selectedItems.map((t) => t.traceId);
    let ok = 0;
    let failed = 0;
    for (let i = 0; i < ids.length; i++) {
      try {
        await api.judge(ids[i]);
        ok++;
      } catch {
        failed++;
      }
      setNotice(
        `机判 ${i + 1} / ${ids.length} · 成功 ${ok}${failed ? ` · 失败 ${failed}` : ""}`,
      );
    }
    setJudgeBusy(false);
    setNotice(
      `模型初判已写入 ${ok} 条 llm_judge${failed ? `，${failed} 条失败` : ""}；人工终判仍需盖章`,
    );
  };

  return (
    <main className="desk">
      <div className="masthead">
        <h1>
          待判队列 <span className="queue-badge">FIFO</span>
        </h1>
        <div className="tail">TRAWL · queue · {dayLabel()}</div>
      </div>

      <div className="numbers">
        <div className="num">
          <div className="v">{stats.pending}</div>
          <div className="k">待判链路</div>
        </div>
        <div className="num">
          <div className="v">{stats.judgedToday}</div>
          <div className="k">今日已判</div>
        </div>
        <div className="num">
          <div className="v">{stats.judgedTotal}</div>
          <div className="k">累计判词</div>
        </div>
      </div>

      {error && <div className="error">队列读取失败：{error}</div>}
      {loading && <div className="loading">正在调取卷宗…</div>}

      {!loading && !error && (
        <div className="queue-grid">
          <div>
            {items.length === 0 ? (
              <div className="queue-empty">
                卷宗已批完。
                <br />
                新的链路在写入后会自动进入队列。
              </div>
            ) : (
              <table>
                <colgroup>
                  <col style={{ width: 36 }} />
                  <col style={{ width: 52 }} />
                  <col />
                  <col style={{ width: 56 }} />
                  <col style={{ width: 72 }} />
                  <col style={{ width: 176 }} />
                </colgroup>
                <thead>
                  <tr>
                    <th>
                      <input
                        type="checkbox"
                        checked={selected.size > 0 && selected.size === items.length}
                        onChange={toggleAll}
                        aria-label="全选"
                      />
                    </th>
                    <th>编号</th>
                    <th>链路</th>
                    <th>节点</th>
                    <th>令牌</th>
                    <th>时间</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((t, i) => (
                    <tr
                      key={t.traceId}
                      className={selected.has(t.traceId) ? "selected" : ""}
                      onClick={() => navigate(`/queue/${encodeURIComponent(t.traceId)}`)}
                    >
                      <td onClick={(e) => e.stopPropagation()}>
                        <input
                          type="checkbox"
                          checked={selected.has(t.traceId)}
                          onChange={() => toggle(t.traceId)}
                          aria-label={`选择 ${t.name || t.traceId}`}
                        />
                      </td>
                      <td className="no">{String(i + 1).padStart(2, "0")}</td>
                      <td className="name">
                        {t.name || t.traceId}
                        {t.hasError && <span className="warn"> ⚠ 异常</span>}
                      </td>
                      <td className="mono">{t.spanCount}</td>
                      <td className="mono">{t.tokens || 0}</td>
                      <td className="mono">{when(t.startTime)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          <aside>
            <div className="queue-note">
              队列收录<b>尚无 verdict 判词</b>的链路，按开始时间先来先判。
              勾选多篇可一次盖章；也可让模型先判一遍——机判写 llm_judge，不动队列，人工终判仍需盖章。
            </div>

            <div className="batch-panel">
              <div className="batch-total">
                已选 <b>{selectedItems.length}</b> 篇
                {selectedItems.length > 0 && (
                  <button className="link" onClick={clear}>
                    清空
                  </button>
                )}
              </div>

              {(["faithful", "accurate"] as const).map((key) => (
                <div className="rubric" key={key}>
                  <div className="rk">
                    <span>{key === "faithful" ? "忠实度" : "准确性"}</span>
                    <span>{rubric[key] ?? "—"}</span>
                  </div>
                  <div className="dots">
                    {[0, 1, 2, 3].map((v) => (
                      <button
                        key={v}
                        className={rubric[key] === v ? "on" : ""}
                        onClick={() => setRubric((r) => ({ ...r, [key]: v }))}
                        aria-label={`${key} ${v}`}
                      />
                    ))}
                  </div>
                </div>
              ))}

              <textarea
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                placeholder="这一批的共同理由…"
              />

              <div className="acts">
                <button
                  className="btn primary"
                  disabled={busy || selectedItems.length === 0}
                  onClick={() => void submit(1)}
                >
                  整 批 通 过
                </button>
                <button
                  className="btn reject"
                  disabled={busy || selectedItems.length === 0}
                  onClick={() => void submit(0)}
                >
                  整 批 不 通 过
                </button>
                <button
                  className="btn"
                  disabled={busy || judgeBusy || selectedItems.length === 0}
                  onClick={() => void submit(-1)}
                >
                  整 批 跳 过
                </button>
                <button
                  className="btn judge"
                  disabled={busy || judgeBusy || selectedItems.length === 0}
                  onClick={() => void runJudge()}
                >
                  {judgeBusy ? "机判进行中…" : "整 批 机 判"}
                </button>
              </div>

              {notice && <div className="notice">{notice}</div>}
            </div>
          </aside>
        </div>
      )}

      <div className="foot">trawl · evaluation-first · 判词写入 scores 表</div>
    </main>
  );
}
