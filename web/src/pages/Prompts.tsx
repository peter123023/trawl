import { useEffect, useState } from "react";
import { api } from "../api";
import type { PromptStat } from "../types";
import { dayLabel, money } from "../utils";

export default function Prompts() {
  const [rows, setRows] = useState<PromptStat[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let alive = true;
    api
      .prompts()
      .then((d) => alive && setRows(d.prompts ?? []))
      .catch((e) => alive && setError(String((e as Error).message || e)))
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
    };
  }, []);

  const calls = rows.reduce((sum, p) => sum + p.calls, 0);
  const judged = rows.reduce((sum, p) => sum + p.judged, 0);
  const passed = rows.reduce((sum, p) => sum + p.passed, 0);
  const rate = judged ? `${Math.round((passed / judged) * 100)}%` : "—";

  return (
    <main className="desk">
      <div className="masthead">
        <h1>Prompt 版本</h1>
        <div className="tail">TRAWL · prompts · {dayLabel()}</div>
      </div>

      <div className="numbers">
        <div className="num">
          <div className="v">{rows.length}</div>
          <div className="k">版本数</div>
        </div>
        <div className="num">
          <div className="v">{calls}</div>
          <div className="k">调用次数</div>
        </div>
        <div className="num">
          <div className="v">{rate}</div>
          <div className="k">整体通过率</div>
        </div>
      </div>

      {error && <div className="error">读取失败：{error}</div>}
      {loading && <div className="loading">正在汇总版本…</div>}

      {!loading && !error && (
        <table>
          <thead>
            <tr>
              <th>Prompt</th>
              <th>版本</th>
              <th>调用</th>
              <th>令牌</th>
              <th>成本</th>
              <th>已判</th>
              <th>通过率</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((p) => (
              <tr key={`${p.name}-v${p.version}`} style={{ cursor: "default" }}>
                <td className="name">{p.name}</td>
                <td className="mono">v{p.version}</td>
                <td className="mono">{p.calls}</td>
                <td className="mono">{p.tokens}</td>
                <td className="mono">{money(p.cost)}</td>
                <td className="mono">{p.judged}</td>
                <td className="mono">
                  {p.judged ? `${Math.round((p.passed / p.judged) * 100)}%` : "—"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {!loading && !error && rows.length === 0 && (
        <div className="empty">
          还没有带 prompt 版本的 generation。
          <br />
          SDK 上报时带上 <code>promptName</code> 与 <code>promptVersion</code> 即可入账。
        </div>
      )}

      <div className="foot">trawl · prompt lineage · 按版本聚合 verdict</div>
    </main>
  );
}
