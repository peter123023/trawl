INSERT OR IGNORE INTO model_prices (model, input_per_1k, output_per_1k, updated_at) VALUES
  ('gpt-4o',               0.0025,   0.0100, CAST(strftime('%s','now') AS INTEGER)*1000),
  ('gpt-4o-mini',          0.00015,  0.0006, CAST(strftime('%s','now') AS INTEGER)*1000),
  ('gpt-4.1',              0.0025,   0.0100, CAST(strftime('%s','now') AS INTEGER)*1000),
  ('gpt-4.1-mini',         0.0004,   0.0016, CAST(strftime('%s','now') AS INTEGER)*1000),
  ('claude-sonnet-4',      0.0030,   0.0150, CAST(strftime('%s','now') AS INTEGER)*1000),
  ('claude-3-5-sonnet',    0.0030,   0.0150, CAST(strftime('%s','now') AS INTEGER)*1000),
  ('deepseek-chat',        0.00014,  0.00028, CAST(strftime('%s','now') AS INTEGER)*1000),
  ('qwen-plus',            0.0004,   0.0012, CAST(strftime('%s','now') AS INTEGER)*1000);
