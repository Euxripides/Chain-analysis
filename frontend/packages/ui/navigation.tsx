import React, { useCallback, useEffect, useId, useRef, useState } from "react";
import { ArrowDownUp, Check, ChevronLeft, ChevronRight, ListFilter, X } from "lucide-react";
import { cx } from "./layout";

/**
 * 导航与筛选组件（蓝图 §8.1 / §8.2 / §8.4）
 *
 * Tabs / SegmentedControl / FilterChip / FilterBar / SortMenu / Pagination / Stepper。
 *
 * 键盘契约（§8.1 "支持键盘左右切换、深链接和明确选中态"）：
 *   - Tabs 用 roving tabindex，左右/Home/End 移动焦点并切换；
 *   - 选中态用 `aria-selected` + 下边框 + 字重，三重表达，不只靠颜色（§14）。
 */

export interface TabItem {
  key: string;
  label: React.ReactNode;
  count?: number;
  disabled?: boolean;
}

/**
 * 受控 Tabs。选中值由调用方持有（通常写到 URL，保证刷新后保持当前 tab，§12.6）。
 */
export function Tabs({
  items,
  value,
  onChange,
  ariaLabel = "标签页",
  className,
}: {
  items: TabItem[];
  value: string;
  onChange: (key: string) => void;
  ariaLabel?: string;
  className?: string;
}) {
  const listRef = useRef<HTMLDivElement>(null);

  const move = useCallback(
    (direction: 1 | -1) => {
      const usable = items.filter((item) => !item.disabled);
      if (usable.length === 0) return;
      const index = usable.findIndex((item) => item.key === value);
      const next = usable[(index + direction + usable.length) % usable.length];
      onChange(next.key);
      /* 焦点随之移动：roving tabindex 下，选中项就是唯一可 Tab 进入的项。 */
      window.requestAnimationFrame(() => {
        listRef.current?.querySelector<HTMLButtonElement>('[data-tab-key="' + next.key + '"]')?.focus();
      });
    },
    [items, onChange, value],
  );

  return (
    <div className={cx("cs-tabs", className)} role="tablist" aria-label={ariaLabel} ref={listRef}>
      {items.map((item) => {
        const selected = item.key === value;
        return (
          <button
            key={item.key}
            type="button"
            role="tab"
            data-tab-key={item.key}
            aria-selected={selected}
            aria-controls={"tabpanel-" + item.key}
            id={"tab-" + item.key}
            tabIndex={selected ? 0 : -1}
            disabled={item.disabled}
            className="cs-tab"
            onClick={() => onChange(item.key)}
            onKeyDown={(event) => {
              if (event.key === "ArrowRight") {
                event.preventDefault();
                move(1);
              } else if (event.key === "ArrowLeft") {
                event.preventDefault();
                move(-1);
              } else if (event.key === "Home") {
                event.preventDefault();
                const first = items.find((i) => !i.disabled);
                if (first) onChange(first.key);
              } else if (event.key === "End") {
                event.preventDefault();
                const last = [...items].reverse().find((i) => !i.disabled);
                if (last) onChange(last.key);
              }
            }}
          >
            {item.label}
            {item.count !== undefined ? <span className="cs-tab__count">{item.count}</span> : null}
          </button>
        );
      })}
    </div>
  );
}

/** 与 Tabs 配套的面板容器，保证 aria-controls 指向真实存在的节点。 */
export function TabPanel({ tabKey, children }: { tabKey: string; children: React.ReactNode }) {
  return (
    <div role="tabpanel" id={"tabpanel-" + tabKey} aria-labelledby={"tab-" + tabKey} tabIndex={0}>
      {children}
    </div>
  );
}

/** 分段控制器：用于 2–4 个互斥视图切换，不做导航。 */
export function SegmentedControl({
  items,
  value,
  onChange,
  ariaLabel,
}: {
  items: Array<{ key: string; label: React.ReactNode }>;
  value: string;
  onChange: (key: string) => void;
  ariaLabel: string;
}) {
  return (
    <div className="cs-segmented" role="group" aria-label={ariaLabel}>
      {items.map((item) => (
        <button
          key={item.key}
          type="button"
          className="cs-segmented__item"
          aria-pressed={item.key === value}
          onClick={() => onChange(item.key)}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}

/** 可切换筛选 chip。`onRemove` 存在时渲染为可删除的已应用筛选。 */
export function FilterChip({
  children,
  active,
  onClick,
  onRemove,
  removeLabel,
}: {
  children: React.ReactNode;
  active?: boolean;
  onClick?: () => void;
  onRemove?: () => void;
  removeLabel?: string;
}) {
  if (onRemove) {
    return (
      <span className="cs-chip" data-static="true">
        {children}
        <button type="button" className="cs-chip__x" aria-label={removeLabel ?? "移除筛选"} onClick={onRemove} style={{ border: 0, background: "transparent", cursor: "pointer", color: "inherit", padding: 0 }}>
          <X size={12} />
        </button>
      </span>
    );
  }
  return (
    <button type="button" className="cs-chip" aria-pressed={Boolean(active)} onClick={onClick}>
      {active ? <Check size={12} aria-hidden="true" /> : null}
      {children}
    </button>
  );
}

/**
 * 筛选栏容器：保留筛选控件本身，即使结果为空也不清空（§11.4）。
 */
export function FilterBar({
  children,
  onClear,
  clearLabel = "清除筛选",
  showClear,
  ariaLabel = "筛选条件",
}: {
  children: React.ReactNode;
  onClear?: () => void;
  clearLabel?: string;
  showClear?: boolean;
  ariaLabel?: string;
}) {
  return (
    <div className="cs-filterbar" role="group" aria-label={ariaLabel}>
      <ListFilter size={15} aria-hidden="true" style={{ color: "var(--text-secondary)", flex: "none" }} />
      {children}
      <span className="cs-filterbar__spacer" />
      {onClear && showClear ? (
        <button type="button" className="btn btn--ghost btn--sm" onClick={onClear}>
          {clearLabel}
        </button>
      ) : null}
    </div>
  );
}

export interface SortOption {
  key: string;
  label: string;
}

/** 排序菜单。只暴露接口真正支持的排序字段，不伪造能力。 */
export function SortMenu({
  options,
  value,
  onChange,
  direction,
  onDirectionChange,
  ariaLabel = "排序",
}: {
  options: SortOption[];
  value: string;
  onChange: (key: string) => void;
  direction: "asc" | "desc";
  onDirectionChange: (next: "asc" | "desc") => void;
  ariaLabel?: string;
}) {
  const id = useId();
  return (
    <span className="toolbar">
      <label className="cs-section__count" htmlFor={id}>
        排序
      </label>
      <select id={id} className="select" value={value} onChange={(event) => onChange(event.target.value)} aria-label={ariaLabel}>
        {options.map((option) => (
          <option key={option.key} value={option.key}>
            {option.label}
          </option>
        ))}
      </select>
      <button
        type="button"
        className="btn btn--sm btn--ghost"
        aria-label={direction === "asc" ? "当前升序，切换为降序" : "当前降序，切换为升序"}
        onClick={() => onDirectionChange(direction === "asc" ? "desc" : "asc")}
      >
        <ArrowDownUp size={14} />
        {direction === "asc" ? "升序" : "降序"}
      </button>
    </span>
  );
}

/**
 * 分页（§8.4）。
 *
 * 只负责显示与切换页码；**数据获取方式（cursor / offset / 服务端分页）由调用方决定**，
 * 组件不猜测后端协议。
 */
export function Pagination({
  page,
  pageCount,
  totalLabel,
  onPage,
  busy,
}: {
  /** 1 基页码。 */
  page: number;
  pageCount: number;
  totalLabel?: React.ReactNode;
  onPage: (page: number) => void;
  busy?: boolean;
}) {
  if (pageCount <= 1 && !totalLabel) return null;
  return (
    <div className="toolbar" style={{ padding: "var(--sp-3) var(--sp-4)", justifyContent: "space-between", flexWrap: "wrap" }}>
      <span className="cs-section__count" data-testid="table-summary">
        {totalLabel}
        {totalLabel && pageCount > 1 ? " · " : ""}
        {pageCount > 1 ? "第 " + page + " / " + pageCount + " 页" : null}
      </span>
      {pageCount > 1 ? (
        <span className="toolbar">
          <button
            type="button"
            className="btn btn--sm"
            disabled={busy || page <= 1}
            aria-label="上一页"
            onClick={() => onPage(page - 1)}
          >
            <ChevronLeft size={14} />
            上一页
          </button>
          <button
            type="button"
            className="btn btn--sm"
            disabled={busy || page >= pageCount}
            aria-label="下一页"
            onClick={() => onPage(page + 1)}
          >
            下一页
            <ChevronRight size={14} />
          </button>
        </span>
      ) : null}
    </div>
  );
}

export interface StepItem {
  key: string;
  label: string;
}

/**
 * 步进器（§12.8 发起调查 / §12.17 新建下载）。
 * 表达真实填写顺序，允许返回已完成步骤；未到达的步骤不可点击。
 */
export function Stepper({
  steps,
  current,
  onJump,
  ariaLabel = "步骤",
}: {
  steps: StepItem[];
  current: string;
  onJump?: (key: string) => void;
  ariaLabel?: string;
}) {
  const currentIndex = steps.findIndex((step) => step.key === current);
  return (
    <ol className="cs-stepper" aria-label={ariaLabel}>
      {steps.map((step, index) => {
        const state = index === currentIndex ? "current" : index < currentIndex ? "done" : "todo";
        return (
          <React.Fragment key={step.key}>
            {index > 0 ? <li className="cs-stepper__sep" aria-hidden="true">/</li> : null}
            <li>
              <button
                type="button"
                className="cs-stepper__step"
                data-state={state}
                aria-current={state === "current" ? "step" : undefined}
                disabled={state === "todo" || !onJump}
                onClick={() => onJump?.(step.key)}
              >
                <span className="cs-stepper__index">{state === "done" ? <Check size={11} strokeWidth={3} /> : index + 1}</span>
                {step.label}
              </button>
            </li>
          </React.Fragment>
        );
      })}
    </ol>
  );
}

/**
 * 批量选择上下文操作栏（§8.4）。
 * 未选择时不占空间——由调用方在 `count === 0` 时不渲染本组件。
 */
export function BulkActionBar({
  count,
  children,
  onClear,
}: {
  count: number;
  children: React.ReactNode;
  onClear?: () => void;
}) {
  if (count <= 0) return null;
  return (
    <div className="cs-bulkbar" role="region" aria-label="批量操作">
      <span className="cs-bulkbar__count">已选择 {count} 项</span>
      {onClear ? (
        <button type="button" className="btn btn--sm btn--ghost" onClick={onClear}>
          取消选择
        </button>
      ) : null}
      <span className="cs-filterbar__spacer" />
      {children}
    </div>
  );
}

/** 行级选择复选框，带可读名称（§14 icon-only 控件必须有 aria-label）。 */
export function RowCheckbox({
  checked,
  onChange,
  label,
  disabled,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  label: string;
  disabled?: boolean;
}) {
  return (
    <input
      type="checkbox"
      checked={checked}
      disabled={disabled}
      aria-label={label}
      onChange={(event) => onChange(event.target.checked)}
      style={{ width: 16, height: 16, cursor: disabled ? "not-allowed" : "pointer" }}
      onClick={(event) => event.stopPropagation()}
    />
  );
}

/**
 * 全选表头复选框：支持 indeterminate 三态。
 * 原生 input 的 indeterminate 只能通过 DOM 属性设置，这里用 ref 同步。
 */
export function SelectAllCheckbox({
  checked,
  indeterminate,
  onChange,
  label = "全选当前页",
}: {
  checked: boolean;
  indeterminate?: boolean;
  onChange: (next: boolean) => void;
  label?: string;
}) {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = Boolean(indeterminate) && !checked;
  }, [checked, indeterminate]);
  return (
    <input
      ref={ref}
      type="checkbox"
      checked={checked}
      aria-label={label}
      onChange={(event) => onChange(event.target.checked)}
      style={{ width: 16, height: 16, cursor: "pointer" }}
    />
  );
}

/** 轻量受控开关（§8.2 Switch）。 */
export function Switch({
  checked,
  onChange,
  label,
  disabled,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  label: string;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      className="cs-switch"
      data-on={checked ? "true" : "false"}
      onClick={() => onChange(!checked)}
    >
      <span className="cs-switch__knob" />
    </button>
  );
}

/** 复选框（原生 input，保留 label 关联与键盘行为）。 */
export function Checkbox({
  checked,
  onChange,
  label,
  disabled,
  id,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  label: React.ReactNode;
  disabled?: boolean;
  id?: string;
}) {
  const auto = useId();
  const inputId = id ?? auto;
  return (
    <label htmlFor={inputId} style={{ display: "inline-flex", alignItems: "center", gap: "var(--sp-2)", cursor: disabled ? "not-allowed" : "pointer", minHeight: 24 }}>
      <input
        id={inputId}
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        style={{ width: 16, height: 16 }}
      />
      <span style={{ fontSize: "var(--fs-13)" }}>{label}</span>
    </label>
  );
}

/** 单选组（§8.2 Radio）：真实 radio input 组，方向键可切换。 */
export function RadioGroup<T extends string>({
  name,
  options,
  value,
  onChange,
  ariaLabel,
}: {
  name: string;
  options: Array<{ value: T; label: React.ReactNode; disabled?: boolean }>;
  value: T | null;
  onChange: (next: T) => void;
  ariaLabel: string;
}) {
  return (
    <div role="radiogroup" aria-label={ariaLabel} style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}>
      {options.map((option) => (
        <label
          key={option.value}
          style={{ display: "inline-flex", alignItems: "center", gap: "var(--sp-2)", cursor: option.disabled ? "not-allowed" : "pointer", minHeight: 24 }}
        >
          <input
            type="radio"
            name={name}
            value={option.value}
            checked={value === option.value}
            disabled={option.disabled}
            onChange={() => onChange(option.value)}
            style={{ width: 16, height: 16 }}
          />
          <span style={{ fontSize: "var(--fs-13)" }}>{option.label}</span>
        </label>
      ))}
    </div>
  );
}
