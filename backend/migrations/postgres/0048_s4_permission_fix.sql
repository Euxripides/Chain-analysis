-- 0048 Sprint 4 权限补齐：Viewer 需要 notification:list 才能打开通知中心。
--
-- 0047 已为 viewer 授予 subject:list / evidence:list；但通知路由使用
-- notification:list（0041 早期只给 viewer 授了 notification:read），
-- 导致 viewer 打开通知页被路由层 403，既不符合 §22.3 默认角色映射，
-- 也无法验证 USER-010 的“通知对象无权时安全失败”路径。
INSERT INTO permissions (key, category, description, risk_level, sensitive) VALUES
    ('notification:list', 'notification', '列出通知', 'low', false)
ON CONFLICT (key) DO UPDATE SET category = EXCLUDED.category, description = EXCLUDED.description,
    risk_level = EXCLUDED.risk_level, sensitive = EXCLUDED.sensitive;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, 'notification:list' FROM roles r
WHERE r.key IN ('viewer', 'auditor') AND r.is_system
ON CONFLICT DO NOTHING;
