// OKLink 外链（文档 §20：集中配置，禁止各组件自拼 URL）
const OKLINK_CHAIN_SLUG: Record<string, string> = {
  bsc: 'bsc',
  eth: 'ethereum',
  ethereum: 'ethereum',
  base: 'base',
  arbitrum: 'arbitrum-one',
  polygon: 'polygon',
  optimism: 'optimism',
}

export function buildOklinkTxUrl(chain: string, txHash: string): string | null {
  const slug = OKLINK_CHAIN_SLUG[chain]
  if (!slug || !txHash) return null
  return 'https://www.oklink.com/' + slug + '/tx/' + txHash
}

export function shortHash(h: string): string {
  if (!h) return ''
  if (h.length <= 16) return h
  return h.slice(0, 8) + '…' + h.slice(-8)
}
