-- 0006: span 身份从 (project_id, span_id) 改为 (project_id, trace_id, span_id)。
--
-- 原因：OTLP 规范只要求 span id 在它自己的 trace 内唯一，跨 trace 可以重名；
-- 旧主键会把「另一个 trace 的同名 span」静默并进已有行——后到的 trace_id 不在
-- 合并列里，于是证据永远挂在旧 trace 上，属于看不见的数据错位。
--
-- external content 的 FTS 以 rowid 对齐，重建表会让 rowid 变号，所以这里把
-- 触发器与索引一并重建，最后按新 rowid 回填。

DROP TRIGGER IF EXISTS spans_fts_ai;
DROP TRIGGER IF EXISTS spans_fts_ad;
DROP TRIGGER IF EXISTS spans_fts_au;
DROP TABLE IF EXISTS span_fts;

-- 索引跟着表改名走，必须先删掉，否则新建表时 IF NOT EXISTS 会跳过，
-- 等 spans_old 被 DROP 时新表就成了无索引裸表。
DROP INDEX IF EXISTS idx_spans_time;
DROP INDEX IF EXISTS idx_spans_trace;
DROP INDEX IF EXISTS idx_spans_sess;
DROP INDEX IF EXISTS idx_spans_user;
DROP INDEX IF EXISTS idx_spans_prompt;

ALTER TABLE spans RENAME TO spans_old;

CREATE TABLE spans (
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
  prompt_name             TEXT    NOT NULL DEFAULT '',
  prompt_version          INTEGER,
  event_ts                INTEGER NOT NULL,
  is_deleted              INTEGER NOT NULL DEFAULT 0,
  created_at              INTEGER NOT NULL,
  updated_at              INTEGER NOT NULL,
  PRIMARY KEY (project_id, trace_id, span_id)
) STRICT;

INSERT INTO spans (
  project_id, span_id, trace_id, parent_span_id, type, name,
  start_time, end_time, completion_start_time, session_id, user_id,
  environment, release, version, tags, level, status_message,
  model, model_parameters, usage_details, cost_details, input, output, metadata,
  prompt_name, prompt_version, event_ts, is_deleted, created_at, updated_at
)
SELECT
  project_id, span_id, trace_id, parent_span_id, type, name,
  start_time, end_time, completion_start_time, session_id, user_id,
  environment, release, version, tags, level, status_message,
  model, model_parameters, usage_details, cost_details, input, output, metadata,
  COALESCE(prompt_name, ''), prompt_version, event_ts, is_deleted, created_at, updated_at
FROM spans_old
ORDER BY rowid;

DROP TABLE spans_old;

CREATE INDEX IF NOT EXISTS idx_spans_time  ON spans(project_id, start_time DESC);
CREATE INDEX IF NOT EXISTS idx_spans_trace ON spans(project_id, trace_id);
CREATE INDEX IF NOT EXISTS idx_spans_sess  ON spans(project_id, session_id) WHERE session_id <> '';
CREATE INDEX IF NOT EXISTS idx_spans_user  ON spans(project_id, user_id)   WHERE user_id   <> '';
CREATE INDEX IF NOT EXISTS idx_spans_prompt ON spans(project_id, prompt_name, prompt_version)
  WHERE prompt_name <> '';

CREATE VIRTUAL TABLE span_fts USING fts5(
  name, input, output,
  content='spans',
  content_rowid='rowid',
  tokenize='trigram'
);

INSERT INTO span_fts(rowid, name, input, output)
SELECT rowid, name, input, output FROM spans;

CREATE TRIGGER spans_fts_ai AFTER INSERT ON spans BEGIN
  INSERT INTO span_fts(rowid, name, input, output)
  VALUES (new.rowid, new.name, new.input, new.output);
END;

CREATE TRIGGER spans_fts_ad AFTER DELETE ON spans BEGIN
  INSERT INTO span_fts(span_fts, rowid, name, input, output)
  VALUES ('delete', old.rowid, old.name, old.input, old.output);
END;

CREATE TRIGGER spans_fts_au AFTER UPDATE ON spans BEGIN
  INSERT INTO span_fts(span_fts, rowid, name, input, output)
  VALUES ('delete', old.rowid, old.name, old.input, old.output);
  INSERT INTO span_fts(rowid, name, input, output)
  VALUES (new.rowid, new.name, new.input, new.output);
END;
