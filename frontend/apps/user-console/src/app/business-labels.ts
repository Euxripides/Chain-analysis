const JOB_STATES: Record<string, string> = {
  CREATED: "已创建", PLANNING: "规划中", QUEUED: "排队中", RUNNING: "运行中",
  WAITING_USER_CONFIRMATION: "等待确认", PAUSE_REQUESTED: "正在暂停", PAUSED: "已暂停",
  PAUSED_FALLBACK_TOO_EXPENSIVE: "因费用限制暂停", RESUMING: "正在恢复", CANCEL_REQUESTED: "正在取消",
  COMPLETED: "已完成", FAILED: "失败", PARTIAL_FAILED: "部分失败", CANCELLED: "已取消", ABORTED_BY_USER: "用户已终止",
};
const DATASETS: Record<string, string> = {
  transactions: "普通交易", token_transfers: "代币转账", internal_transactions: "内部转账",
  native_transfers: "原生币转账", logs: "事件日志", traces: "交易追踪",
};
const CASE_STATES: Record<string, string> = { active: "进行中", under_review: "复核中", draft: "草稿", closed: "已结案", archived: "已归档" };
export const caseStatusLabel = (value: string): string => CASE_STATES[value.toLowerCase()] ?? value;
export const jobStatusLabel = (value: string): string => JOB_STATES[value.toUpperCase()] ?? value;
export const datasetLabel = (value: string): string => DATASETS[value] ?? value;
const REUSE_POLICIES: Record<string, string> = { prefer_local: "优先复用本地", prefer_reuse: "优先复用", local_only: "仅使用本地", refresh: "重新下载" };
const PERFORMANCE_MODES: Record<string, string> = { safe: "稳妥", fast: "快速", turbo: "极速", auto: "自动", provider_pull: "数据服务下载" };
export const reusePolicyLabel = (value: string): string => REUSE_POLICIES[value] ?? value;
export const performanceModeLabel = (value: string): string => PERFORMANCE_MODES[value.toLowerCase()] ?? value;
