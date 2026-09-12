-- 0053_s6_permission_grants.sql
-- Sprint 6 / S6-DB-01 权限补齐：把控制面写权限授给真正承担后台运维的系统角色。
--
-- 背景：0051/0052 建立了控制面、治理与备份恢复能力，但这些写权限（除 owner 外）
-- 没有任何角色持有 —— 路由一旦挂载，platform_admin 会在 403 处被挡住，
-- ADMIN-003/004/006/007/009/010 无法执行，也无法验证「审计员只读」的正向对照。
--
-- 授权原则（§4.1 / §19）：
--   - platform_admin：平台日常运维（Provider/路由/性能/成本/预算/告警/配置/开关/保留/备份）。
--   - security_admin：安全控制（Kill Switch 与 API Key 生命周期、审批决定、风险处置）。
--   - auditor：只读；本迁移不增加任何写权限（AUDIT-002/ADMIN-* 的「审计员无法写」基线）。
--
-- 全部为幂等 INSERT ... ON CONFLICT DO NOTHING，不删除既有授权。

-- 1. 权限目录补齐（与 policy/catalog.go 保持一致，catalog_sql_test.go 双向校验）。
INSERT INTO permissions (key, category, description, risk_level, sensitive) VALUES
    ('admin:killswitch:engage',  'admin.control',  '启用 Kill Switch',         'critical', false),
    ('admin:killswitch:release', 'admin.control',  '解除 Kill Switch',         'critical', false),
    ('admin:approval:decide',    'admin.control',  '审批高风险操作',           'critical', false),
    ('admin:provider:key:rotate','admin.provider', '轮换 Provider 密钥',       'critical', false),
    ('admin:budget:update',      'admin.provider', '修改预算',                 'critical', false),
    ('admin:config:update',      'admin.system',   '修改系统配置',             'critical', false),
    ('admin:backup:create',      'admin.system',   '创建备份',                 'high',     false),
    ('admin:backup:restore',     'admin.system',   '恢复备份',                 'critical', false),
    ('admin:retention:manage',   'admin.data',     '管理保留策略',             'critical', false),
    ('admin:deletion:request',   'admin.data',     '发起删除请求',             'critical', false),
    ('admin:deletion:approve',   'admin.data',     '审批删除请求',             'critical', false),
    ('admin:storage:manage',     'admin.data',     '管理对象存储',             'high',     false),
    ('admin:dataset:manage',     'admin.data',     '重建 Dataset',             'critical', false)
ON CONFLICT (key) DO UPDATE SET category = EXCLUDED.category, description = EXCLUDED.description,
    risk_level = EXCLUDED.risk_level, sensitive = EXCLUDED.sensitive;

-- 2. platform_admin：平台运维写权限。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM roles r
CROSS JOIN (VALUES
    ('admin:killswitch:engage'),
    ('admin:killswitch:release'),
    ('admin:approval:decide'),
    ('admin:provider:key:rotate'),
    ('admin:budget:update'),
    ('admin:config:update'),
    ('admin:backup:create'),
    ('admin:backup:restore'),
    ('admin:retention:manage'),
    ('admin:deletion:request'),
    ('admin:deletion:approve'),
    ('admin:storage:manage'),
    ('admin:dataset:manage')
) AS p(key)
WHERE r.key = 'platform_admin' AND r.is_system
ON CONFLICT DO NOTHING;

-- 3. security_admin：安全控制与密钥生命周期。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM roles r
CROSS JOIN (VALUES
    ('admin:killswitch:engage'),
    ('admin:killswitch:release'),
    ('admin:approval:decide'),
    ('admin:provider:key:rotate'),
    ('admin:config:update'),
    ('admin:alert:manage'),
    ('admin:flag:update')
) AS p(key)
WHERE r.key = 'security_admin' AND r.is_system
ON CONFLICT DO NOTHING;

-- 4. 审计员只读基线（AUDIT-002 / ADMIN-* 的负向对照）。
--    本迁移不向 auditor 授予任何 Sprint 6 写权限，也不删除历史授权；
--    验收时用以下查询证明「审计员无法写」是配置事实而不是口头说明：
--      SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id
--      WHERE r.key='auditor' AND rp.permission_key IN
--        ('admin:killswitch:engage','admin:killswitch:release','admin:approval:decide',
--         'admin:provider:key:rotate','admin:budget:update','admin:config:update',
--         'admin:backup:create','admin:backup:restore','admin:retention:manage',
--         'admin:deletion:request','admin:deletion:approve','admin:provider:update',
--         'admin:routing:update','admin:performance:update','admin:alert:manage',
--         'admin:flag:update','admin:job:control');  -- 期望 0
