import { useEffect, useState } from "react";
import { api } from "../api";
import type { Dataset, DatasetItem, VersionStat } from "../types";
import { navigate } from "../App";
import { dayLabel, when } from "../utils";

export default function Datasets() {
  const [list, setList] = useState<Dataset[]>([]);
  const [activeId, setActiveId] = useState("");
  const [detail, setDetail] = useState<{ items: DatasetItem[]; versions: VersionStat[] } | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [name, setName] = useState("");
  const [note, setNote] = useState("");
  const [creating, setCreating] = useState(false);

  const loadList = () => {
    api
      .datasets()
      .then((d) => {
        setList(d.datasets ?? []);
        if (!activeId && d.datasets?.length) setActiveId(d.datasets[0].id);
      })
      .catch((e) => setError((e as Error).message));
  };

  useEffect(loadList, []);

  useEffect(() => {
    if (!activeId) {
      setDetail(null);
      return;
    }
    let alive = true;
    api
      .dataset(activeId)
      .then((d) => alive && setDetail({ items: d.items ?? [], versions: d.versions ?? [] }))
      .catch((e) => alive && setError((e as Error).message));
    return () => {
      alive = false;
    };
  }, [activeId]);

  const create = async () => {
    if (!name.trim() || creating) return;
    setCreating(true);
    try {
      const ds = await api.createDataset(name.trim(), note.trim());
      setName("");
      setNote("");
      setNotice(`已建立数据集「${ds.name}」`);
      loadList();
      setActiveId(ds.id);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setCreating(false);
    }
  };

  const remove = async (traceId: string) => {
    if (!activeId) return;
    try {
      await api.removeDatasetItem(activeId, traceId);
      const d = await api.dataset(activeId);
      setDetail({ items: d.items ?? [], versions: d.versions ?? [] });
      loadList();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const destroy = async () => {
    if (!activeId || !active) return;
    try {
      await api.deleteDataset(activeId);
      setNotice(`已删除「${active.name}」`);
      setActiveId("");
      setDetail(null);
      loadList();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const active = list.find((d) => d.id === activeId);

  return (
    <main className="desk">
      <div className="masthead">
        <h1>回归集</h1>
        <div className="tail">TRAWL · datasets · {dayLabel()}</div>
      </div>

      <div className="numbers">
        <div className="num">
          <div className="v">{list.length}</div>
          <div className="k">数据集</div>
        </div>
        <div className="num">
          <div className="v">{active?.items ?? 0}</div>
          <div className="k">当前条目</div>
        </div>
        <div className="num">
          <div className="v">{detail?.versions.length ?? 0}</div>
          <div className="k">可比版本</div>
        </div>
      </div>

      {error && <div className="error">读取失败：{error}</div>}

      <div className="ds-grid">
        <div>
          {list.length === 0 ? (
            <div className="queue-empty">
              还没有回归集。
              <br />
              在台账里挑几条链路，或在这里先建一个空集再往里加。
            </div>
          ) : (
            <table>
              <colgroup>
                <col style={{ width: 200 }} />
                <col />
                <col style={{ width: 80 }} />
              </colgroup>
              <thead>
                <tr>
                  <th>数据集</th>
                  <th>备注</th>
                  <th>条目</th>
                </tr>
              </thead>
              <tbody>
                {list.map((d) => (
                  <tr
                    key={d.id}
                    className={d.id === activeId ? "selected" : ""}
                    onClick={() => setActiveId(d.id)}
                  >
                    <td className="name">{d.name}</td>
                    <td className="dim">{d.note || "—"}</td>
                    <td className="mono">{d.items}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        <aside>
          <div className="queue-note">
            回归集把「同一批问题」固定下来，按 prompt 版本比通过率。
            条目来自台账，判词仍写在 scores 表里。
          </div>

          <div className="side-block">
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="新数据集名称…" />
            <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="备注（可选）" />
            <button className="btn" disabled={creating || !name.trim()} onClick={() => void create()}>
              {creating ? "建立中…" : "建 立 数 据 集"}
            </button>
          </div>

          {notice && <div className="notice">{notice}</div>}

          {active && (
            <div className="row-inline ds-danger">
              <span className="dim mono small">当前：{active.name}</span>
              <button className="link" onClick={() => void destroy()}>
                删除该集
              </button>
            </div>
          )}

          {detail && detail.versions.length > 0 && (
            <div className="versions">
              <div className="rk">
                <span>按版本</span>
                <span>通过率</span>
              </div>
              {detail.versions.map((v) => (
                <div className="vrow" key={`${v.promptName}-${v.promptVersion ?? "na"}`}>
                  <span>
                    {v.promptName}
                    {v.promptVersion != null && ` v${v.promptVersion}`}
                  </span>
                  <span className="mono">
                    {v.passed}/{v.items} · {v.passRate.toFixed(0)}%
                  </span>
                </div>
              ))}
            </div>
          )}
        </aside>
      </div>

      {detail && (
        <section className="items">
          <div className="rk">
            <span>条目 {detail.items.length}</span>
            <span>判词口径：verdict ≥ 1 视为通过</span>
          </div>
          {detail.items.length === 0 ? (
            <div className="queue-empty">
              这个集还是空的。
              <br />
              打开任意一条链路，在右侧「收入回归集」里加入。
            </div>
          ) : (
            <table>
              <colgroup>
                <col style={{ width: 48 }} />
                <col />
                <col style={{ width: 140 }} />
                <col style={{ width: 80 }} />
                <col style={{ width: 176 }} />
                <col style={{ width: 44 }} />
              </colgroup>
              <thead>
                <tr>
                  <th>编号</th>
                  <th>链路</th>
                  <th>Prompt</th>
                  <th>判词</th>
                  <th>时间</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {detail.items.map((it, i) => (
                  <tr key={it.id} onClick={() => navigate(`/t/${encodeURIComponent(it.traceId)}`)}>
                    <td className="no">{String(i + 1).padStart(2, "0")}</td>
                    <td className="name">{it.traceName || it.traceId}</td>
                    <td className="mono dim">
                      {it.promptName ? `${it.promptName}${it.promptVersion != null ? ` v${it.promptVersion}` : ""}` : "—"}
                    </td>
                    <td className="mono">{it.verdict == null ? "未判" : it.verdict}</td>
                    <td className="mono">{when(it.startTime)}</td>
                    <td onClick={(e) => e.stopPropagation()}>
                      <button className="link" onClick={() => void remove(it.traceId)}>
                        移出
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
      )}

      <div className="foot">trawl · evaluation-first · 同一批问题，不同版本</div>
    </main>
  );
}
