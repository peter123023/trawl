import { useEffect, useState } from "react";
import { api } from "../api";
import type { PriceRow } from "../types";
import { dayLabel } from "../utils";

export default function Settings() {
  const [prices, setPrices] = useState<PriceRow[]>([]);
  const [projects, setProjects] = useState<{ id: string; name: string; traces: number; keys: number }[]>([]);
  const [current, setCurrent] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState("");
  const [model, setModel] = useState("");
  const [input, setInput] = useState("");
  const [output, setOutput] = useState("");
  const [projName, setProjName] = useState("");
  const [issued, setIssued] = useState<{ publicKey: string; secretKey: string } | null>(null);

  const load = () => {
    Promise.all([api.prices(), api.projects()])
      .then(([p, pr]) => {
        setPrices(p.prices ?? []);
        setProjects(pr.projects ?? []);
        setCurrent(pr.current ?? "");
      })
      .catch((e) => setError((e as Error).message));
  };

  useEffect(load, []);

  const savePrice = async () => {
    if (!model.trim() || busy) return;
    const i = Number(input);
    const o = Number(output);
    if (!Number.isFinite(i) || !Number.isFinite(o) || i < 0 || o < 0) {
      setError("价格必须是 ≥ 0 的数字（每 1k token 美元）");
      return;
    }
    setBusy("price");
    try {
      await api.putPrice(model.trim(), i, o);
      setModel("");
      setInput("");
      setOutput("");
      setNotice("价格已更新，新写入的链路立刻按新价换算");
      load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy("");
    }
  };

  const sync = async () => {
    if (busy) return;
    setBusy("sync");
    setError("");
    try {
      const res = await api.syncPrices();
      setNotice(`已从 ${res.source} 同步 ${res.updated} 个模型价格`);
      load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy("");
    }
  };

  const backup = async () => {
    if (busy) return;
    setBusy("backup");
    try {
      const res = await api.backup();
      setNotice(`备份已生成：${res.name}（${(res.size / 1024).toFixed(0)} KB）`);
      window.location.href = res.download;
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy("");
    }
  };

  const exportCSV = async () => {
    if (busy) return;
    setBusy("export");
    try {
      const blob = await api.exportScores();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `trawl-scores-${new Date().toISOString().slice(0, 10)}.csv`;
      a.click();
      URL.revokeObjectURL(url);
      setNotice("判词 CSV 已导出");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy("");
    }
  };

  const createProject = async () => {
    if (!projName.trim() || busy) return;
    setBusy("project");
    try {
      const res = await api.createProject(projName.trim());
      setIssued({ publicKey: res.publicKey, secretKey: res.secretKey });
      setProjName("");
      load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy("");
    }
  };

  return (
    <main className="desk">
      <div className="masthead">
        <h1>设置</h1>
        <div className="tail">TRAWL · admin · {dayLabel()}</div>
      </div>

      {error && <div className="error">{error}</div>}
      {notice && <div className="notice">{notice}</div>}

      <section className="side-block wide">
        <div className="rk">
          <span>项目</span>
          <span>密钥只显示一次</span>
        </div>
        {projects.map((p) => (
          <div className="vrow" key={p.id}>
            <span>
              {p.name} <span className="dim mono">{p.id}</span>
              {p.id === current && <span className="tag">当前</span>}
            </span>
            <span className="mono dim">
              {p.traces} 链路 · {p.keys} 密钥
            </span>
          </div>
        ))}
        <div className="row-inline">
          <input value={projName} onChange={(e) => setProjName(e.target.value)} placeholder="新项目名称…" />
          <button className="btn" disabled={busy === "project" || !projName.trim()} onClick={() => void createProject()}>
            新 建 项 目
          </button>
        </div>
        {issued && (
          <div className="issue">
            <div>public {issued.publicKey}</div>
            <div>secret {issued.secretKey}</div>
            <div className="dim">只显示这一次；SDK 侧改 baseUrl 与这两把钥匙即可上报。</div>
          </div>
        )}
      </section>

      <section className="side-block wide">
        <div className="rk">
          <span>模型价格（每 1k token，USD）</span>
          <span>{prices.length} 个模型</span>
        </div>
        <div className="row-inline">
          <input value={model} onChange={(e) => setModel(e.target.value)} placeholder="model 名称…" />
          <input value={input} onChange={(e) => setInput(e.target.value)} placeholder="input /1k" />
          <input value={output} onChange={(e) => setOutput(e.target.value)} placeholder="output /1k" />
          <button className="btn" disabled={busy === "price" || !model.trim()} onClick={() => void savePrice()}>
            写 入
          </button>
        </div>
        {prices.length > 0 && (
          <table>
            <colgroup>
              <col />
              <col style={{ width: 110 }} />
              <col style={{ width: 110 }} />
              <col style={{ width: 44 }} />
            </colgroup>
            <thead>
              <tr>
                <th>模型</th>
                <th>input</th>
                <th>output</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {prices.map((p) => (
                <tr key={p.model}>
                  <td className="mono">{p.model}</td>
                  <td className="mono">{p.inputPer1k}</td>
                  <td className="mono">{p.outputPer1k}</td>
                  <td>
                    <button className="link" onClick={() => void api.deletePrice(p.model).then(load)}>
                      删
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <div className="row-inline">
          <button className="btn" disabled={busy === "sync"} onClick={() => void sync()}>
            {busy === "sync" ? "同步中…" : "从价格源同步"}
          </button>
          <span className="dim mono">TRAWL_PRICES_URL · LiteLLM model_prices.json 或简化列表</span>
        </div>
      </section>

      <section className="side-block wide">
        <div className="rk">
          <span>备份与导出</span>
          <span>VACUUM INTO，不锁写</span>
        </div>
        <div className="row-inline">
          <button className="btn" disabled={busy === "backup"} onClick={() => void backup()}>
            {busy === "backup" ? "生成中…" : "生 成 备 份"}
          </button>
          <button className="btn" disabled={busy === "export"} onClick={() => void exportCSV()}>
            {busy === "export" ? "导出中…" : "导 出 判 词 CSV"}
          </button>
        </div>
        <div className="dim mono small">导出按当前项目过滤；备份是整库快照。</div>
      </section>

      <div className="foot">trawl · local-first · 单文件 SQLite</div>
    </main>
  );
}
