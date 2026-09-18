/**
 * 最近访问对象（蓝图 §5.3"打开最近访问对象"）
 *
 * 纯本地视觉偏好：记录用户**自己导航过**的对象路径与短标签，
 * 不缓存任何业务字段，也不作为任何请求的参数来源。
 * 服务端数据仍然只由各自页面按权限实时拉取。
 *
 * 存储上限 12 条，写入失败（隐私模式）不影响功能。
 */

const KEY = "chainsight.user.recent-objects.v1";
const LIMIT = 12;

export interface RecentObject {
  path: string;
  label: string;
  kind: string;
  at: number;
}

export function readRecentObjects(): RecentObject[] {
  try {
    const raw = window.localStorage.getItem(KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter((item): item is RecentObject => {
        if (!item || typeof item !== "object") return false;
        const candidate = item as Partial<RecentObject>;
        return typeof candidate.path === "string" && typeof candidate.label === "string";
      })
      .slice(0, LIMIT);
  } catch {
    return [];
  }
}

export function pushRecentObject(entry: Omit<RecentObject, "at">): void {
  try {
    const next = [
      { ...entry, at: Date.now() },
      ...readRecentObjects().filter((item) => item.path !== entry.path),
    ].slice(0, LIMIT);
    window.localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    /* 存储不可用时静默降级：最近对象只是加速入口，不是功能本身。 */
  }
}

/** 按当前路径推断对象类型与短标签，供 shell 在路由变化时登记。 */
export function describeRoute(pathname: string): { kind: string; label: string } | null {
  const segments = pathname.split("/").filter(Boolean);
  if (segments.length < 2) return null;
  const [head, id] = segments;
  const short = id.length > 18 ? id.slice(0, 10) + "…" + id.slice(-6) : id;
  switch (head) {
    case "cases":
      return { kind: "案件", label: "案件 " + short };
    case "investigations":
      return { kind: "调查", label: "调查 " + short };
    case "addresses":
      return { kind: "地址", label: short };
    case "transactions":
      return { kind: "交易", label: short };
    case "jobs":
      return { kind: "任务", label: "任务 " + short };
    case "reports":
      return { kind: "报告", label: "报告 " + short };
    case "canvases":
      return { kind: "图谱", label: "Canvas " + short };
    default:
      return null;
  }
}
