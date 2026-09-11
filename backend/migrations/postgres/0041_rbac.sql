-- 0041 RBAC：权限目录、角色、成员关系、角色绑定
-- 对应 docs/dual-system-development-plan.md §4 / §6.4 / Sprint S3-DB-01。
--
-- 设计：
--   * 权限键统一为 resource:action（§4.2）。
--   * 系统角色 organization_id IS NULL，可被组织复制后再改。
--   * 授权事实来源为 member_role_bindings；users.role 只用于本迁移回填。
--   * 作用域 scope_type ∈ {organization, workspace, case, self}。

CREATE TABLE IF NOT EXISTS permissions (
    key         text PRIMARY KEY,
    category    text NOT NULL,
    description text NOT NULL DEFAULT '',
    risk_level  text NOT NULL DEFAULT 'low' CHECK (risk_level IN ('low', 'medium', 'high', 'critical')),
    sensitive   boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS roles (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
    key             text NOT NULL,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    is_system       boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS roles_system_key_uidx ON roles (key) WHERE is_system;
CREATE UNIQUE INDEX IF NOT EXISTS roles_org_key_uidx ON roles (organization_id, key) WHERE organization_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS role_permissions (
    role_id        uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_key text NOT NULL REFERENCES permissions(key) ON DELETE CASCADE,
    granted_at     timestamptz NOT NULL DEFAULT now(),
    granted_by     uuid REFERENCES identities(id),
    PRIMARY KEY (role_id, permission_key)
);

CREATE TABLE IF NOT EXISTS organization_memberships (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    identity_id     uuid NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
    status          text NOT NULL DEFAULT 'active' CHECK (status IN ('invited', 'active', 'suspended', 'removed')),
    title           text NOT NULL DEFAULT '',
    joined_at       timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, identity_id)
);

CREATE TABLE IF NOT EXISTS workspace_memberships (
    workspace_id    uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    identity_id     uuid NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
    status          text NOT NULL DEFAULT 'active' CHECK (status IN ('invited', 'active', 'suspended', 'removed')),
    joined_at       timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, identity_id)
);
CREATE INDEX IF NOT EXISTS workspace_memberships_identity_idx ON workspace_memberships (identity_id, status);

CREATE TABLE IF NOT EXISTS member_role_bindings (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_id  uuid NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
    role_id      uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    scope_type   text NOT NULL CHECK (scope_type IN ('organization', 'workspace', 'case', 'self')),
    scope_id     uuid,
    granted_by   uuid REFERENCES identities(id),
    granted_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz,
    revoked_at   timestamptz,
    reason       text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS member_role_bindings_uidx
    ON member_role_bindings (identity_id, role_id, scope_type, COALESCE(scope_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS member_role_bindings_identity_idx ON member_role_bindings (identity_id) WHERE revoked_at IS NULL;

-- ABAC 条件策略：RBAC 之外的少量对象级条件（§4.2）。默认不启用 deny 规则。
CREATE TABLE IF NOT EXISTS access_policies (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
    key          text NOT NULL,
    effect       text NOT NULL DEFAULT 'allow' CHECK (effect IN ('allow', 'deny')),
    permission_key text NOT NULL,
    condition    jsonb NOT NULL DEFAULT '{}'::jsonb,
    priority     integer NOT NULL DEFAULT 100,
    enabled      boolean NOT NULL DEFAULT true,
    description  text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS access_policies_key_uidx ON access_policies (key);

-- ============================ 权限目录 ============================
INSERT INTO permissions (key, category, description, risk_level, sensitive) VALUES
    -- 案件
    ('case:create', 'case', '创建案件', 'low', false),
    ('case:read', 'case', '查看案件', 'low', false),
    ('case:update', 'case', '修改案件', 'medium', false),
    ('case:archive', 'case', '归档/恢复案件', 'medium', false),
    ('case:delete', 'case', '删除案件', 'high', false),
    ('case:member:manage', 'case', '管理案件成员', 'high', false),
    ('case:activity:read', 'case', '查看案件动态', 'low', false),
    ('case:comment:create', 'case', '发表案件评论', 'low', false),
    ('case:subject:manage', 'case', '管理案件对象', 'medium', false),
    -- 调查
    ('investigation:create', 'investigation', '创建调查任务', 'low', false),
    ('investigation:read', 'investigation', '查看调查任务', 'low', false),
    ('investigation:update', 'investigation', '修改调查参数', 'medium', false),
    ('investigation:run', 'investigation', '启动/暂停/继续调查', 'medium', false),
    ('investigation:cancel', 'investigation', '取消调查', 'medium', false),
    ('investigation:approve', 'investigation', '复核调查结论', 'high', false),
    ('investigation:delete', 'investigation', '删除调查', 'high', false),
    -- 结论与证据
    ('finding:create', 'finding', '新增 Finding', 'low', false),
    ('finding:read', 'finding', '查看 Finding', 'low', false),
    ('finding:update', 'finding', '修改 Finding', 'medium', false),
    ('finding:review', 'finding', '复核 Finding', 'high', false),
    ('evidence:read', 'evidence', '查看证据元数据', 'low', false),
    ('evidence:create', 'evidence', '添加证据', 'low', false),
    ('evidence:download', 'evidence', '下载证据文件', 'medium', true),
    ('evidence:delete', 'evidence', '移除证据', 'high', false),
    ('evidence:sensitive:read', 'evidence', '读取敏感证据正文', 'critical', true),
    ('evidence:snapshot:create', 'evidence', '固化证据快照', 'medium', false),
    -- 图谱
    ('canvas:create', 'canvas', '创建图谱画布', 'low', false),
    ('canvas:read', 'canvas', '查看图谱', 'low', false),
    ('canvas:update', 'canvas', '编辑图谱', 'medium', false),
    ('canvas:delete', 'canvas', '删除图谱', 'high', false),
    ('canvas:snapshot:create', 'canvas', '创建图谱快照', 'medium', false),
    -- 数据查询
    ('explorer:read', 'explorer', '查询本地数据', 'low', false),
    ('explorer:fetch', 'explorer', '触发链上取数', 'medium', false),
    ('address:read', 'explorer', '查看地址详情', 'low', false),
    ('transaction:read', 'explorer', '查看交易详情', 'low', false),
    ('token:read', 'explorer', '查看 Token 元数据', 'low', false),
    ('search:global', 'explorer', '使用全局搜索', 'low', false),
    -- 下载与任务
    ('download:preview', 'download', '预览 Coverage 与影响', 'low', false),
    ('download:create', 'download', '创建下载任务', 'medium', false),
    ('download:read', 'download', '查看下载任务', 'low', false),
    ('download:reuse', 'download', '复用已有 Coverage 数据', 'low', false),
    ('download:control', 'download', '暂停/继续/取消下载', 'medium', false),
    ('download:delete', 'download', '删除下载任务数据', 'high', false),
    ('job:read', 'job', '查看任务', 'low', false),
    ('job:control', 'job', '控制任务', 'medium', false),
    ('job:retry', 'job', '重试任务', 'medium', false),
    ('job:diagnose', 'job', '查看任务诊断信息', 'medium', false),
    ('job:admin', 'job', '全局任务控制', 'high', false),
    -- 导出与报告
    ('export:create', 'export', '创建导出', 'medium', false),
    ('export:read', 'export', '查看导出', 'low', false),
    ('export:download', 'export', '下载导出文件', 'medium', true),
    ('export:delete', 'export', '删除导出', 'high', false),
    ('report:create', 'report', '生成报告', 'low', false),
    ('report:read', 'report', '查看报告', 'low', false),
    ('report:download', 'report', '下载报告', 'medium', true),
    ('report:approve', 'report', '审批报告', 'high', false),
    ('report:sign', 'report', '签署报告', 'critical', false),
    ('report:delete', 'report', '删除报告', 'high', false),
    -- 协作
    ('notification:read', 'notification', '查看通知', 'low', false),
    ('notification:manage', 'notification', '管理通知偏好', 'low', false),
    ('inbox:read', 'notification', '查看待我处理', 'low', false),
    ('inbox:assign', 'notification', '分派待办', 'medium', false),
    -- 个人
    ('profile:read', 'self', '查看个人资料', 'low', false),
    ('profile:update', 'self', '修改个人资料', 'low', false),
    ('preference:update', 'self', '修改使用偏好', 'low', false),
    ('security:update', 'self', '修改安全设置', 'high', false),
    ('session:self:revoke', 'self', '撤销自己的会话', 'medium', false),
    -- 后台：组织与成员
    ('admin:dashboard:read', 'admin.ops', '查看运营总览', 'low', false),
    ('admin:org:read', 'admin.identity', '查看组织', 'low', false),
    ('admin:org:manage', 'admin.identity', '管理组织', 'high', false),
    ('admin:workspace:read', 'admin.identity', '查看工作区', 'low', false),
    ('admin:workspace:manage', 'admin.identity', '管理工作区', 'high', false),
    ('admin:user:read', 'admin.identity', '查看用户', 'medium', false),
    ('admin:user:create', 'admin.identity', '创建用户', 'high', false),
    ('admin:user:update', 'admin.identity', '修改用户', 'high', false),
    ('admin:user:disable', 'admin.identity', '停用用户', 'critical', false),
    ('admin:user:invite', 'admin.identity', '邀请用户', 'medium', false),
    ('admin:user:password:reset', 'admin.identity', '重置用户密码', 'critical', false),
    ('admin:role:read', 'admin.identity', '查看角色', 'low', false),
    ('admin:role:manage', 'admin.identity', '管理角色与权限', 'critical', false),
    ('admin:permission:read', 'admin.identity', '查看权限目录', 'low', false),
    ('admin:member:read', 'admin.identity', '查看成员关系', 'medium', false),
    ('admin:member:manage', 'admin.identity', '管理成员关系', 'high', false),
    ('admin:session:read', 'admin.security', '查看会话', 'medium', false),
    ('admin:session:revoke', 'admin.security', '强制下线会话', 'critical', false),
    ('admin:login-event:read', 'admin.security', '查看登录记录', 'medium', true),
    ('admin:risk:read', 'admin.security', '查看风险事件', 'medium', false),
    ('admin:risk:manage', 'admin.security', '处置风险事件', 'high', false),
    ('admin:audit:read', 'admin.security', '查看审计日志', 'medium', true),
    ('admin:audit:export', 'admin.security', '导出审计日志', 'critical', true),
    ('admin:audit:verify', 'admin.security', '校验审计哈希链', 'medium', false),
    ('admin:apikey:read', 'admin.security', '查看 API/MCP 密钥', 'high', true),
    ('admin:apikey:create', 'admin.security', '创建 API/MCP 密钥', 'critical', false),
    ('admin:apikey:revoke', 'admin.security', '吊销 API/MCP 密钥', 'critical', false),
    ('admin:killswitch:read', 'admin.control', '查看 Kill Switch', 'medium', false),
    ('admin:killswitch:engage', 'admin.control', '启用 Kill Switch', 'critical', false),
    ('admin:killswitch:release', 'admin.control', '解除 Kill Switch', 'critical', false),
    ('admin:approval:read', 'admin.control', '查看审批请求', 'medium', false),
    ('admin:approval:decide', 'admin.control', '审批高风险操作', 'critical', false),
    ('admin:alert:read', 'admin.control', '查看告警', 'low', false),
    ('admin:alert:manage', 'admin.control', '处置告警', 'high', false),
    -- 后台：Provider 与成本
    ('admin:provider:read', 'admin.provider', '查看 Provider', 'low', false),
    ('admin:provider:update', 'admin.provider', '修改 Provider 配置', 'high', false),
    ('admin:provider:key:rotate', 'admin.provider', '轮换 Provider 密钥', 'critical', false),
    ('admin:routing:read', 'admin.provider', '查看路由与限流', 'low', false),
    ('admin:routing:update', 'admin.provider', '修改路由与限流', 'high', false),
    ('admin:performance:read', 'admin.provider', '查看性能模式', 'low', false),
    ('admin:performance:update', 'admin.provider', '修改性能模式', 'high', false),
    ('admin:cost:read', 'admin.provider', '查看成本', 'medium', false),
    ('admin:budget:read', 'admin.provider', '查看预算', 'medium', false),
    ('admin:budget:update', 'admin.provider', '修改预算', 'critical', false),
    ('admin:request:read', 'admin.provider', '查看请求审计', 'medium', true),
    -- 后台：任务与数据
    ('admin:job:read', 'admin.data', '查看全局任务', 'low', false),
    ('admin:job:control', 'admin.data', '控制全局任务', 'high', false),
    ('admin:job:cancel', 'admin.data', '取消任意任务', 'critical', false),
    ('admin:job:retry', 'admin.data', '重试任意任务', 'high', false),
    ('admin:coverage:read', 'admin.data', '查看 Coverage 与数据质量', 'low', false),
    ('admin:dataset:read', 'admin.data', '查看 Dataset', 'low', false),
    ('admin:dataset:manage', 'admin.data', '重建 Dataset', 'critical', false),
    ('admin:export:read', 'admin.data', '查看全部导出', 'medium', true),
    ('admin:export:delete', 'admin.data', '删除导出对象', 'critical', false),
    ('admin:storage:read', 'admin.data', '查看对象存储用量', 'low', false),
    ('admin:storage:manage', 'admin.data', '管理对象存储', 'high', false),
    ('admin:retention:read', 'admin.data', '查看保留策略', 'low', false),
    ('admin:retention:manage', 'admin.data', '管理保留策略', 'critical', false),
    ('admin:deletion:read', 'admin.data', '查看删除请求', 'medium', false),
    ('admin:deletion:request', 'admin.data', '发起删除请求', 'critical', false),
    ('admin:deletion:approve', 'admin.data', '审批删除请求', 'critical', false),
    -- 后台：系统
    ('admin:config:read', 'admin.system', '查看系统配置', 'medium', false),
    ('admin:config:update', 'admin.system', '修改系统配置', 'critical', false),
    ('admin:config:history:read', 'admin.system', '查看配置历史', 'medium', false),
    ('admin:flag:read', 'admin.system', '查看功能开关', 'low', false),
    ('admin:flag:update', 'admin.system', '修改功能开关', 'high', false),
    ('admin:backup:read', 'admin.system', '查看备份', 'medium', false),
    ('admin:backup:create', 'admin.system', '创建备份', 'high', false),
    ('admin:backup:restore', 'admin.system', '恢复备份', 'critical', false),
    ('admin:version:read', 'admin.system', '查看版本与迁移', 'low', false),
    ('admin:version:manage', 'admin.system', '执行版本与迁移操作', 'critical', false),
    ('admin:notification-channel:read', 'admin.system', '查看通知渠道', 'low', false),
    ('admin:notification-channel:manage', 'admin.system', '管理通知渠道', 'high', false),
    ('admin:system:read', 'admin.system', '查看系统健康', 'low', false),
    ('dashboard:read', 'dashboard', '查看个人工作台', 'low', false),
    ('search:read', 'explorer', '执行全局搜索', 'low', false),
    ('case:list', 'case', '列出案件', 'low', false),
    ('case:state:update', 'case', '案件状态命令', 'medium', false),
    ('case:member:list', 'case', '查看案件成员', 'low', false),
    ('subject:list', 'case', '查看案件对象', 'low', false),
    ('subject:create', 'case', '新增案件对象', 'medium', false),
    ('comment:list', 'case', '查看案件评论', 'low', false),
    ('comment:create', 'case', '发表案件评论', 'low', false),
    ('investigation:list', 'investigation', '列出调查任务', 'low', false),
    ('finding:list', 'finding', '列出 Finding', 'low', false),
    ('evidence:review', 'evidence', '复核证据状态', 'high', false),
    ('canvas:list', 'canvas', '列出图谱画布', 'low', false),
    ('canvas:branch', 'canvas', '创建图谱分支', 'medium', false),
    ('canvas:snapshot', 'canvas', '创建/查看图谱快照', 'medium', false),
    ('report:list', 'report', '列出报告', 'low', false),
    ('job:list:self', 'job', '列出本人任务', 'low', false),
    ('export:list', 'export', '列出导出', 'low', false),
    ('notification:list', 'notification', '列出通知', 'low', false),
    ('notification:update', 'notification', '更新通知状态', 'low', false),
    ('profile:update:self', 'self', '修改本人资料', 'low', false),
    ('admin:metrics:read', 'admin.system', '查看平台指标', 'low', false)
ON CONFLICT (key) DO UPDATE SET category = EXCLUDED.category, description = EXCLUDED.description,
    risk_level = EXCLUDED.risk_level, sensitive = EXCLUDED.sensitive;

-- ============================ 系统角色 ============================
INSERT INTO roles (organization_id, key, name, description, is_system) VALUES
    (NULL, 'owner',          'Owner',          '组织所有者：全组织案件、数据与后台全局配置', true),
    (NULL, 'platform_admin', 'Platform Admin', '平台管理员：系统、Provider、任务、告警与配置', true),
    (NULL, 'security_admin', 'Security Admin', '安全管理员：认证策略、会话、密钥与风险事件', true),
    (NULL, 'auditor',        'Auditor',        '审计员：只读访问审计、配置历史与导出校验', true),
    (NULL, 'investigator',   'Investigator',   '调查员：案件、调查、图谱、结论、证据与报告', true),
    (NULL, 'analyst',        'Analyst',        '分析员：查询、分析、导出与协作', true),
    (NULL, 'reviewer',       'Reviewer',       '复核人：查看证据、审批结论、签署报告', true),
    (NULL, 'viewer',         'Viewer',         '只读协作者：查看已授权案件与报告', true),
    (NULL, 'api_client',     'API Client',     'API 客户端：仅声明的 scope，不能登录后台', true)
ON CONFLICT DO NOTHING;

-- Owner：全部权限
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM roles r CROSS JOIN permissions p WHERE r.key = 'owner' AND r.is_system
ON CONFLICT DO NOTHING;

-- Platform Admin：后台运维与系统控制；案件只读元数据。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'case:read', 'investigation:read', 'download:read', 'job:read', 'export:read', 'report:read',
    'admin:dashboard:read', 'admin:system:read', 'admin:metrics:read',
    'admin:org:read', 'admin:workspace:read', 'admin:user:read', 'admin:role:read', 'admin:permission:read',
    'admin:member:read', 'admin:session:read', 'admin:login-event:read', 'admin:risk:read',
    'admin:audit:read', 'admin:audit:verify', 'admin:apikey:read',
    'admin:provider:read', 'admin:provider:update', 'admin:routing:read', 'admin:routing:update',
    'admin:performance:read', 'admin:performance:update', 'admin:cost:read', 'admin:budget:read',
    'admin:request:read',
    'admin:job:read', 'admin:job:control', 'admin:job:retry', 'admin:coverage:read',
    'admin:dataset:read', 'admin:export:read', 'admin:storage:read', 'admin:retention:read',
    'admin:alert:read', 'admin:alert:manage', 'admin:approval:read',
    'admin:killswitch:read', 'admin:config:read', 'admin:config:history:read',
    'admin:flag:read', 'admin:flag:update', 'admin:backup:read', 'admin:version:read',
    'admin:notification-channel:read'
]) AS k WHERE r.key = 'platform_admin' AND r.is_system
ON CONFLICT DO NOTHING;

-- Security Admin：认证策略、会话、密钥、风险与审计。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'admin:dashboard:read', 'admin:user:read', 'admin:session:read', 'admin:session:revoke',
    'admin:login-event:read', 'admin:risk:read', 'admin:risk:manage',
    'admin:audit:read', 'admin:audit:verify', 'admin:apikey:read', 'admin:apikey:create',
    'admin:apikey:revoke', 'admin:role:read', 'admin:permission:read', 'admin:member:read',
    'admin:org:read', 'admin:workspace:read', 'admin:system:read',
    'admin:killswitch:read', 'admin:config:read', 'admin:config:history:read', 'admin:flag:read'
]) AS k WHERE r.key = 'security_admin' AND r.is_system
ON CONFLICT DO NOTHING;

-- Auditor：只读审计与配置历史，不得执行 Kill Switch 或更改权限。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'admin:dashboard:read', 'admin:audit:read', 'admin:audit:export', 'admin:audit:verify',
    'admin:login-event:read', 'admin:session:read', 'admin:risk:read',
    'admin:config:read', 'admin:config:history:read', 'admin:backup:read',
    'admin:version:read', 'admin:user:read', 'admin:role:read', 'admin:permission:read',
    'admin:org:read', 'admin:workspace:read', 'admin:job:read', 'admin:export:read', 'admin:system:read'
]) AS k WHERE r.key = 'auditor' AND r.is_system
ON CONFLICT DO NOTHING;

-- Investigator：用户层完整闭环。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'case:create', 'case:read', 'case:update', 'case:archive', 'case:member:manage',
    'case:activity:read', 'case:comment:create', 'case:subject:manage',
    'investigation:create', 'investigation:read', 'investigation:update', 'investigation:run',
    'investigation:cancel', 'finding:create', 'finding:read', 'finding:update',
    'evidence:read', 'evidence:create', 'evidence:download', 'evidence:snapshot:create',
    'canvas:create', 'canvas:read', 'canvas:update', 'canvas:snapshot:create',
    'explorer:read', 'explorer:fetch', 'address:read', 'transaction:read', 'token:read',
    'search:global', 'download:preview', 'download:create', 'download:read', 'download:control',
    'download:reuse', 'job:read', 'job:control', 'job:retry', 'job:diagnose',
    'export:create', 'export:read', 'export:download', 'report:create', 'report:read', 'report:download',
    'notification:read', 'notification:manage', 'inbox:read',
    'profile:read', 'profile:update', 'preference:update', 'security:update', 'session:self:revoke'
]) AS k WHERE r.key = 'investigator' AND r.is_system
ON CONFLICT DO NOTHING;

-- Analyst：查询、分析、导出。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'case:read', 'case:activity:read', 'case:comment:create',
    'investigation:create', 'investigation:read', 'investigation:update', 'investigation:run',
    'finding:create', 'finding:read', 'finding:update',
    'evidence:read', 'evidence:create', 'evidence:snapshot:create',
    'canvas:create', 'canvas:read', 'canvas:update', 'canvas:snapshot:create',
    'explorer:read', 'explorer:fetch', 'address:read', 'transaction:read', 'token:read',
    'search:global', 'download:preview', 'download:create', 'download:read', 'download:control',
    'download:reuse', 'job:read', 'job:control', 'job:retry',
    'export:create', 'export:read', 'export:download', 'report:create', 'report:read', 'report:download',
    'notification:read', 'notification:manage', 'inbox:read',
    'profile:read', 'profile:update', 'preference:update', 'security:update', 'session:self:revoke'
]) AS k WHERE r.key = 'analyst' AND r.is_system
ON CONFLICT DO NOTHING;

-- Reviewer：查看证据、审批结论、签署报告。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'case:read', 'case:activity:read', 'case:comment:create',
    'investigation:read', 'investigation:approve', 'investigation:cancel',
    'finding:read', 'finding:review',
    'evidence:read', 'evidence:download', 'evidence:sensitive:read',
    'canvas:read', 'explorer:read', 'address:read', 'transaction:read', 'token:read', 'search:global',
    'download:read', 'job:read', 'export:read', 'export:download',
    'report:read', 'report:download', 'report:approve', 'report:sign',
    'notification:read', 'notification:manage', 'inbox:read', 'inbox:assign',
    'profile:read', 'profile:update', 'preference:update', 'security:update', 'session:self:revoke'
]) AS k WHERE r.key = 'reviewer' AND r.is_system
ON CONFLICT DO NOTHING;

-- Viewer：只读。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'case:read', 'case:activity:read', 'investigation:read', 'finding:read', 'evidence:read',
    'canvas:read', 'explorer:read', 'address:read', 'transaction:read', 'token:read', 'search:global',
    'job:read', 'export:read', 'report:read',
    'notification:read', 'inbox:read', 'profile:read', 'preference:update', 'session:self:revoke'
]) AS k WHERE r.key = 'viewer' AND r.is_system
ON CONFLICT DO NOTHING;

-- API Client：仅声明的 scope（默认最小集，由密钥 scope 进一步收窄）。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY['explorer:read', 'address:read', 'transaction:read', 'token:read'])
    AS k WHERE r.key = 'api_client' AND r.is_system
ON CONFLICT DO NOTHING;

-- ============================ 回填 ============================
INSERT INTO organization_memberships (organization_id, identity_id, status, joined_at)
SELECT organization_id, id, 'active', created_at FROM identities
ON CONFLICT (organization_id, identity_id) DO NOTHING;

INSERT INTO workspace_memberships (workspace_id, organization_id, identity_id, status, joined_at)
SELECT w.id, w.organization_id, i.id, 'active', i.created_at
FROM workspaces w JOIN identities i ON i.organization_id = w.organization_id
ON CONFLICT (workspace_id, identity_id) DO NOTHING;

-- users.role → 系统角色绑定（用户层角色绑到组织作用域；管理角色绑到组织作用域 + workspace 作用域）。
INSERT INTO member_role_bindings (identity_id, role_id, scope_type, scope_id, reason)
SELECT u.id, r.id, 'organization', u.organization_id, 'migration from users.role=' || u.role
FROM users u
JOIN identities i ON i.legacy_user_id = u.id
JOIN roles r ON r.is_system AND r.key = CASE u.role
    WHEN 'owner'        THEN 'owner'
    WHEN 'admin'        THEN 'platform_admin'
    WHEN 'analyst'      THEN 'analyst'
    WHEN 'investigator' THEN 'investigator'
    WHEN 'reviewer'     THEN 'reviewer'
    WHEN 'viewer'       THEN 'viewer'
    WHEN 'api_client'   THEN 'api_client'
    ELSE 'viewer'
END
WHERE u.organization_id IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM member_role_bindings b
      WHERE b.identity_id = u.id AND b.role_id = r.id AND b.scope_type = 'organization'
        AND b.scope_id = u.organization_id AND b.revoked_at IS NULL);

-- §21.5 契约中出现的权限键（与上述同义键并存；新增接口一律使用契约键）。
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'dashboard:read', 'search:read', 'case:list', 'case:read', 'case:activity:read',
    'case:member:list', 'comment:list'
]) AS k WHERE r.key IN ('platform_admin', 'security_admin', 'auditor') AND r.is_system
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'dashboard:read', 'search:read', 'case:list', 'case:state:update', 'case:member:list',
    'subject:list', 'subject:create', 'comment:list', 'comment:create', 'investigation:list',
    'finding:list', 'evidence:review', 'canvas:list', 'canvas:branch', 'canvas:snapshot',
    'report:list', 'job:list:self', 'export:list', 'notification:list', 'notification:update',
    'profile:update:self'
]) AS k WHERE r.key = 'investigator' AND r.is_system
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'dashboard:read', 'search:read', 'case:list', 'case:member:list', 'subject:list',
    'comment:list', 'comment:create', 'investigation:list', 'finding:list', 'canvas:list',
    'canvas:branch', 'canvas:snapshot', 'report:list', 'job:list:self', 'export:list',
    'notification:list', 'notification:update', 'profile:update:self'
]) AS k WHERE r.key = 'analyst' AND r.is_system
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'dashboard:read', 'search:read', 'case:list', 'case:member:list', 'comment:list', 'comment:create',
    'investigation:list', 'finding:list', 'evidence:review', 'canvas:list', 'canvas:snapshot',
    'report:list', 'job:list:self', 'export:list', 'notification:list', 'notification:update',
    'profile:update:self'
]) AS k WHERE r.key = 'reviewer' AND r.is_system
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k FROM roles r, unnest(ARRAY[
    'dashboard:read', 'search:read', 'case:list', 'investigation:list', 'finding:list',
    'canvas:list', 'report:list', 'job:list:self', 'export:list', 'notification:list',
    'notification:update', 'profile:update:self'
]) AS k WHERE r.key = 'viewer' AND r.is_system
ON CONFLICT DO NOTHING;
