# 发布范围决定（书面）：OIDC 与三个未接线功能开关

| 项 | 内容 |
|---|---|
| 决定主题 | ① OIDC 是否属于本次正式发布范围；② `admin.provider_registry` / `admin.backup_restore` / `user.gauge_rollout` 三个功能开关是否在本次发布接线到请求路径 |
| 依据条款 | `docs/dual-system-completion-acceptance-criteria.md` §16.5（OIDC 范围必须书面决定）、§16.7 与 §7.3（功能开关必须在真实请求路径求值） |
| 决定时间 | 2026-09-16 |
| 冻结版本 | `backend/bin/api` `7831bff0…`、`worker` `0d49fb75…`、`migrate` `d48e58fe…`；用户台 `71714cdc…`、后台台 `2d4a129e…` |
| 决定者 | 执行方（本文件为书面建议）；**须由发布批准人在发布窗口内确认**（§16.12） |

---

## 1. OIDC：**不属于**本次正式发布范围

**决定**：OIDC / 联邦登录不进入本次发布范围。代码保持"显式未启用"：`/api/auth/v1/oidc/*` 一律返回 `501 OIDC_NOT_CONFIGURED`，不在需求与契约中把 OIDC 描述成可用能力。

理由：

1. 本次发布没有真实 IdP 可用（§16.5 的判定条件之一），没有真实 IdP 就无法完成 §27 AUTH-011 要求的 state / nonce / PKCE / 回调失败 / 审计的 **live 负向验收**；
2. 适配器层已经实现并通过隔离测试（`backend/internal/iam/oidc_test.go`：`TestOIDCRejectsTamperedState`、`TestOIDCRejectsWrongNonce`、`TestOIDCRejectsTamperedIDToken`、`TestOIDCRejectsExpiredToken`、`TestOIDCDisabledKeepsProductionClosed`），但"适配器正确"不等于"接真实 IdP 可用"；
3. 把未接线的能力写进契约会让下游误判可用性 —— 平台自身的规则是"不能把未实现能力写成可用"（§8）。

**对验收矩阵的影响（显式写清，不用含糊状态混过）**：

- `AUTH-011` 在矩阵中的状态为 **PASS（范围外）**：判据是"能力未被启用、契约显式声明 501、启用路径的协议负向由适配器测试覆盖"。**这不是**"OIDC 已通过 live 验收"；真实 IdP 验收仍然欠着。若未来把 OIDC 纳入范围，必须重新打开该条目并接真实 IdP。
- 需要同步的产物：`openapi/auth-v1.yaml` 的 OIDC 段落保持"未启用（501）"语义；手册不宣传 OIDC 登录步骤。

## 2. 三个未接线功能开关：**不在**本次发布接线，保持"后台可试算"

**决定**：`admin.provider_registry`、`admin.backup_restore`、`user.gauge_rollout` 三个开关**不**在本次发布会话接线到请求路径。

理由：

1. 这三个开关各自对应一个**语义决定**，不是纯技术接线：
   - `admin.provider_registry`：关掉它意味着"Provider 账户与路由策略页面整体不可用"，而下载/拉取链路依赖这些配置；关掉之后是"页面打不开"还是"路由回退到配置默认值"必须由产品与运维共同定义；
   - `admin.backup_restore`：备份与恢复页面属于**恢复能力**。ADR-021 的硬性边界是"安全与恢复能力不得被功能开关灰度掉"；若要接线，必须先把"只灰度只读预览、执行/恢复始终可用"这条边界写清楚，否则会出现"悄悄关掉恢复入口"的风险；
   - `user.gauge_rollout`：它是**对照实验分组**开关（默认关闭、percentage=0，历史上被 ADMIN-010 用作对照），语义是"给一部分用户开某个实验"，而不是"关掉某个能力"。在没有明确实验对象前接线等于凭空造一个能力面。
2. §7.3 的要求（"功能开关必须在真实请求路径求值，不能只在后台页面试算"）**已经通过三条能力面满足**：`user.explorer_v2`、`admin.alert_center`、`admin.control_plane_v2`（ADR-021；验收套件 `run-sprint7-featureflags.sh` PASS 21/0）。机制已经存在并被真实流量验证，不存在"开关只是摆设"的问题。

**对验收矩阵的影响**：`ADMIN-010`（功能开关灰度）状态 **PASS**，证据为 `run-sprint7-featureflags.sh`（真实路径求值 + 评估记录）与后台功能开关页面的真实交互用例；矩阵中同时显式写明"另有 3 个开关未接线，属于范围外"，不得让读者以为 6 个开关全部生效。

## 3. 若后续要改这个决定

- OIDC 纳入范围：需要真实 IdP 的测试租户 + §27 AUTH-011 的 live 负向脚本 + 契约与手册同步；
- 三个开关接线：需要先写各自的语义（关掉之后的行为、是否属于恢复路径），再按 ADR-021 的方式接线并扩展 `featureflag_route_contract_test.go` 的清单。
