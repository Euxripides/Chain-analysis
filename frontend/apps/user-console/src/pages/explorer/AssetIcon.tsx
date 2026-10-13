import { useState } from "react";
import { Coins } from "lucide-react";

import { getChainLogo } from "../../../../../packages/chain-assets";

const verifiedTokenContracts: Record<string, string[]> = {
  bsc: ["0x55d398326f99059ff775485246999027b3197955", "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d"],
  eth: ["0xdac17f958d2ee523a2206206994597c13d831ec7", "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"],
};

function IconImage({ src, name, size }: { src: string; name: string; size: number }) {
  const [failed, setFailed] = useState(false);
  return <span className="cs-asset-icon" style={{ width: size, height: size }} title={name}>
    {!failed && src ? <img src={src} alt={name} width={size} height={size} loading="lazy" decoding="async" onError={() => setFailed(true)} /> : <Coins size={Math.max(14, size - 4)} aria-label={`${name} · 图标缺失`} />}
  </span>;
}

export function ChainIcon({ chain, size = 20 }: { chain: string; size?: number }) {
  const src = getChainLogo(chain) || "";
  return <IconImage key={src} src={src} name={chain} size={size} />;
}

export function TokenIcon({ chain, contract, symbol, size = 20 }: { chain: string; contract?: string | null; symbol?: string; size?: number }) {
  if (!contract) return <ChainIcon chain={chain} size={size} />;
  // Identity is chain + contract. Symbols never select a known coin's image.
  const canonicalChain = chain === "ethereum" ? "eth" : chain === "bnb" ? "bsc" : chain;
  const canonicalContract = canonicalChain === "eth" || canonicalChain === "bsc" ? contract.toLowerCase() : contract;
  const src = verifiedTokenContracts[canonicalChain]?.includes(canonicalContract)
    ? `${import.meta.env.BASE_URL}assets/tokens/${canonicalChain}/${canonicalContract}.png`
    : `/api/v2/explorer/tokens/${encodeURIComponent(chain)}/${encodeURIComponent(contract)}/logo`;
  return <IconImage key={src} src={src} name={symbol || contract} size={size} />;
}
