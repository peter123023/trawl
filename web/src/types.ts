export interface TraceSummary {
  traceId: string;
  name: string;
  startTime: number;
  endTime: number;
  spanCount: number;
  cost: number;
  tokens: number;
  hasError: boolean;
  environment: string;
}

export interface Span {
  projectId: string;
  spanId: string;
  traceId: string;
  parentSpanId: string;
  type: string;
  name: string;
  startTime: number;
  endTime?: number;
  completionStartTime?: number;
  sessionId?: string;
  userId?: string;
  environment: string;
  release?: string;
  version?: string;
  tags: string;
  level: string;
  statusMessage?: string;
  model?: string;
  modelParameters?: string;
  promptName?: string;
  promptVersion?: number;
  usageDetails?: string;
  costDetails?: string;
  input?: string;
  output?: string;
  metadata?: string;
  eventTs: number;
  createdAt: number;
  updatedAt: number;
}

export interface Score {
  id: string;
  traceId: string;
  spanId?: string;
  name: string;
  dataType: string;
  value?: number;
  stringValue?: string;
  comment?: string;
  source: string;
  authorName?: string;
  rawResponse?: string;
  createdAt: number;
}

export interface QueueStats {
  pending: number;
  judgedToday: number;
  judgedTotal: number;
}

export interface QueueResponse {
  items: TraceSummary[];
  stats: QueueStats;
}

export interface PromptStat {
  name: string;
  version: number;
  calls: number;
  tokens: number;
  cost: number;
  judged: number;
  passed: number;
}

export interface Dataset {
  id: string;
  projectId: string;
  name: string;
  note: string;
  items: number;
  createdAt: number;
}

export interface DatasetItem {
  id: string;
  traceId: string;
  note: string;
  createdAt: number;
  traceName: string;
  startTime: number;
  promptName?: string;
  promptVersion?: number;
  verdict?: number;
}

export interface VersionStat {
  promptName: string;
  promptVersion?: number;
  items: number;
  judged: number;
  passed: number;
  passRate: number;
  avgVerdict?: number;
}

export interface PriceRow {
  model: string;
  inputPer1k: number;
  outputPer1k: number;
  cacheReadPer1k?: number;
  updatedAt: number;
}

export interface ProjectRow {
  id: string;
  name: string;
  createdAt: number;
  traces: number;
  keys: number;
}
