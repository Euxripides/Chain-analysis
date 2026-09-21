/**
 * 下载通道工具：Ticket 优先 + IPv6 直连探测 + IPv4/Tunnel 自动回退。
 * - 大文件绝不 fetch 进 JS 内存（改用浏览器导航下载）。
 * - Ticket URL 仅用于当前下载动作，不写入 localStorage / 长时状态。
 */
import { api } from '../api/client'

/** 直连 IPv6 下载域名（DNS only，独立 origin） */
// 2026-09-19：家宽入站 80/443 被运营商封锁，下载通道改走非标端口 8443（证书与 SNI 无关，无需重签）
export const DIRECT_DOWNLOAD_ORIGIN = 'https://download.aurore.online:8443'
/** IPv6 可达性探测超时（毫秒） */
export const IPV6_PROBE_TIMEOUT_MS = 1200

export interface DownloadStart {
  url: string
  /** 是否走 download.aurore.online 直连 */
  viaDirect: boolean
  /** 实际采用的通道 */
  channel: 'direct' | 'fallback'
  ticketId?: string
}

/** 主站 origin 的兼容下载 URL（Tunnel fallback，保留原直链形态） */
export function fallbackDownloadUrl(exportId: string): string {
  return window.location.origin + '/api/v1/exports/' + encodeURIComponent(exportId) + '/download'
}

/** 轻量探测直连域是否可达（CORS 仅放行主站 origin；失败/超时均视为不可用） */
export async function canUseDirectDownload(timeoutMs: number = IPV6_PROBE_TIMEOUT_MS): Promise<boolean> {
  const controller = new AbortController()
  const timer = window.setTimeout(() => controller.abort(), timeoutMs)
  try {
    const res = await fetch(DIRECT_DOWNLOAD_ORIGIN + '/download-health', {
      method: 'GET',
      mode: 'cors',
      cache: 'no-store',
      signal: controller.signal,
    })
    return res.status === 204
  } catch {
    return false
  } finally {
    window.clearTimeout(timer)
  }
}

/**
 * Ticket 优先下载：
 *   1) POST /api/v1/exports/{id}/download-ticket 获取 15 分钟票据；
 *   2) IPv6 直连可达 → https://download.aurore.online/api/v1/download/{ticket}；
 *   3) 不可达/无票据 → aurore.online 原直链（Tunnel fallback）。
 * 返回后由调用方触发浏览器导航（不 fetch 大文件）。
 */
export async function startExportDownload(exportId: string): Promise<DownloadStart> {
  let ticketId: string | undefined
  try {
    const resp = await api.exportTicket(exportId)
    ticketId = resp.ticket_id
  } catch {
    // 票据不可用（导出异常/后端拒绝）→ 直接走兼容下载
  }

  if (ticketId && (await canUseDirectDownload())) {
    return {
      url: DIRECT_DOWNLOAD_ORIGIN + '/api/v1/download/' + encodeURIComponent(ticketId),
      viaDirect: true,
      channel: 'direct',
      ticketId,
    }
  }
  return { url: fallbackDownloadUrl(exportId), viaDirect: false, channel: 'fallback', ticketId }
}

/** 触发浏览器下载（锚点点击：不占用 JS 内存，不触发弹窗拦截） */
export function triggerBrowserDownload(url: string): void {
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.rel = 'noopener noreferrer'
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
}
