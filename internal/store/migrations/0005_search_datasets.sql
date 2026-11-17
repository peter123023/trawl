-- 0005: 中文可检索。trigram 分词支持 ≥3 字子串；2 字词仍走 LIKE。
-- 用 external content（content='spans'）避免正文双份存储，靠触发器与 rowid 对齐。
CREATE VIRTUAL TABLE IF NOT EXISTS span_fts USING fts5(
  name,
  input,
  output,
  content='spans',
  content_rowid='rowid',
  tokenize='trigram'
);

CREATE TRIGGER IF NOT EXISTS spans_fts_ai AFTER INSERT ON spans BEGIN
  INSERT INTO span_fts(rowid, name, input, output)
  VALUES (new.rowid, new.name, new.input, new.output);
END;

CREATE TRIGGER IF NOT EXISTS spans_fts_ad AFTER DELETE ON spans BEGIN
  INSERT INTO span_fts(span_fts, rowid, name, input, output)
  VALUES ('delete', old.rowid, old.name, old.input, old.output);
END;

CREATE TRIGGER IF NOT EXISTS spans_fts_au AFTER UPDATE ON spans BEGIN
  INSERT INTO span_fts(span_fts, rowid, name, input, output)
  VALUES ('delete', old.rowid, old.name, old.input, old.output);
  INSERT INTO span_fts(rowid, name, input, output)
  VALUES (new.rowid, new.name, new.input, new.output);
END;

-- 回填已有行（触发器只对之后写入生效）
INSERT INTO span_fts(rowid, name, input, output)
SELECT rowid, name, input, output FROM spans;

-- 数据集：从台账里挑样本，形成可回归的集合
CREATE TABLE IF NOT EXISTS datasets (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL,
  name        TEXT NOT NULL,
  note        TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS dataset_items (
  id          TEXT PRIMARY KEY,
  dataset_id  TEXT NOT NULL,
  project_id  TEXT NOT NULL,
  trace_id    TEXT NOT NULL,
  note        TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_dataset_items_uniq ON dataset_items(dataset_id, trace_id);
CREATE INDEX IF NOT EXISTS idx_dataset_items_trace ON dataset_items(project_id, trace_id);
