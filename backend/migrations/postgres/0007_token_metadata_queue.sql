-- 已部署 0006 的环境补充手动解析优先级；全新环境执行时保持幂等。
ALTER TABLE token_metadata_resolve_queue ADD COLUMN IF NOT EXISTS priority SMALLINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_token_resolve_priority
    ON token_metadata_resolve_queue(status, priority DESC, next_retry_at, updated_at);

-- 进程异常退出时 running 任务不会永久滞留。
UPDATE token_metadata_resolve_queue
SET status='retry_wait', next_retry_at=now(), updated_at=now()
WHERE status='running';
