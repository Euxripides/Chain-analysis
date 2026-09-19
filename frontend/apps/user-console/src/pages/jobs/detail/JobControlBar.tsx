import { Button } from "@chain/ui";
import { ChevronRight } from "lucide-react";

/**
 * 控制条（用户端重构规范 V4 §28.7 / §28.1）
 *
 * 业务行为**完全未改**：pause / resume / cancel / retry 仍然走原来的
 * `/api/v2/jobs/{id}/commands/{command}`，仍然要求填写审计原因，
 * 仍然由服务端确认后才是真实状态（前端不做乐观"已取消"，§28.7）。
 *
 * 表现层变化只有两点：
 *   1. 每个按钮各自 busy（服务端的 pending 状态决定 disabled），不整块转圈（§28.6）；
 *   2. 原来的 8 个技术 Tab 首屏导航消失，改成右下角的"查看技术详情 ›"二级入口（§28.3）。
 */

export interface JobControlBarProps {
  canPause: boolean;
  canResume: boolean;
  canCancel: boolean;
  canRetry: boolean;
  busy: boolean;
  onCommand: (command: "pause" | "resume" | "cancel" | "retry") => void;
  /** 打开技术详情（Drawer），默认关闭。 */
  onOpenTechnical: () => void;
  /** 完成后的下载入口；没有可下载导出时为 undefined。 */
  downloadAction?: React.ReactNode;
}

export function JobControlBar({
  canPause,
  canResume,
  canCancel,
  canRetry,
  busy,
  onCommand,
  onOpenTechnical,
  downloadAction,
}: JobControlBarProps) {
  return (
    <div className="toolbar" data-testid="job-control-bar" style={{ flexWrap: "wrap", gap: "var(--sp-2)" }}>
      <Button size="sm" disabled={!canPause || busy} onClick={() => onCommand("pause")}>
        暂停
      </Button>
      <Button size="sm" disabled={!canResume || busy} onClick={() => onCommand("resume")}>
        继续
      </Button>
      <Button size="sm" disabled={!canRetry || busy} onClick={() => onCommand("retry")}>
        重试失败分片
      </Button>
      <Button size="sm" variant="danger" disabled={!canCancel || busy} onClick={() => onCommand("cancel")}>
        取消任务
      </Button>
      <span style={{ flex: 1, minWidth: 8 }} />
      {downloadAction}
      <Button size="sm" variant="ghost" onClick={onOpenTechnical}>
        查看技术详情
        <ChevronRight size={14} aria-hidden="true" />
      </Button>
    </div>
  );
}
