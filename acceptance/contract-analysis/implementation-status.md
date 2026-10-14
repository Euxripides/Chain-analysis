# 合约运行与方法分析实施记录

已发布至 https://aurore.online/app-v2/contract-analyses。公网 MCP 发布 119 个工具，新增 8 个合约分析工具。使用相同配置启动的 Codex 直连客户端加载 119 个工具，schema、描述与服务端差异为零，见 release/mcp-sync-check.json。本回合最初载入的工具快照仍为 111；该计数与新客户端的实际发现分别记录。

批准计划：docs/contract-analysis-development-plan.md。隔离部署基线为 850e795d642ba84ba8e52cffd7338ffb0fe58f51。原工作区及原有修改保留；原 API、MCP、用户端和下载 worker 的 PID 保持 3258429、3258435、2384809、1971802。

## 实际实现

固定区块与代码身份、精确源码重编译、AST、modifier/内部调用展开、WhatsABI/sevm/Heimdall、中文方法作用、条件、步骤、金额公式、状态、权限、跨方法依赖、代理解析、历史范围、交易 trace/prestate/diff、离线 Anvil 重放和资金核对已接入后端、MCP、SDK、工作台。

专用 worker 使用 lease/fence、输入冻结、持久预算和原子发布。完整分页、分区工件、授权文件块、逐块/整文件/ZIP 内文件 SHA256、JSON/CSV/Markdown/DOT/图数据导出已验收。网页有证据定位、深链接、取消/恢复、权限控制、桌面与移动布局。

WETH9 的 Deposit/Withdrawal 与 Transfer 共同核对账目；ERC20 直接余额映射按代码、布局及交易状态核对。ERC721/ERC1155 事件保留完整编号与数量。mapping 基槽不填写账户余额。失败祖先、delegate 重复 value、static call 和同地址 self-call 不计为独立原生币付款。

## 正式组件

WhatsABI 0.29.0、sevm 0.7.4、精确 Solidity 编译器包、Foundry/Anvil 1.8.5。Heimdall 上游 release 0.9.3 的发布二进制报告 0.9.2，两个值及 SHA256 分别保存。solc 0.8.1 npm 包的编译器身份不符，拒绝用于该版本。

正式镜像：sha256:2b54082289b8ad8c487bacd86ed3227f0c3d4510243774439586f57f8805e37a。自研模型/规则：contract-analysis-v1 / funds-semantics-1。

## 验收证据

| 检查 | 结果 | 文件 |
|---|---|---|
| 隔离镜像样本 | 24/24 | isolated-engine-benchmark.json |
| 本地 EVM 执行 | 53/53 | runtime-fixtures/report.json |
| 取消、崩溃、租约、fence、预算 | 21/21 | lifecycle-qa.json |
| timeout、OOM、超大输入、服务存活 | 4/4 | resource-fault-qa.json |
| 会话 API、全分页、作用域、ZIP | 41/41 | live-api-qa.json |
| 真实 MCP 与 resource | 12/12 | mcp-live-qa.json |
| 复杂交易、账目、证据引用 | 17/17 | execution-live-qa.json |
| 桌面 1440 / 移动 390 | 68/68 | browser/report.json |
| 既有页面与后台 Dialog | 通过，problems=[] | legacy-browser-regression/report.json |
| 既有路由、权限、API 契约 | 33/23/78 保持 | freeze 检查 |
| OAuth 授权、刷新、原撤销状态 | 8/8 | release-oauth-qa.json |
| 公网工具、生产任务、工件、费用 | 13/13 | release/production-qa.json |
| 生产数据库、ZIP、审计链、进程 | 8/8 | release/production-integrity-qa.json |
| 公网资源、登录要求、屏幕 | 10/10 | release/production-render-qa.json |
| Codex 新直连客户端 | 119/119，差异 0 | release/mcp-sync-check.json |

Go 全包检查通过。MCP 43 项测试及构建、引擎 11 项测试、前端 9 个 workspace 类型检查与用户端构建通过。

生产样本为 Ethereum WETH9：0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2，区块 19000000。任务 54cbd137-b857-41cb-9632-bdc76df55305 完成 12 个方法的分析，记录 11 次 RPC HTTP 请求、2200 credits，估算 0.00022 美元。

复杂交易样本：0xecdc668e16ada76f439b09c9b8c2faa7b76f80244933193c7691ac797e26eae7。成功状态、6 条日志、149853 Gas、5 个变化的槽与账户状态相符；WETH 转账及销毁后的两地址净账本相符。运行证据均能定位保存的原始工件。

## 发布与范围

生产应用增量迁移 0083。新增 API 仅监听 127.0.0.1:18084，feature-only 模式不启动旧后台循环；专用 worker 只领取新处理版本。MCP 18085 的旧工具请求原 API，OAuth 权威留在原进程。用户端 18086 保留 429 个旧资源。会话和 MCP/API 客户端的成功调用均写入任务审计，敏感读取单独标识。

公开 schema 为 https://aurore.online/contract-analysis-openapi.json；只有该规范副本通过静态文件公开。发布 manifest 保存二进制、资产、MCP、迁移、引擎和 nginx 哈希。

关闭新提交并保留查询：controlled_release.py disable_submissions。恢复旧入口：controlled_release.py rollback。回退保留任务、对象和增量表。

正式支持与未实现项见 docs/contract-analysis-capabilities.md。当前覆盖 EVM；TRON、Solana、Vyper、DOCX/XLSX、人工批注保存、通用符号证明未接入。未具备协议 adapter、历史覆盖、规则或 witness 的部分按维度保留缺口。未穷尽所有路径的任务保持 completed + partial。
