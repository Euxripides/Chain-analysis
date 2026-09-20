/**
 * 路由入口薄壳。
 *
 * 页面实现按《新建数据下载 Flowline 界面重构实施规范》§19 拆到 ./downloads/：
 *   downloads/DownloadNewPage.tsx         状态、API、预览指纹、创建编排、Dialog 调用
 *   downloads/DownloadFlowRail.tsx        Flowline 数据轨道（横向 / 竖向）
 *   downloads/DownloadAddressPanel.tsx    ① 对象与地址
 *   downloads/DownloadScopePanel.tsx      ② 链与范围
 *   downloads/DownloadDatasetGrid.tsx     ③ Dataset
 *   downloads/DownloadCoveragePanel.tsx   ④ Coverage 与成本预览
 *   downloads/DownloadTaskPreview.tsx     ⑤ 任务预览与创建入口
 *   downloads/DownloadMobileActionBar.tsx 移动端底部安全区操作栏
 *   downloads/download.types.ts           协议类型、常量与纯函数（含 DEFAULT_REUSE_POLICY）
 *   downloads/download.motion.ts          页面动效令牌与 variants
 *   downloads/download-new.css            页面作用域样式（令牌限制在 .dl-flow-page）
 *
 * 保留这个文件是为了让 App.tsx 的路由懒加载路径保持字面量不变
 * （路由与 API 字面量受 uc-contract-freeze-check 冻结）。
 */
export { default } from "./downloads/DownloadNewPage";
