/**
 * 地址 → 链族推断（用户端三处数据工具页面共用：地址与交易查询 / 导出中心 / 新建数据下载）。
 *
 * 为什么必须有这一层：这三个页面的链选择器默认选中的是链注册表里的**第一条链**
 * （当前是 arbitrum）。用户粘贴 TRON / Solana 地址时请求仍按 arbitrum 发出，后果各不相同
 * 但都是"功能用不了"：
 *   - 地址与交易查询：/explorer/addresses/arbitrum/T… → 400「EVM 地址不合法」，
 *     地址详情页显示"概览数据不可用 / 加载失败"；
 *   - 新建数据下载：/downloads/validate-addresses 判 INVALID_ADDRESS → 整批被拒，
 *     而链选择在第 ② 步（第 ① 步不通过就到不了第 ② 步），用户被卡死在第一步；
 *   - 导出中心：以 arbitrum 建 TRON 地址的导出 → 查的是另一条链的数据，结果为空。
 *
 * 地址形状本身是**可判定**的（0x+40hex / T+33 Base58 / 32–44 Base58），所以这里把它
 * 做成纯函数：页面在用户输入的那一刻就把链切到匹配的那条，而不是等服务端拒绝之后
 * 让人自己去猜。多族混合时不猜（交由逐条链处理）。
 *
 * 纯逻辑：无 React、无 DOM、无网络，可被页面与单测直接调用。
 */

export type AddressChainFamily = "evm" | "tron" | "solana";

/** EVM：0x + 40 位十六进制。 */
const EVM_ADDRESS = /^0x[0-9a-fA-F]{40}$/;
/** 省略 0x 的 40 位十六进制：按 EVM 处理（与 Base58 在极少数串上重叠，EVM 更常见）。 */
const BARE_HEX_ADDRESS = /^[0-9a-fA-F]{40}$/;
/** TRON：T + 33 位 Base58。 */
const TRON_ADDRESS = /^T[1-9A-HJ-NP-Za-km-z]{33}$/;
/** Solana：32–44 位 Base58（大小写敏感；0 / O / I / l 不属于字母表）。 */
const BASE58_ADDRESS = /^[1-9A-HJ-NP-Za-km-z]{32,44}$/;

/**
 * 单个地址的链族；认不出来（空行、交易哈希、非法形状）返回 ""。
 * 返回值只表达"形状属于哪一族"，不表达"这条链是启用的"。
 */
export function addressChainFamily(address: string): AddressChainFamily | "" {
  const value = address.trim();
  if (value === "") return "";
  if (EVM_ADDRESS.test(value) || BARE_HEX_ADDRESS.test(value)) return "evm";
  if (TRON_ADDRESS.test(value)) return "tron";
  if (BASE58_ADDRESS.test(value)) return "solana";
  return "";
}

/** 链 slug → 族。链注册表里只有 tron / solana 不是 EVM（与后端 chain 注册表同构）。 */
export function chainFamilyOf(slug: string): AddressChainFamily | "" {
  const value = slug.trim().toLowerCase();
  if (value === "") return "";
  if (value === "tron") return "tron";
  if (value === "solana") return "solana";
  return "evm";
}

/**
 * 一批地址的**唯一**族：全部可识别地址同族才返回该族；混合、或一个都认不出时返回 ""。
 * 混合族不能拿单条默认链硬套，必须逐条指定链（服务端 validate-addresses 支持逐条 chain）。
 */
export function unanimousAddressFamily(addresses: readonly string[]): AddressChainFamily | "" {
  let family: AddressChainFamily | "" = "";
  for (const address of addresses) {
    const current = addressChainFamily(address);
    if (current === "") continue;
    if (family === "") {
      family = current;
    } else if (family !== current) {
      return "";
    }
  }
  return family;
}

/**
 * 在**已启用**链里挑第一条与族匹配的链（按注册表顺序）。找不到（链被停用 / 未登记）返回 ""，
 * 调用方保持原选择，让服务端给出可解释的拒绝原因，而不是静默改到一条同样不能用的链。
 */
export function chainForFamily<T extends { slug: string; enabled: boolean }>(
  chains: readonly T[],
  family: AddressChainFamily | "",
): string {
  if (family === "") return "";
  const hit = chains.find((item) => item.enabled && chainFamilyOf(item.slug) === family);
  return hit ? hit.slug : "";
}

/** 族的用户可读名称（Dialog 文案用）。 */
export function familyLabel(family: AddressChainFamily): string {
  switch (family) {
    case "evm":
      return "EVM 链（bsc / eth / arbitrum 等）";
    case "tron":
      return "TRON";
    case "solana":
      return "Solana";
  }
}
