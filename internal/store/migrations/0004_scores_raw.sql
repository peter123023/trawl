-- 0004: 保留 judge 的原始响应。
-- 技术说明书 §7.2 要求 judge 输出必须可审计：解析失败或分数可疑时，
-- 只有 raw_response 能回答"模型到底返回了什么"。
ALTER TABLE scores ADD COLUMN raw_response TEXT NOT NULL DEFAULT '';
