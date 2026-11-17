PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS spans (
  project_id              TEXT    NOT NULL,
  span_id                 TEXT    NOT NULL,
  trace_id                TEXT    NOT NULL,
  parent_span_id          TEXT    NOT NULL DEFAULT '',
  type                    TEXT    NOT NULL DEFAULT 'span',
  name                    TEXT    NOT NULL DEFAULT '',
  start_time              INTEGER NOT NULL,
  end_time                INTEGER,
  completion_start_time   INTEGER,
  session_id              TEXT    NOT NULL DEFAULT '',
  user_id                 TEXT    NOT NULL DEFAULT '',
  environment             TEXT    NOT NULL DEFAULT 'default',
  release                 TEXT    NOT NULL DEFAULT '',
  version                 TEXT    NOT NULL DEFAULT '',
  tags                    TEXT    NOT NULL DEFAULT '[]',
  level                   TEXT    NOT NULL DEFAULT 'DEFAULT',
  status_message          TEXT    NOT NULL DEFAULT '',
  model                   TEXT    NOT NULL DEFAULT '',
  model_parameters        TEXT    NOT NULL DEFAULT '',
  usage_details           TEXT    NOT NULL DEFAULT '',
  cost_details            TEXT    NOT NULL DEFAULT '',
  input                   TEXT    NOT NULL DEFAULT '',
  output                  TEXT    NOT NULL DEFAULT '',
  metadata                TEXT    NOT NULL DEFAULT '',
  event_ts                INTEGER NOT NULL,
  is_deleted              INTEGER NOT NULL DEFAULT 0,
  created_at              INTEGER NOT NULL,
  updated_at              INTEGER NOT NULL,
  PRIMARY KEY (project_id, span_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_spans_time  ON spans(project_id, start_time DESC);
CREATE INDEX IF NOT EXISTS idx_spans_trace ON spans(project_id, trace_id);
CREATE INDEX IF NOT EXISTS idx_spans_sess  ON spans(project_id, session_id) WHERE session_id <> '';
CREATE INDEX IF NOT EXISTS idx_spans_user  ON spans(project_id, user_id)   WHERE user_id   <> '';

CREATE TABLE IF NOT EXISTS scores (
  id            TEXT PRIMARY KEY,
  project_id    TEXT NOT NULL,
  trace_id      TEXT NOT NULL DEFAULT '',
  span_id       TEXT NOT NULL DEFAULT '',
  name          TEXT NOT NULL,
  data_type     TEXT NOT NULL DEFAULT 'NUMERIC',
  value         REAL,
  string_value  TEXT NOT NULL DEFAULT '',
  comment       TEXT NOT NULL DEFAULT '',
  source        TEXT NOT NULL DEFAULT 'API',
  author_name   TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX IF NOT EXISTS idx_scores_trace ON scores(project_id, trace_id, name);

CREATE TABLE IF NOT EXISTS projects (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  created_at  INTEGER NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS api_keys (
  id           TEXT PRIMARY KEY,
  project_id   TEXT NOT NULL,
  public_key   TEXT NOT NULL UNIQUE,
  secret_hash  TEXT NOT NULL,
  name         TEXT NOT NULL DEFAULT 'default',
  last_used_at INTEGER,
  created_at   INTEGER NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS model_prices (
  model             TEXT PRIMARY KEY,
  input_per_1k      REAL NOT NULL,
  output_per_1k     REAL NOT NULL,
  cache_read_per_1k REAL,
  updated_at        INTEGER NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS ingest_batches (
  project_id  TEXT NOT NULL,
  batch_id    TEXT NOT NULL,
  received_at INTEGER NOT NULL,
  PRIMARY KEY (project_id, batch_id)
) STRICT;
