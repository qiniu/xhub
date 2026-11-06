-- 只保存请求键和载荷的摘要，不存储对话正文、凭据或响应；过期记录按有界批次清理。
CREATE TABLE IF NOT EXISTS inference_idempotency_fences (
    key_hash TEXT PRIMARY KEY,
    request_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS inference_idempotency_fences_expiry ON inference_idempotency_fences (expires_at);
