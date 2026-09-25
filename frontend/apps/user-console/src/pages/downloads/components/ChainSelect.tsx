import React from "react";
import { Select } from "antd";
import { ChainIcon } from "./ChainIcon";

/**
 * 链选择器（《新建数据下载页面重构规范》§10 / §12）
 *
 * 统一走 Ant Design Select：可搜索、有 optionRender/labelRender、有 loading/empty、
 * 支持键盘导航、popup 由 Ant 统一挂载。**页面里不再出现任何原生 <select>**。
 *
 * 选项数据继续来自 /api/v2/meta/chains（未硬编码任何链），slug 作为提交值。
 */

export interface ChainOption {
  /** 提交值：chain slug。 */
  value: string;
  /** 展示名：服务端返回的链名。 */
  name: string;
  /** 是否可用（服务端 enabled）。不可用时在选项里禁用，但仍可见。 */
  enabled: boolean;
}

/**
 * 单行链选项：固定图标槽位 + 唯一可见名称。
 *
 * slug 只用于提交和搜索，永远不进入可见标签。服务端的 name 已经是面向用户的完整名称，
 * 再拼 slug 会产生 "BNB Smart Chainbsc"、"OP Mainnetoptimism" 等错误结果。
 */
export function ChainOptionRow({
  chain,
  name,
  enabled = true,
  size = 24,
}: {
  chain: string;
  name: string;
  enabled?: boolean;
  size?: number;
}) {
  return (
    <span className="cs-chainoption" data-chain={chain} data-disabled={enabled ? undefined : "true"}>
      <ChainIcon chain={chain} size={size} />
      <span className="cs-chainoption__text">
        <span className="cs-chainoption__name">{name}</span>
      </span>
      {enabled ? null : <span className="cs-chainoption__off">不可用</span>}
    </span>
  );
}

export interface ChainSelectProps {
  id?: string;
  value: string;
  options: ChainOption[];
  onChange: (next: string) => void;
  loading?: boolean;
  disabled?: boolean;
  ariaLabel?: string;
  /** 字段级错误（校验失败时由父级传入）。 */
  invalid?: boolean;
  /** 默认 360px；窄屏页面可传 true，使弹层跟随触发器宽度。 */
  popupMatchSelectWidth?: boolean | number;
}

export function ChainSelect({
  id,
  value,
  options,
  onChange,
  loading = false,
  disabled = false,
  ariaLabel = "网络",
  invalid = false,
  popupMatchSelectWidth = 360,
}: ChainSelectProps) {
  const items = options.map((item) => ({
    value: item.value,
    label: item.name,
    name: item.name,
    enabled: item.enabled,
  }));

  return (
    <Select<string>
      id={id}
      className="cs-chainselect"
      aria-label={ariaLabel}
      status={invalid ? "error" : undefined}
      value={value || undefined}
      onChange={onChange}
      options={items}
      loading={loading}
      disabled={disabled || loading}
      showSearch
      allowClear={false}
      placeholder="选择网络"
      /* 统一 popup 容器：下拉是 Portal，用同一个类名统一外观（规范 §10）。 */
      classNames={{ popup: { root: "cs-select-dropdown-wizard" } }}
      popupMatchSelectWidth={popupMatchSelectWidth}
      listHeight={320}
      /* 搜索同时匹配链名与 slug（服务端 slug 是提交值，用户可能直接敲 tron）。 */
      filterOption={(input, option) => {
        const query = input.trim().toLowerCase();
        if (!query) return true;
        const data = option as { label?: string; value?: string; enabled?: boolean } | undefined;
        return (
          String(data?.label ?? "").toLowerCase().includes(query) ||
          String(data?.value ?? "").toLowerCase().includes(query)
        );
      }}
      optionRender={(option) => {
        const data = option.data as { value: string; name: string; enabled: boolean };
        return <ChainOptionRow chain={data.value} name={data.name} enabled={data.enabled} />;
      }}
      labelRender={(props) => {
        const slug = String(props.value ?? "");
        const matched = options.find((item) => item.value === slug);
        if (!matched) return <span className="cs-chainoption__name">{props.label}</span>;
        return <ChainOptionRow chain={matched.value} name={matched.name} size={20} />;
      }}
      notFoundContent={
        <span className="cs-chainselect__empty">{loading ? "正在加载网络…" : "没有匹配的网络"}</span>
      }
    />
  );
}

export default ChainSelect;
