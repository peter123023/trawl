import { useEffect, useState } from "react";
import { api, currentProject, setCurrentProject } from "../api";
import type { ProjectRow, TraceSummary } from "../types";
import { dayLabel, money, ms, when } from "../utils";
import { navigate } from "../App";

// Search runs server-side with a 280ms debounce, enough for tens of thousands of traces.
export default function Desk() {
  const [traces, setTraces] = useState<TraceSummary[]>([]);
  const [query, setQuery] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [projects, setProjects] = useState<ProjectRow[]>([]);
  const [project, setProject] = useState(currentProject());

  useEffect(() => {
    api
      .projects()
      .then((d) => {
        setProjects(d.projects ?? []);
        if (!currentProject() && d.current) {
          setCurrentProject(d.current);
          setProject(d.current);
        }
      })
      .catch(() => {
      });
  }, []);

  useEffect(() => {
    const saved = currentProject();
    if (project && project !== saved) setCurrentProject(project);
  }, [project]);

  useEffect(() => {
    let alive = true;
    const timer = setTimeout(() => {
      api
        .traces(60, query.trim() || undefined)
        .then((d) => {
          if (!alive) return;
          setTraces(d.traces ?? []);
          setError("");
        })
        .catch((e) => alive && setError(String((e as Error).message || e)))
        .finally(() => alive && setLoading(false));
    }, 280);
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [query, project]);

  const errors = traces.filter((t) => t.hasError).length;
  const cost = traces.reduce((sum, t) => sum + (t.cost || 0), 0);

  return (
    <main className="desk">
      <div className="masthead">
        <h1>案件台账</h1>
        <div className="tail">TRAWL · local-first · {dayLabel()}</div>
      </div>

      <div className="numbers">
        <div className="num">
          <div className="v">{traces.length}</div>
          <div className="k">近 7 日链路</div>
        </div>
        <div className="num">
          <div className="v">{errors}</div>
          <div className="k">含异常</div>
        </div>
        <div className="num">
          <div className="v">{money(cost)}</div>
          <div className="k">累计成本</div>
        </div>
      </div>

      <div className="bar">
        {projects.length > 1 && (
          <select
            className="proj-select"
            value={project}
            onChange={(e) => setProject(e.target.value)}
            title="切换项目"
          >
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        )}
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="按名称或输入/输出内容搜索…"
        />
        <span className="hint">点击任意一行进入阅卷</span>
      </div>

      {error && <div className="error">台账读取失败：{error}</div>}
      {loading && <div className="loading">正在展开卷宗…</div>}

      {!loading && !error && (
        <table>
          <thead>
            <tr>
              <th>编号</th>
              <th>链路</th>
              <th>节点</th>
              <th>耗时</th>
              <th>令牌</th>
              <th>成本</th>
              <th>时间</th>
            </tr>
          </thead>
          <tbody>
            {traces.map((t, i) => (
              <tr key={t.traceId} onClick={() => navigate(`/t/${encodeURIComponent(t.traceId)}`)}>
                <td className="no">{String(i + 1).padStart(2, "0")}</td>
                <td className="name">
                  {t.name || t.traceId}
                  {t.hasError && <span className="warn"> ⚠ 异常</span>}
                </td>
                <td className="mono">{t.spanCount}</td>
                <td className="mono">{ms((t.endTime || t.startTime) - t.startTime)}</td>
                <td className="mono">{t.tokens || 0}</td>
                <td className="mono">{money(t.cost)}</td>
                <td className="mono">{when(t.startTime)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {!loading && !error && traces.length === 0 && (
        <div className="empty">
          {query.trim() ? (
            <>
              没有匹配「{query.trim()}」的链路。
              <br />
              搜索覆盖链路名称、输入与输出正文。
            </>
          ) : (
            <>
              此处应有一条链路。
              <br />
              试着上报一次：
              <br />
              <code>
                curl -X POST localhost:8080/api/public/ingestion -H "Authorization: Bearer
                $TRAWL_SECRET" -d @testdata/sample.json
              </code>
            </>
          )}
        </div>
      )}

      <div className="foot">trawl · local-first · 单文件 SQLite</div>
    </main>
  );
}
