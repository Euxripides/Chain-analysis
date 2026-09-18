# Policy 差异观察（同一主体、同一意图，在旧 v1 与新 v2 路径上的结果）

| 场景 | 旧路径 /api/v1 | 新路径 /api/v2 | 判定 |
|---|---|---|---|
| viewer 读取案件列表 | GET /api/v1/cases -> 200 | GET /api/v2/cases?limit=20 -> 200 | 需人工核对 |
| viewer 删除案件(随机 ID) | DELETE /api/v1/cases/00000000-0000-0000-0000-0000000000ff -> 403 | DELETE /api/v2/cases/00000000-0000-0000-0000-0000000000ff -> 405 | 需人工核对 |
| viewer 访问后台概览 | GET /api/v1/admin/overview -> 404 | GET /api/admin/v1/overview -> 401 | 新路径按策略拒绝 |
| viewer 创建导出 | POST /api/v1/exports -> 403 | POST /api/v2/exports -> 403 | 新路径按策略拒绝 |
| 未认证读取案件列表(对照) | GET /api/v1/cases -> 200 | GET /api/v2/cases?limit=20 -> 200 | 需人工核对 |
