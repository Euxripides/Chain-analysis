#!/usr/bin/env node
/**
 * 地址 → 链族推断的回归检查（用户端三处数据工具页面共用的 app/chain-address.ts）。
 *
 * 它守着的是一个真实故障（2026-09-21 现场）：
 *   三个页面的链选择器默认选中链注册表的第一条链（arbitrum）。用户粘贴 TRON / Solana 地址时：
 *     - 地址与交易查询 → /explorer/addresses/arbitrum/T… → 400「EVM 地址不合法」；
 *     - 新建数据下载   → validate-addresses 判 INVALID_ADDRESS，第 ① 步过不去，
 *                        而链选择在第 ② 步（到不了），用户被卡死；
 *     - 导出中心       → 以 arbitrum 建 TRON 地址的导出，结果为空。
 *   修法是"输入时按地址形状切链"，因此这里把形状判定与选链规则钉成表驱动断言。
 *
 * 纯函数检查，不需要浏览器与后端：node scripts/chain-address-check.mjs
 * 退出码 0 = 全部通过；1 = 有断言失败。
 */
import {
  addressChainFamily,
  chainFamilyOf,
  chainForFamily,
  familyLabel,
  unanimousAddressFamily,
} from "../apps/user-console/src/app/chain-address.ts";

const problems = [];
const notes = [];

function eq(actual, expected, label) {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) problems.push(label + "：期望 " + e + "，实际 " + a);
}

/* ---- 形状判定：真实地址样本 ---- */
const TRON = "TJJGcsf3VaBDoL55rp2QMGRuwYQtPscwsV";
const TRON_2 = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t";
const TRON_3 = "TRjzoaBzjVoy3WiLEhTVWjcLuBD5zKDH4G";
const EVM = "0x769614a8b0d62b337c116b37632e1afc3468a692";
const EVM_UPPER = "0x769614A8B0D62B337C116B37632E1AFC3468A692";
const SOLANA = "5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9";
const SOLANA_2 = "Vote111111111111111111111111111111111111111";
const TX_HASH = "0x13c94c2be35538c344a1be194de83b32afad347694225e9bd74d1a483d91fe7f";

for (const [value, expected] of [
  [TRON, "tron"],
  [TRON_2, "tron"],
  [TRON_3, "tron"],
  [EVM, "evm"],
  [EVM_UPPER, "evm"],
  [EVM.slice(2), "evm"],
  [SOLANA, "solana"],
  [SOLANA_2, "solana"],
  ["", ""],
  ["   ", ""],
  [TX_HASH, ""],
  ["not-an-address", ""],
  ["0x1234", ""],
  /*
   * 已知的形状重叠：TRON 是 T + 33 位 Base58（34 字符），而 Solana 是 32–44 位 Base58，
   * 因此"少一位的 TRON 地址"在形状上就是一条合法的 Solana 地址，只能按 Solana 归类。
   * 完整 TRON 地址永远先命中 TRON 规则（见上面的 3 个样本）。
   */
  ["TJJGcsf3VaBDoL55rp2QMGRuwYQtPscws", "solana"],
]) {
  eq(addressChainFamily(value), expected, "addressChainFamily(" + JSON.stringify(value.slice(0, 16)) + "…)");
}

/* ---- 链 slug → 族：注册表里只有 tron / solana 不是 EVM ---- */
for (const [slug, expected] of [
  ["tron", "tron"],
  ["TRON", "tron"],
  ["solana", "solana"],
  ["bsc", "evm"],
  ["eth", "evm"],
  ["ethereum", "evm"],
  ["arbitrum", "evm"],
  ["polygon", "evm"],
  ["", ""],
]) {
  eq(chainFamilyOf(slug), expected, "chainFamilyOf(" + JSON.stringify(slug) + ")");
}

/* ---- 唯一族：同族给族、混族不猜、全不可识别给空 ---- */
eq(unanimousAddressFamily([TRON, TRON_2, TRON_3]), "tron", "同族 TRON");
eq(unanimousAddressFamily([EVM, EVM_UPPER]), "evm", "同族 EVM");
eq(unanimousAddressFamily([SOLANA]), "solana", "同族 Solana");
eq(unanimousAddressFamily([TRON, EVM]), "", "混族必须返回空（不能拿单链硬套）");
eq(unanimousAddressFamily([TRON, "bad", EVM]), "", "混族 + 非法串仍返回空");
eq(unanimousAddressFamily([TRON, "bad"]), "tron", "同族 + 非法串：族仍然确定");
eq(unanimousAddressFamily(["bad", ""]), "", "全不可识别返回空");
eq(unanimousAddressFamily([]), "", "空集合返回空");

/* ---- 选链：只从**已启用**链里挑，且必须挑到同族 ---- */
const CHAINS = [
  { slug: "arbitrum", enabled: true },
  { slug: "bsc", enabled: true },
  { slug: "tron", enabled: true },
  { slug: "solana", enabled: false },
  { slug: "eth", enabled: true },
];
eq(chainForFamily(CHAINS, "tron"), "tron", "TRON 族应选到 tron");
eq(chainForFamily(CHAINS, "evm"), "arbitrum", "EVM 族按注册表顺序取第一条");
eq(chainForFamily(CHAINS, "solana"), "", "链被停用时不返回它（交给服务端报错）");
eq(chainForFamily(CHAINS, ""), "", "无族不选链");
eq(chainForFamily([], "tron"), "", "空链表不选链");

/*
 * 现场回归：默认链 = 注册表第一条（arbitrum），地址 = TRON。
 * 修复前页面会带着 arbitrum 提交（400 / INVALID_ADDRESS / 空导出）；
 * 修复后必须解析出 tron —— 这就是本检查要钉住的因果链。
 */
const defaultChain = CHAINS.find((item) => item.enabled).slug;
const pasted = [TRON, TRON_2];
const family = unanimousAddressFamily(pasted);
eq(defaultChain, "arbitrum", "前提：默认链是注册表第一条");
eq(chainFamilyOf(defaultChain) === family, false, "前提：默认链与粘贴地址不同族");
eq(chainForFamily(CHAINS, family), "tron", "粘贴 TRON 地址后必须切到 tron");

notes.push("地址族样本 " + [TRON, EVM, SOLANA].map((v) => addressChainFamily(v)).join(" / "));
notes.push("族名称：" + ["evm", "tron", "solana"].map((f) => familyLabel(f)).join(" · "));

const result = { ok: problems.length === 0, problems, notes };
console.log(JSON.stringify(result, null, 2));
if (problems.length > 0) process.exitCode = 1;
