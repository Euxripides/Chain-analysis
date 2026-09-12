// Sprint 6 后台控制台的共享展示组件（A-* 页面共用）。
//
// 这些组件只承载业务数据、字段标签与操作控件；任何解释性文案必须通过 useDialog() 弹窗呈现
// （AGENTS.md / docs/code-standards.md / AD-009）。

import { useId, useState, type ReactNode } from "react";

// ---- Tabs ----

export interface TabItem {
  key: string;
  label: string;
  content: ReactNode;
  disabled?: boolean;
}

/** Tabs 使用原生 button + aria 语义，支持键盘左右切换与 URL 无关的本地状态。 */
export function Tabs({ items, initialKey, ariaLabel }: { items: TabItem[]; initialKey?: string; ariaLabel: string }) {
  const [active, setActive] = useState(initialKey ?? items[0]?.key ?? "");
  const baseId = useId();
  const current = items.find((item) => item.key === active) ?? items[0];
  return (
    <div>
      <div className="toolbar" role="tablist" aria-label={ariaLabel} style={{ flexWrap: "wrap" }}>
        {items.map((item) => {
          const selected = item.key === current?.key;
          return (
            <button
              key={item.key}
              type="button"
              role="tab"
              id={baseId + "-tab-" + item.key}
              aria-selected={selected}
              aria-controls={baseId + "-panel-" + item.key}
              tabIndex={selected ? 0 : -1}
              disabled={item.disabled}
              className={"btn btn--sm" + (selected ? " btn--primary" : "")}
              onClick={() => setActive(item.key)}
              onKeyDown={(event) => {
                if (event.key !== "ArrowRight" && event.key !== "ArrowLeft") return;
                event.preventDefault();
                const index = items.findIndex((candidate) => candidate.key === current?.key);
                const delta = event.key === "ArrowRight" ? 1 : -1;
                const next = items[(index + delta + items.length) % items.length];
                if (next && !next.disabled) setActive(next.key);
              }}
            >
              {item.label}
            </button>
          );
        })}
      </div>
      {current ? (
        <div role="tabpanel" id={baseId + "-panel-" + current.key} aria-labelledby={baseId + "-tab-" + current.key} tabIndex={0}>
          {current.content}
        </div>
      ) : null}
    </div>
  );
}

// ---- 键值明细 ----

export function KeyValueList({ items, columns = 2 }: { items: Array<{ label: string; value: ReactNode; mono?: boolean }>; columns?: number }) {
  return (
    <dl
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fit, minmax(" + (columns >= 2 ? 220 : 320) + "px, 1fr))",
        gap: "var(--sp-3) var(--sp-5)",
        margin: 0,
      }}
    >
      {items.map((item) => (
        <div key={item.label}>
          <dt style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>{item.label}</dt>
          <dd className={item.mono ? "mono" : undefined} style={{ margin: "2px 0 0", wordBreak: "break-word" }}>
            {item.value === null || item.value === undefined || item.value === "" ? "—" : item.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

// ---- 时间线 ----

export interface TimelineEntry {
  id: string;
  title: ReactNode;
  meta?: ReactNode;
  body?: ReactNode;
  tone?: "ok" | "warn" | "danger" | "info" | "neutral";
}

export function Timeline({ entries, emptyLabel }: { entries: TimelineEntry[]; emptyLabel: string }) {
  if (entries.length === 0) {
    return <p style={{ color: "var(--c-slate)", margin: 0 }}>{emptyLabel}</p>;
  }
  return (
    <ol style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}>
      {entries.map((entry) => (
        <li key={entry.id} style={{ borderLeft: "2px solid var(--c-border)", paddingLeft: "var(--sp-3)" }}>
          <div style={{ display: "flex", justifyContent: "space-between", gap: "var(--sp-3)", flexWrap: "wrap" }}>
            <strong style={{ fontWeight: 600 }}>{entry.title}</strong>
            {entry.meta ? <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>{entry.meta}</span> : null}
          </div>
          {entry.body ? <div style={{ marginTop: "var(--sp-1)", fontSize: "var(--fs-13)" }}>{entry.body}</div> : null}
        </li>
      ))}
    </ol>
  );
}

// ---- 摘要化密钥 ----

export function MaskedSecret({ prefix, fingerprint, rotatedAt, lastUsedAt, secretRef }: {
  prefix?: string;
  fingerprint?: string;
  rotatedAt?: ReactNode;
  lastUsedAt?: ReactNode;
  secretRef?: string;
}) {
  return (
    <KeyValueList
      items={[
        { label: "前缀（前 6 位）", value: prefix ? prefix + "••••••" : "—", mono: true },
        { label: "SHA-256 指纹", value: fingerprint ?? "—", mono: true },
        { label: "引用名（摘要）", value: secretRef ?? "—", mono: true },
        { label: "轮换时间", value: rotatedAt ?? "—" },
        { label: "最后使用", value: lastUsedAt ?? "—" },
        { label: "明文回显", value: "永不提供" },
      ]}
    />
  );
}

// ---- 前后对比 ----

export function DiffList({ before, after, labels }: {
  before: Record<string, unknown>;
  after: Record<string, unknown>;
  labels?: Record<string, string>;
}) {
  const keys = Array.from(new Set([...Object.keys(before ?? {}), ...Object.keys(after ?? {})])).sort();
  return (
    <dl style={{ display: "grid", gridTemplateColumns: "minmax(140px, 1fr) minmax(120px, 1.2fr) minmax(120px, 1.2fr)", gap: "var(--sp-2) var(--sp-3)", margin: 0 }}>
      <dt style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>字段</dt>
      <dt style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>变更前</dt>
      <dt style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>变更后</dt>
      {keys.map((key) => {
        const left = renderValue(before?.[key]);
        const right = renderValue(after?.[key]);
        const changed = left !== right;
        return (
          <div key={key} style={{ display: "contents" }}>
            <dt>{labels?.[key] ?? key}</dt>
            <dd className="mono" style={{ margin: 0, color: changed ? "var(--c-danger)" : undefined }}>{left}</dd>
            <dd className="mono" style={{ margin: 0, color: changed ? "var(--c-green)" : undefined }}>{right}</dd>
          </div>
        );
      })}
    </dl>
  );
}

function renderValue(value: unknown): string {
  if (value === null || value === undefined) return "—";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

// ---- JSON 折叠块 ----

export function JsonBlock({ value, label }: { value: unknown; label: string }) {
  if (value === null || value === undefined) return <p style={{ color: "var(--c-slate)", margin: 0 }}>—</p>;
  return (
    <details>
      <summary style={{ cursor: "pointer" }}>{label}</summary>
      <pre className="mono" style={{ overflowX: "auto", margin: "var(--sp-2) 0 0", fontSize: "var(--fs-12)" }}>
        {JSON.stringify(value, null, 2)}
      </pre>
    </details>
  );
}

// ---- 受控输入 ----

export function TextInput({ id, label, value, onChange, placeholder, type = "text", hint }: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  type?: string;
  hint?: string;
}) {
  return (
    <div style={{ flex: "1 1 180px", minWidth: 0 }}>
      <label className="field__label" htmlFor={id}>{label}</label>
      <input id={id} className="input" type={type} value={value} placeholder={placeholder} onChange={(event) => onChange(event.target.value)} />
      {hint ? <span className="field__hint">{hint}</span> : null}
    </div>
  );
}

export function SelectInput({ id, label, value, onChange, options }: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: Array<{ value: string; label: string }>;
}) {
  return (
    <div style={{ flex: "1 1 180px", minWidth: 0 }}>
      <label className="field__label" htmlFor={id}>{label}</label>
      <select id={id} className="select" value={value} onChange={(event) => onChange(event.target.value)}>
        {options.map((option) => (
          <option key={option.value} value={option.value}>{option.label}</option>
        ))}
      </select>
    </div>
  );
}

export function NumberInput({ id, label, value, onChange, min, max, step }: {
  id: string;
  label: string;
  value: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  step?: number;
}) {
  return (
    <div style={{ flex: "1 1 160px", minWidth: 0 }}>
      <label className="field__label" htmlFor={id}>{label}</label>
      <input
        id={id}
        className="input"
        type="number"
        value={Number.isFinite(value) ? value : 0}
        min={min}
        max={max}
        step={step}
        onChange={(event) => onChange(Number(event.target.value))}
      />
    </div>
  );
}

export function CheckboxRow({ id, label, checked, onChange, hint }: {
  id: string;
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  hint?: string;
}) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: "var(--sp-2)" }}>
      <input id={id} type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} />
      <label htmlFor={id}>{label}</label>
      {hint ? <span className="field__hint">{hint}</span> : null}
    </div>
  );
}
