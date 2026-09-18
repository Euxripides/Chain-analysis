import { useMemo } from "react";
import { encodeQr, qrPathData } from "./qr";

/**
 * QrCode 以**内联 SVG** 渲染二维码：零依赖、无网络资源、无额外 DOM 容器。
 *
 * 用途：MFA（TOTP）注册时需要把后端 ProvisioningURI 生成的 otpauth:// 链接
 * 交给认证器扫描（后端见 backend/internal/mfa/totp.go）。
 *
 * 渲染约定：
 * - viewBox 含四周各 4 个模块的静默区（ISO/IEC 18004 要求的最小值）；
 * - width/height 由 size 决定（默认 200），模块边缘用 crispEdges 对齐像素；
 * - 深色模块用 currentColor，跟随文字颜色；背景保持透明，不绘制底色矩形；
 * - value 为空或编码失败时返回 null，绝不抛异常打断页面渲染。
 */

/** 静默区宽度（模块数）。 */
const QUIET_ZONE = 4;

export interface QrCodeProps {
  /** 要编码的内容（byte 模式 / UTF-8），例如 otpauth:// 链接。 */
  value: string;
  /** 渲染边长（像素），默认 200。 */
  size?: number;
  /** 附加类名。 */
  className?: string;
  /** 无障碍标签，默认 "MFA 二维码"。 */
  label?: string;
}

export function QrCode(props: QrCodeProps) {
  const { value, size = 200, className, label } = props;

  const shape = useMemo(() => {
    if (!value) return null;
    try {
      const { size: moduleCount, modules } = encodeQr(value);
      return {
        path: qrPathData(modules, moduleCount, QUIET_ZONE),
        extent: moduleCount + QUIET_ZONE * 2,
      };
    } catch {
      // 编码失败（空内容或超出容量）时静默降级，由调用方决定是否弹窗提示
      return null;
    }
  }, [value]);

  if (!shape) return null;

  return (
    <svg
      className={className}
      width={size}
      height={size}
      viewBox={"0 0 " + shape.extent + " " + shape.extent}
      shapeRendering="crispEdges"
      role="img"
      aria-label={label ?? "MFA 二维码"}
    >
      <path d={shape.path} fill="currentColor" />
    </svg>
  );
}
