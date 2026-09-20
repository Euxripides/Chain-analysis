import React from "react";
import ethereumLogo from "../../../assets/chains/ethereum.png";
import bscLogo from "../../../assets/chains/bsc.png";
import arbitrumLogo from "../../../assets/chains/arbitrum.png";
import optimismLogo from "../../../assets/chains/optimism.png";
import baseLogo from "../../../assets/chains/base.png";
import polygonLogo from "../../../assets/chains/polygon.png";
import solanaLogo from "../../../assets/chains/solana.png";
import avalancheLogo from "../../../assets/chains/avalanche.png";
import tronLogo from "../../../assets/chains/tron.png";

/**
 * 链图标（《新建数据下载页面重构规范》§11）
 *
 * 用**官方链标识**（与公网一致），全部是仓库内的本地资源：
 *   - 不远程热链（规范 §11 明令禁止）；
 *   - 来源与授权见 ../../../assets/chains/README.md（Trust Wallet Assets，MIT）；
 *   - Vite 会把它们作为独立资源输出，随下载页的懒加载 chunk 一起取，不进首屏包。
 *
 * 未登记的链**不猜图标**：回落为"圆形底色 + 链名称首字母"（规范 §11 的兜底规则）。
 */

/** 键 = 服务端 chain slug（/api/v2/meta/chains）。 */
const CHAIN_LOGOS: Record<string, string> = {
  ethereum: ethereumLogo,
  bsc: bscLogo,
  arbitrum: arbitrumLogo,
  optimism: optimismLogo,
  base: baseLogo,
  polygon: polygonLogo,
  solana: solanaLogo,
  avalanche: avalancheLogo,
  tron: tronLogo,
};

export function chainHasLogo(chain: string): boolean {
  return Boolean(CHAIN_LOGOS[chain]);
}

export interface ChainIconProps {
  /** 服务端 chain slug。 */
  chain: string;
  /** 图标边长（px）。默认 22；选择器/卡片 22–24，摘要里 18–20。 */
  size?: number;
  className?: string;
}

/**
 * 图标是**装饰**：链名始终以文字出现在它旁边，因此对读屏隐藏，避免重复朗读（规范 §25）。
 */
export function ChainIcon({ chain, size = 22, className }: ChainIconProps) {
  const classes = className ? `cs-chainicon ${className}` : "cs-chainicon";
  const src = CHAIN_LOGOS[chain];
  if (src) {
    return (
      <img
        className={`${classes} cs-chainicon--logo`}
        src={src}
        alt=""
        aria-hidden="true"
        width={size}
        height={size}
        style={{ width: size, height: size }}
        loading="lazy"
        decoding="async"
      />
    );
  }
  const initial = (/[a-z0-9]/i.exec(chain)?.[0] ?? "?").toUpperCase();
  return (
    <span
      className={`${classes} cs-chainicon--fallback`}
      aria-hidden="true"
      style={{ width: size, height: size, fontSize: Math.max(9, Math.round(size * 0.46)) }}
    >
      {initial}
    </span>
  );
}

export default ChainIcon;
