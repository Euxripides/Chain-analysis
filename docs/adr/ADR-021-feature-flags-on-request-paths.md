# ADR-021：功能开关在真实请求路径上求值

| 项 | 值 |
|---|---|
| 状态 | Accepted（2026-09-16） |
| 关联 | 计划 §7.3「功能开关必须在真实请求路径求值，不能只在后台页面试算」、§16.7、§21.6 控制面、ADR-017 S6 控制面契约 |
| 触发项 | 验收标准 §16.7 复核：开关改不动任何真实行为（此前只有后台试算会求值） |

## 背景

平台在 Sprint 6 建好了功能开关的**全套后台能力**：`feature_flags` 表（环境 / 组织 / 工作区 / 用户作用域 + 百分比灰度）、
`POST /api/admin/v1/feature-flags/{key}/commands/evaluate` 试算、`feature_flag_evaluations` 评估记录、
`admin:flag:read` / `admin:flag:update` 权限与审计、后台「功能开关」页面（列表、试算、回滚）。

但**没有任何业务请求读过 `feature_flags`**：把 `user.explorer_v2` 关掉，Explorer 照样返回数据；
把 `admin.alert_center` 关掉，告警中心照样可用。开关事实上是一张"配置展示表"，
这与 §7.3 的要求（真实请求路径求值）不符，也让 §16.7 无法关闭。

## 决策

1. **求值算法只有一份。** 新增 `internal/featureflag.Evaluate`，后台试算（`s6EvaluateFlag`）改为调用它。
   两处共用同一实现，杜绝"试算说开、线上说关"（这条由 `featureflag_route_contract_test.go` 静态断言）。
2. **能力面挂在开关下，安全与恢复路径不挂。** 本轮接线：
   `user.explorer_v2` → 用户层 Explorer 全部读端点；
   `admin.alert_center` → 后台告警列表 / 详情 / 命令；
   `admin.control_plane_v2` → 控制面**只读**视图（state / actions / policies GET / kill-switch requests GET / health）。
   Kill Switch 的影响预览、发起、审批、执行、恢复**一律不挂开关**：把恢复能力做成可灰度的开关，
   等于给出一个"悄悄关掉恢复"的入口（该边界由同一个契约测试断言）。
3. **拒绝是显式的、稳定的。** 开关关闭时返回 `503 FEATURE_DISABLED`，details 带 `flag` / `reason` / `revision`；
   开关不存在或读库失败同样拒绝（fail closed），理由 `flag_not_found` / `evaluation_error`。
   不允许"静默降级成空数据"——那会让前端把"被关闭"显示成"没有数据"。
4. **真实流量的求值必须留痕。** 拒绝时同步写 `feature_flag_evaluations`（保证响应返回时即可查询），
   放行时异步写并按 `(flag, subject, reason)` 每分钟去重（不给热路径加写库）。
   于是"开关是否真的在请求路径上被求值"有可查询的证据，而不只是后台试算那一条。
5. **读取带 10 秒 TTL 缓存，写入立即失效。** 开关是低频改、高频读的配置；
   后台写入路径（create / update / rollback）在落库后调用 `invalidateFlagCache()`，
   避免"我明明关了，线上还按旧值放行 10 秒"。

## 被否决的方案

| 方案 | 否决理由 |
|---|---|
| 继续只在后台试算 | 直接违反 §7.3；试算记录会给出"开关在工作"的错觉 |
| 开关关闭时返回空数据 / 200 + `enabled:false` 字段 | 前端与运维都无法区分"被关闭"与"真的没有数据"，是最难排查的一种降级 |
| 给 Kill Switch 恢复路径也加开关 | 安全能力被"灰度"掉之后，事故中会失去恢复手段；恢复必须始终可用 |
| 每次请求都查一次 `feature_flags` | 热路径多一次库往返；开关变更频率极低，短 TTL 缓存足够 |
| 为每条路由各写一份求值逻辑 | 算法分叉，试算与线上行为迟早不一致 |

## 影响

- **正向**：§7.3 / §16.7 可关闭；开关可用真实流量验证（关掉 → 真实请求 503 + 评估记录 → 打开 → 恢复）；
  运维可以按 subject 查询"线上按哪个 revision、什么 reason 放行/拒绝了谁"。
- **代价**：受开关保护的路由多一次内存查表（TTL 命中时零库操作）；关闭状态下的请求会多一次同步写库
  （拒绝是低频事件，可接受）。
- **边界**：开关目前只覆盖上述三条能力面；`admin.provider_registry` / `admin.backup_restore` / `user.gauge_rollout`
  仍只在后台可试算，未接线到请求路径——**这是公开的未完成项**，见 `ROUND-20260916-defect-closure.md` 开放项。

## 回滚方式

1. 把 `feature_flags` 里对应 key 的 `enabled` 置回 `true`、`percentage` 置回 `100`（无需改代码）；
2. 需要彻底回滚接线时，去掉路由上的 `s.requireFlag(...)` 包装并重新部署——
   `internal/api/featureflag_route_contract_test.go` 会立刻失败，提醒回滚者同步更新契约。
无迁移、无数据改写。
