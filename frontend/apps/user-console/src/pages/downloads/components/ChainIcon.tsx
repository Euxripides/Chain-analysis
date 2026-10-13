import React from "react";
import { getChainLogo } from "../../../../../../packages/chain-assets";

/** Local chain brand assets; unknown chains keep the initial-letter fallback. */
export function chainHasLogo(chain: string): boolean {
  return Boolean(getChainLogo(chain));
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
  const normalized = chain.trim().toLowerCase();
  const src = getChainLogo(normalized);
  if (src) {
    return (
      <span
        className={`${classes} cs-chainicon--logo`}
        data-chain={normalized}
        aria-hidden="true"
        style={{ width: size, height: size }}
      >
        <img
          className="cs-chainicon__image"
          src={src}
          alt=""
          width={size}
          height={size}
          loading="eager"
          decoding="async"
        />
      </span>
    );
  }
  const initial = (/[a-z0-9]/i.exec(normalized)?.[0] ?? "?").toUpperCase();
  return (
    <span
      className={`${classes} cs-chainicon--fallback`}
      data-chain={normalized}
      aria-hidden="true"
      style={{ width: size, height: size, fontSize: Math.max(9, Math.round(size * 0.46)) }}
    >
      {initial}
    </span>
  );
}

export default ChainIcon;
