-- 0068_basic_user_role.sql
-- 普通用户默认角色与「删除用户」权限键（计划 §3.5 / §3.5.1 / 决策 D4、D5）。
--
-- 需求口径（需求方 2026-09-18 确认）：
--   * 后台管理只属于管理员；普通用户只有「基础操作」；
--   * 新账号由管理员在后台生成（账号 + 密码），没有自助注册。
--
-- 因此 basic_user = 查询 + 自己案件的常规读写，**不含任何 admin:* 键**，
-- 也不含删除 / 审批 / 复核 / 签署类动作（需要的人由管理员按人授予
-- reviewer / auditor / owner）。
--
-- 幂等：全部 ON CONFLICT DO NOTHING；末尾自检保证「基础用户永远拿不到 admin:*」。

INSERT INTO permissions (key, category, description, risk_level, sensitive)
VALUES ('admin:user:delete', 'admin.identity',
        '删除（离职）用户：软删 + 匿名化 + 归档其个人空间，保留审计引用', 'critical', true)
ON CONFLICT (key) DO NOTHING;

INSERT INTO roles (organization_id, key, name, description, is_system)
SELECT NULL, 'basic_user', '基础用户',
       '普通用户默认角色：链上数据查询 + 自己案件的常规读写；不含任何后台管理权限',
       true
 WHERE NOT EXISTS (SELECT 1 FROM roles WHERE key = 'basic_user' AND is_system);

-- 逐条声明（与计划 §3.5.1 一一对应）；只授予权限目录里真实存在的键。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, wanted.key
  FROM roles r
  JOIN (VALUES
    -- 查询（共享链上数据）
    ('explorer:read'), ('explorer:fetch'), ('address:read'), ('transaction:read'), ('token:read'),
    ('search:read'), ('search:global'), ('dashboard:read'),
    -- 案件
    ('case:list'), ('case:read'), ('case:create'), ('case:update'), ('case:activity:read'),
    ('case:comment:create'), ('comment:list'), ('subject:list'), ('subject:create'), ('subject:update'),
    ('case:member:list'),
    -- 调查
    ('investigation:list'), ('investigation:read'), ('investigation:create'), ('investigation:update'),
    ('investigation:run'), ('investigation:start'), ('investigation:pause'), ('investigation:resume'),
    ('investigation:retry'), ('investigation:cancel'),
    -- 画布
    ('canvas:list'), ('canvas:read'), ('canvas:create'), ('canvas:update'), ('canvas:branch'),
    ('canvas:snapshot'), ('canvas:snapshot:create'),
    -- 报告 / 发现 / 证据
    ('report:list'), ('report:read'), ('report:create'), ('report:download'),
    ('finding:list'), ('finding:read'), ('finding:create'), ('finding:update'),
    ('evidence:list'), ('evidence:read'), ('evidence:create'), ('evidence:download'), ('evidence:snapshot:create'),
    -- 下载 / 作业 / 导出
    ('download:create'), ('download:read'), ('download:preview'), ('download:refresh'), ('download:reuse'),
    ('download:control'), ('job:list:self'), ('job:read'), ('job:control'), ('job:retry'),
    ('export:create'), ('export:read'), ('export:list'), ('export:download'),
    -- 通知与个人
    ('notification:list'), ('notification:read'), ('notification:update'), ('inbox:read'),
    ('profile:read'), ('profile:update'), ('profile:update:self'), ('preference:update'),
    ('security:update'), ('session:self:revoke')
  ) AS wanted(key) ON true
  JOIN permissions p ON p.key = wanted.key
 WHERE r.key = 'basic_user' AND r.is_system
ON CONFLICT DO NOTHING;

-- admin:user:delete 授予平台管理员与安全管理员（两者已有 admin:user:* 的其他键）。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, 'admin:user:delete'
  FROM roles r
 WHERE r.is_system AND r.key IN ('platform_admin', 'security_admin')
ON CONFLICT DO NOTHING;

-- 自检一：basic_user 绝不能持有任何 admin:* 权限。
DO $$
DECLARE
    leaks text;
BEGIN
    SELECT string_agg(rp.permission_key, ', ') INTO leaks
      FROM role_permissions rp
      JOIN roles r ON r.id = rp.role_id
     WHERE r.key = 'basic_user' AND r.is_system AND rp.permission_key LIKE 'admin:%';
    IF leaks IS NOT NULL THEN
        RAISE EXCEPTION '0068 self-check failed: basic_user holds admin permissions: %', leaks;
    END IF;
END $$;

-- 自检二：基础角色必须真的拿到权限（否则是目录键名写错，静默降级）。
DO $$
DECLARE
    granted integer;
BEGIN
    SELECT count(*) INTO granted
      FROM role_permissions rp JOIN roles r ON r.id = rp.role_id
     WHERE r.key = 'basic_user' AND r.is_system;
    IF granted < 50 THEN
        RAISE EXCEPTION '0068 self-check failed: basic_user only has % permissions (expected >= 50)', granted;
    END IF;
END $$;
