import type { DatasetMeta } from "./download.types";

/**
 * 数据类型的一段话说明（UI 文案，不是接口数据）。
 *
 * 《新建数据下载页面重构规范》§16 的卡片示例带描述行，而
 * `/api/v2/meta/datasets` 只返回 `{ id, label, status }` —— 没有描述字段。
 * 因此这里按 **dataset id** 维护一份固定的界面文案：接口有就说，没有就不说，
 * 绝不为未知 dataset 编造解释。新增 dataset 时同步补一行即可。
 */
const DATASET_COPY: Record<string, string> = {
  transactions: "支持 ETH 转账、合约交互等",
  token_transfers: "ERC20、TRC20、SPL 等",
  nft_transfers: "ERC721、ERC1155 等",
  logs: "合约事件 Event Logs",
  balances: "每个区块的余额变化",
  internal_transactions: "合约内部调用记录",
};

export function datasetDescription(id: string): string | undefined {
  return DATASET_COPY[id];
}

/** 服务端返回的 status 决定可用性；只有 available 可选（与重构前一致）。 */
export function datasetSelectable(item: DatasetMeta): boolean {
  return item.status === "available";
}

/**
 * 不可用原因（挂在 title / aria-label 上，**不作为页面内联说明段落**）。
 *
 * 只陈述服务端返回的事实：status 与来源（provider / api_channel）。
 * 不写"为什么这条链没有这个 Dataset"——那属于服务端口径，前端不猜。
 */
export function datasetUnavailableReason(item: DatasetMeta): string | null {
  if (datasetSelectable(item)) return null;
  const source = [item.provider, item.api_channel].filter(Boolean).join(" · ");
  if (item.status === "unsupported") {
    return source
      ? "此 Dataset 在当前链上没有可用来源（服务端能力表 unsupported · " + source + "）"
      : "此 Dataset 在当前链上没有可用来源（服务端能力表 unsupported）";
  }
  return "服务端未把此 Dataset 标记为 available，不可勾选";
}
