# ADR-022：案件证据 Tab 的临时双权限（case:read + evidence:list + investigation:list）

- 状态：**Accepted（temporary compatibility rule）** —— 保留技术债，最终形态待产品/数据层决策
- 日期：2026-09-19
- 关联：§0.4.2 权限契约、§24.3 无权限状态、ADR-003 权限命名与强制路由声明

## 背景

案件详情的 7 个子 Tab 原本只在渲染层按权限过滤，而路由 `/cases/:caseId/:tab` 只校验 `case:read`，
于是手输 URL / 收藏夹 / 浏览器历史都能绕过 UI 直达无权限子页面。

补齐 Tab 级路由守卫时发现一处**前后端权限语义不对齐**：

- 证据 Tab 当前实际调用的是 `GET /api/v2/investigations`（按 `case_id` 过滤枚举调查，再在这些调查下展示证据汇总）；
- 而该接口在 `openapi/user-v2.yaml` 声明的 `x-required-permission` 是 **`investigation:list`**；
- `evidence:list` 对应的是另一个接口 `GET /api/v2/investigations/{id}/evidence`。

因此"只要求 `evidence:list`"会让**持有 evidence:list 但无 investigation:list** 的用户顺利进入证据 Tab，
随后被后端 403 —— 即"进得去但用不了"。

## 决策

1. 证据 Tab 的准入权限为 **`case:read` + `evidence:list` + `investigation:list`**，多权限按 **AND** 组合（全部满足才放行）。
2. 该规则在**四处**同时生效，且共用同一份定义（`apps/user-console/src/app/case-tabs.ts`）：
   - 权限表本身（唯一事实来源）；
   - Route Guard（`app/CaseTabRoute.tsx`，在页面挂载前判定）；
   - UI Tab 过滤（`DeepTabs` 的 `permissions` 全满足才显示入口）；
   - 入口过滤（命令面板的搜索命中与最近访问、报告详情深链接，走 `canVisitCaseTabUrl`）。
3. 该规则在权限契约冻结中标记为 **`temporary: true`**，去掉标记会被 `check:contract` 判为失败 —— 防止它被无声地当成最终设计。

## 本次前端重构中**明确不做**的事

以下任何一项都超出前端重构范围，需要单独的产品/数据层决策，**不得**顺手实施：

- ❌ 删除 `investigation:list`（会立刻让证据 Tab 对合法用户 403）；
- ❌ 新增案件级 Evidence API（`/api/v2/cases/{caseId}/evidence`）—— 那是数据层改动；
- ❌ 修改后端权限语义或放宽服务端权限；
- ❌ 为了"避免 403"而放宽任何一层准入。

## 最终形态的两种可能（待决策）

| 若证据在业务上… | 则目标形态 | 需要的改动 |
| --- | --- | --- |
| **属于案件级资源** | 新增 `GET /api/v2/cases/{caseId}/evidence`，查看权限收敛为 `case:read` + `evidence:list` | 后端新增接口 + 前端切数据源 + 移除 `investigation:list` + 去掉 `temporary` |
| **始终隶属于 Investigation** | 当前双权限**就是**合理的最终模型 | 去掉 `temporary` 标记，并在本 ADR 记录决定 |

决策触发条件：确认案件级证据数据模型的归属之后。在那之前不得以"看起来更干净"为由改动它。


## 被否决的方案

| 方案 | 否决原因 |
| --- | --- |
| 证据 Tab 只要求 `evidence:list` | 持该权限但无 `investigation:list` 的用户会进入页面然后吃 403 —— "进得去但用不了"，比看不到更糟。 |
| 把证据 Tab 的 `case-tabs.ts` 权限改成 `investigation:list` 单权限 | 语义错误：证据业务本身属于 `evidence` 域，让 `investigation:list` 单独决定能不能看证据，会把"能看调查"误当成"能看证据"。 |
| 前端改调 `GET /api/v2/investigations/{id}/evidence` 以避免依赖 | 数据层重构：需要按调查逐个拉取并自行聚合，且改变现有证据页的展示口径，超出前端重构范围。 |
| 放宽后端让 `GET /api/v2/investigations` 接受 `evidence:list` | 直接破坏服务端权限语义，是本次重构明令禁止的。 |
| 前端捕获 403 后显示空态 | 把"权限不足"伪装成"没有数据"，掩盖真实原因，且不改变任何人能拿到什么。 |

## 回滚方式

这一条**本身就是"未做改动"的状态**：它只在前端准入层加严，没有改接口、没有改后端权限、没有迁移数据。
若要回滚到重构前的行为（证据 Tab 只按 `evidence:list` 显示）：

1. 把 `apps/user-console/src/app/case-tabs.ts` 中 evidence 条目的 `permissions` 改回 `["case:read", "evidence:list"]`；
2. 同步修改 `scripts/uc-contract-freeze-check.mjs` 里 `CASE_TAB_CONTRACT` 的对应条目（否则 `check:contract` 会红——这是刻意设计的摩擦）；
3. 保留 `temporary: true` 标记与两条测试（矩阵 + 联调规格）不变。

回滚**不会**影响任何已存储数据或后端行为。

## 影响

- 持 `evidence:list` 但无 `investigation:list` 的用户**看不到证据 Tab**。这比"看得见、点进去 403"更合理：能进入的人一定能取到数。
- 语义上略紧（`investigation:list` 是当前实现的偶然依赖，不是证据业务本身的权限），因此它是**技术债**而非设计意图，必须由本 ADR 显式承载。
- 权限契约冻结会在有人改动这张表时失败，失败信息会指出"权限被放宽"或"temporary 标记被改动"。

## 测试覆盖

`scripts/uc-v4-render-check.mjs` 的"案件 Tab 权限矩阵"对 7 个 Tab 表驱动地双向验证；证据 Tab 覆盖以下四条（拒绝方向）：

1. **Tab 不显示** —— 无任一必需权限时，Tab 栏里没有证据入口；
2. **直达 URL 被拦截** —— 出现无权限页而不是证据内容；
3. **页面不挂载** —— `CaseDetailPage` 未渲染（`.cs-tabs` 计数为 0）；
4. **相关 API 不请求** —— `/api/v2/investigations` 请求次数为 0。

真实后端联调另有 `tests/case-permissions.spec.ts`，对每个 Tab 额外做一次**绕过前端的直接调用**并要求后端返回 **403**
—— 那一条才是安全边界，前端两层只负责不给入口与页面。