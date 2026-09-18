/**
 * 直连下载通道（download.aurore.online，DNS only、独立 origin）。
 *
 * 2026-09-19：家宽入站 80/443 被运营商封锁（实测 9998/8443 可达、80/443 超时），
 * 因此直连通道改用非标端口 8443；证书按 SNI 签发、与端口无关，无需重签。
 *
 * 设计：导出票据与 v1 消费端点共用同一存储（backend/internal/store/meta_v2.go 的
 * CreateExportTicket / GetExportTicket），因此控制台签发的 v2 票据可直接在直连通道的
 * GET /api/v1/download/{ticket} 消费。探测失败（无 IPv6 / 路由不可达）一律回退主站隧道。
 */

/** 直连下载域名（含端口） */
export const DIRECT_DOWNLOAD_ORIGIN = "https://download.aurore.online:8443";

/** 可达性探测超时（毫秒）——超时即视为不可用并回退 */
export const DIRECT_DOWNLOAD_PROBE_TIMEOUT_MS = 1200;

/** 直连通道的票据下载 URL */
export function directTicketUrl(ticketId: string): string {
  return DIRECT_DOWNLOAD_ORIGIN + "/api/v1/download/" + encodeURIComponent(ticketId);
}

/**
 * 轻量探测直连通道是否可达：/download-health 返回 204（CORS 仅放行主站 origin）。
 * 任何异常、超时或非 204 都返回 false，由调用方回退到主站隧道 URL。
 */
export async function canUseDirectDownload(
  timeoutMs: number = DIRECT_DOWNLOAD_PROBE_TIMEOUT_MS,
): Promise<boolean> {
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(DIRECT_DOWNLOAD_ORIGIN + "/download-health", {
      method: "GET",
      mode: "cors",
      cache: "no-store",
      signal: controller.signal,
    });
    return res.status === 204;
  } catch {
    return false;
  } finally {
    window.clearTimeout(timer);
  }
}
