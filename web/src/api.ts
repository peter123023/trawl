import type {
  Dataset,
  DatasetItem,
  PriceRow,
  ProjectRow,
  PromptStat,
  QueueResponse,
  Score,
  Span,
  TraceSummary,
  VersionStat,
} from "./types";

export function currentProject(): string {
  try {
    return localStorage.getItem("trawl.project") || "";
  } catch {
    return "";
  }
}

export function setCurrentProject(id: string) {
  try {
    if (id) localStorage.setItem("trawl.project", id);
    else localStorage.removeItem("trawl.project");
  } catch {
  }
}

async function errorText(res: Response): Promise<string> {
  try {
    const body = await res.json();
    return body?.error?.message || String(res.status);
  } catch {
    return String(res.status);
  }
}

function projectHeaders(): Record<string, string> {
  const id = currentProject();
  return id ? { "X-Trawl-Project": id } : {};
}

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url, { headers: { Accept: "application/json", ...projectHeaders() } });
  if (!res.ok) {
    let detail = res.statusText;
    try {
      const body = await res.json();
      detail = body?.error?.message || JSON.stringify(body);
    } catch {
      }
    throw new Error(`${res.status} ${detail}`);
  }
  return (await res.json()) as T;
}

export interface ScoreInput {
  traceId: string;
  spanId?: string;
  name: string;
  value?: number;
  stringValue?: string;
  comment?: string;
  authorName?: string;
  dataType?: string;
}

export const api = {
  traces(limit = 60, q?: string) {
    const params = new URLSearchParams({ limit: String(limit) });
    if (q) params.set("q", q);
    return getJSON<{ traces: TraceSummary[]; nextCursor: string }>(`/api/traces?${params}`);
  },
  trace(traceId: string) {
    return getJSON<{ spans: Span[]; scores: Score[] }>(
      `/api/traces/${encodeURIComponent(traceId)}`,
    );
  },
  queue(limit = 100) {
    return getJSON<QueueResponse>(`/api/queue?limit=${limit}`);
  },
  prompts() {
    return getJSON<{ prompts: PromptStat[] }>(`/api/prompts`);
  },
  async judge(traceId: string, spanId?: string): Promise<{ score: number; reason: string; model: string }> {
    const res = await fetch("/api/judge", {
      method: "POST",
      headers: { "Content-Type": "application/json", ...projectHeaders() },
      body: JSON.stringify({ traceId, spanId }),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body?.error?.message || String(res.status));
    return body as { score: number; reason: string; model: string };
  },
  async addScore(input: ScoreInput): Promise<{ id: string }> {
    const res = await fetch("/api/scores", {
      method: "POST",
      headers: { "Content-Type": "application/json", ...projectHeaders() },
      body: JSON.stringify({ dataType: "NUMERIC", authorName: "local", ...input }),
    });
    if (!res.ok) throw new Error(`score failed: ${res.status}`);
    return (await res.json()) as { id: string };
  },
  datasets() {
    return getJSON<{ datasets: Dataset[] }>(`/api/datasets`);
  },
  dataset(id: string) {
    return getJSON<{ dataset: Dataset | null; items: DatasetItem[]; versions: VersionStat[] }>(
      `/api/datasets/${encodeURIComponent(id)}`,
    );
  },
  async createDataset(name: string, note: string): Promise<Dataset> {
    const res = await fetch("/api/datasets", {
      method: "POST",
      headers: { "Content-Type": "application/json", ...projectHeaders() },
      body: JSON.stringify({ name, note }),
    });
    if (!res.ok) throw new Error(await errorText(res));
    return (await res.json()) as Dataset;
  },
  async addDatasetItems(id: string, traceIds: string[]): Promise<{ added: number; requested: number }> {
    const res = await fetch(`/api/datasets/${encodeURIComponent(id)}/items`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...projectHeaders() },
      body: JSON.stringify({ traceIds }),
    });
    if (!res.ok) throw new Error(await errorText(res));
    return (await res.json()) as { added: number; requested: number };
  },
  async deleteDataset(id: string): Promise<void> {
    const res = await fetch(`/api/datasets/${encodeURIComponent(id)}`, {
      method: "DELETE",
      headers: projectHeaders(),
    });
    if (!res.ok) throw new Error(await errorText(res));
  },
  async removeDatasetItem(id: string, traceId: string): Promise<void> {
    const res = await fetch(
      `/api/datasets/${encodeURIComponent(id)}/items/${encodeURIComponent(traceId)}`,
      { method: "DELETE", headers: projectHeaders() },
    );
    if (!res.ok) throw new Error(await errorText(res));
  },
  prices() {
    return getJSON<{ prices: PriceRow[] }>(`/api/prices`);
  },
  async putPrice(model: string, inputPer1k: number, outputPer1k: number): Promise<void> {
    const res = await fetch(`/api/prices/${encodeURIComponent(model)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", ...projectHeaders() },
      body: JSON.stringify({ inputPer1k, outputPer1k }),
    });
    if (!res.ok) throw new Error(await errorText(res));
  },
  async deletePrice(model: string): Promise<void> {
    const res = await fetch(`/api/prices/${encodeURIComponent(model)}`, {
      method: "DELETE",
      headers: projectHeaders(),
    });
    if (!res.ok) throw new Error(await errorText(res));
  },
  async syncPrices(url?: string): Promise<{ updated: number; source: string }> {
    const res = await fetch("/api/prices/sync", {
      method: "POST",
      headers: { "Content-Type": "application/json", ...projectHeaders() },
      body: JSON.stringify(url ? { url } : {}),
    });
    if (!res.ok) throw new Error(await errorText(res));
    return (await res.json()) as { updated: number; source: string };
  },
  async exportScores(): Promise<Blob> {
    const res = await fetch("/api/export/scores.csv", { headers: projectHeaders() });
    if (!res.ok) throw new Error(await errorText(res));
    return res.blob();
  },
  async backup(): Promise<{ name: string; size: number; download: string }> {
    const res = await fetch("/api/backup", { method: "POST", headers: projectHeaders() });
    if (!res.ok) throw new Error(await errorText(res));
    return (await res.json()) as { name: string; size: number; download: string };
  },
  projects() {
    return getJSON<{ projects: ProjectRow[]; current: string }>(`/api/projects`);
  },
  async createProject(name: string): Promise<{ projectId: string; publicKey: string; secretKey: string }> {
    const res = await fetch("/api/projects", {
      method: "POST",
      headers: { "Content-Type": "application/json", ...projectHeaders() },
      body: JSON.stringify({ name }),
    });
    if (!res.ok) throw new Error(await errorText(res));
    return (await res.json()) as { projectId: string; publicKey: string; secretKey: string };
  },
  async batchScores(scores: ScoreInput[]): Promise<{ count: number; ids: string[] }> {
    const res = await fetch("/api/scores/batch", {
      method: "POST",
      headers: { "Content-Type": "application/json", ...projectHeaders() },
      body: JSON.stringify({
        scores: scores.map((s) => ({ dataType: "NUMERIC", authorName: "local", ...s })),
      }),
    });
    if (!res.ok) {
      let detail = String(res.status);
      try {
        const body = await res.json();
        detail = body?.error?.message || detail;
      } catch {
          }
      throw new Error(detail);
    }
    return (await res.json()) as { count: number; ids: string[] };
  },
};
