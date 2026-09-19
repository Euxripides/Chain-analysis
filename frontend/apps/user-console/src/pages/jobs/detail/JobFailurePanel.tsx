import { Button } from "@chain/ui";
import { CsStatusTag } from "@chain/user-ui";
import { CircleAlert } from "lucide-react";

/**
 * 失败态主卡（用户端重构规范 V4 §28.8）
 *
 * 失败首屏只有三样东西：一句安全文案、重试、查看技术详情。
 *
 * 明确不做：**不把整个页面染红**，**不默认展开大量日志**。
 * trace id、Shard 错误、事件字段全部留在技术详情里按需查看。
 * 这里展示的 `safeMessage` 直接来自服务端 `layers.job.error_safe`，
 * 前端不加工、不推断、不拼接后端 stack。
 */

export interface JobFailurePanelProps {
  safeMessage: string;
  canRetry: boolean;
  busy: boolean;
  onRetry: () => void;
  onOpenTechnical: () => void;
}

export function JobFailurePanel({ safeMessage, canRetry, busy, onRetry, onOpenTechnical }: JobFailurePanelProps) {
  return (
    <section className="cs-surface cs-jobprogress" data-state="failed" data-testid="job-failure-panel">
      <div style={{ display: "flex", alignItems: "center", gap: "var(--sp-3)", flexWrap: "wrap" }}>
        <span
          style={{
            display: "grid",
            placeItems: "center",
            width: 34,
            height: 34,
            borderRadius: 11,
            background: "var(--cs-danger-soft)",
            color: "var(--cs-danger-text)",
          }}
          aria-hidden="true"
        >
          <CircleAlert size={18} strokeWidth={2.2} />
        </span>
        <span className="cs-panelcard__title">下载失败</span>
        <CsStatusTag tone="danger">FAILED</CsStatusTag>
      </div>
      <p style={{ margin: 0, fontSize: "var(--fs-13)", color: "var(--cs-text-secondary)", lineHeight: 1.6 }}>
        {safeMessage || "服务端未提供可展示的失败说明。"}
      </p>
      <div className="toolbar" style={{ flexWrap: "wrap", gap: "var(--sp-2)" }}>
        <Button size="sm" variant="primary" disabled={!canRetry || busy} onClick={onRetry}>
          重试
        </Button>
        <Button size="sm" variant="ghost" onClick={onOpenTechnical}>
          查看技术详情
        </Button>
      </div>
    </section>
  );
}
