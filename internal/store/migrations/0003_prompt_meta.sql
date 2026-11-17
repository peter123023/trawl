-- Prompt lineage: which prompt version produced this generation.
-- Kept as first-class columns so /api/prompts can aggregate without JSON scans.
ALTER TABLE spans ADD COLUMN prompt_name TEXT NOT NULL DEFAULT '';
ALTER TABLE spans ADD COLUMN prompt_version INTEGER;

CREATE INDEX IF NOT EXISTS idx_spans_prompt
  ON spans (project_id, prompt_name, prompt_version);
