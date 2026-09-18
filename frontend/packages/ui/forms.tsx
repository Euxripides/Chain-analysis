import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { Eye, EyeOff, Loader, Search, UploadCloud, X } from "lucide-react";
import { cx } from "./layout";

/**
 * 基础交互组件（蓝图 §8.2）
 *
 * Button / IconButton / SearchInput / PasswordInput / Combobox / MultiSelect /
 * DateTimeRange / BlockRange / FileDropzone / ChainSelector / DatasetSelector。
 *
 * 控件高度契约（§8.2）：紧凑 32px、默认 40px、移动触摸目标至少 44×44px，
 * 由 .btn / .input 的 CSS 令牌保证；**关键操作不能只靠 hover 暴露**。
 */

type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "icon";

export interface ButtonProps extends Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "className"> {
  variant?: ButtonVariant;
  size?: "sm" | "md";
  /**
   * busy：保留原宽度 + spinner + 动词（§10.4"创建中…"）。
   * 只表示"请求已发出"，**不在前端假定服务端成功**。
   */
  busy?: boolean;
  busyLabel?: string;
  icon?: React.ReactNode;
  className?: string;
}

export function Button({
  variant = "secondary",
  size = "md",
  busy = false,
  busyLabel,
  icon,
  children,
  className,
  disabled,
  type = "button",
  ...rest
}: ButtonProps) {
  const variantClass =
    variant === "primary"
      ? "btn--primary"
      : variant === "danger"
        ? "btn--danger"
        : variant === "ghost"
          ? "btn--ghost"
          : variant === "icon"
            ? "btn--ghost"
            : "";
  return (
    <button
      type={type}
      className={cx("btn", variantClass, size === "sm" && "btn--sm", busy && "btn--busy", className)}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      {...rest}
    >
      {busy ? <span className="btn__spinner" aria-hidden="true" /> : icon}
      {busy && busyLabel ? busyLabel : children}
    </button>
  );
}

/** 图标按钮：**必须有 aria-label 和 tooltip**（§8.2 / §14）。 */
export function IconButton({
  label,
  icon,
  onClick,
  disabled,
  busy,
  active,
  className,
  ...rest
}: {
  label: string;
  icon: React.ReactNode;
  onClick?: () => void;
  disabled?: boolean;
  busy?: boolean;
  active?: boolean;
  className?: string;
} & Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "className" | "onClick" | "aria-label">) {
  return (
    <button
      type="button"
      className={cx("cs-iconbtn", className)}
      aria-label={label}
      title={label}
      aria-expanded={active === undefined ? undefined : active}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      onClick={onClick}
      {...rest}
    >
      {busy ? <Loader size={16} className="cs-spin" aria-hidden="true" /> : icon}
    </button>
  );
}

export interface SearchInputProps {
  value: string;
  onChange: (next: string) => void;
  onSubmit?: (value: string) => void;
  label: string;
  placeholder?: string;
  /** 最小可提交字符数；不足时不清除输入、不报错，只是不触发检索（§12.3）。 */
  minLength?: number;
  autoFocus?: boolean;
  big?: boolean;
  busy?: boolean;
  className?: string;
  id?: string;
}

/**
 * 搜索输入。Enter 提交、Escape 清空；**不在此处做防抖**——
 * 防抖属于数据层（TansStack Query / useAsync），组件只保证输入行为正确。
 */
export function SearchInput({
  value,
  onChange,
  onSubmit,
  label,
  placeholder,
  minLength = 0,
  autoFocus,
  big,
  busy,
  className,
  id,
}: SearchInputProps) {
  const auto = useId();
  const inputId = id ?? auto;
  return (
    <div className={cx("cs-searchfield", className)}>
      <Search size={big ? 18 : 15} aria-hidden="true" className="cs-searchfield__icon" />
      <input
        id={inputId}
        className={cx("input", "cs-searchfield__input", big && "cs-searchfield__input--big")}
        type="search"
        role="searchbox"
        aria-label={label}
        placeholder={placeholder}
        value={value}
        autoFocus={autoFocus}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            event.preventDefault();
            const trimmed = value.trim();
            if (trimmed.length >= minLength) onSubmit?.(trimmed);
          } else if (event.key === "Escape" && value) {
            event.preventDefault();
            onChange("");
          }
        }}
      />
      {busy ? <Loader size={15} className="cs-spin cs-searchfield__busy" aria-hidden="true" /> : null}
      {!busy && value ? (
        <button type="button" className="cs-searchfield__clear" aria-label="清空搜索" onClick={() => onChange("")}>
          <X size={14} />
        </button>
      ) : null}
    </div>
  );
}

/** 密码输入：可见性切换按钮有 aria-label 与 aria-pressed（§12.1）。 */
export function PasswordInput({
  value,
  onChange,
  label,
  id,
  autoComplete = "current-password",
  disabled,
  placeholder,
  required,
  onEnter,
}: {
  value: string;
  onChange: (next: string) => void;
  label: string;
  id?: string;
  autoComplete?: string;
  disabled?: boolean;
  placeholder?: string;
  required?: boolean;
  onEnter?: () => void;
}) {
  const auto = useId();
  const inputId = id ?? auto;
  const [visible, setVisible] = useState(false);
  return (
    <div className="cs-passwordfield">
      <input
        id={inputId}
        className="input"
        type={visible ? "text" : "password"}
        aria-label={label}
        autoComplete={autoComplete}
        placeholder={placeholder}
        required={required}
        disabled={disabled}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter") onEnter?.();
        }}
      />
      <button
        type="button"
        className="cs-passwordfield__toggle"
        aria-label={visible ? "隐藏密码" : "显示密码"}
        aria-pressed={visible}
        onClick={() => setVisible((v) => !v)}
      >
        {visible ? <EyeOff size={15} /> : <Eye size={15} />}
      </button>
    </div>
  );
}

export interface ComboOption {
  value: string;
  label: string;
  hint?: string;
  disabled?: boolean;
}

/**
 * 可搜索单选下拉（§8.2 Combobox）。
 *
 * 空查询时显示全部选项，输入时按 label 过滤（本地过滤，不发请求）；
 * 上下键移动、Enter 选择、Escape 关闭并还原。
 */
export function Combobox({
  options,
  value,
  onChange,
  label,
  placeholder = "请选择",
  disabled,
  emptyText = "没有匹配项",
  id,
}: {
  options: ComboOption[];
  value: string | null;
  onChange: (next: string) => void;
  label: string;
  placeholder?: string;
  disabled?: boolean;
  emptyText?: string;
  id?: string;
}) {
  const auto = useId();
  const listId = (id ?? auto) + "-list";
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const boxRef = useRef<HTMLDivElement>(null);
  const selected = options.find((option) => option.value === value);
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return options;
    return options.filter((option) => (option.label + " " + (option.hint ?? "")).toLowerCase().includes(q));
  }, [options, query]);

  useEffect(() => {
    if (!open) return;
    const onDocDown = (event: MouseEvent) => {
      if (!boxRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDocDown);
    return () => document.removeEventListener("mousedown", onDocDown);
  }, [open]);

  const commit = (option: ComboOption | undefined) => {
    if (!option || option.disabled) return;
    onChange(option.value);
    setOpen(false);
    setQuery("");
  };

  return (
    <div className="cs-combo" ref={boxRef}>
      <button
        type="button"
        id={id ?? auto}
        className={cx("input", "cs-combo__trigger")}
        role="combobox"
        aria-expanded={open}
        aria-controls={listId}
        aria-haspopup="listbox"
        aria-label={label}
        disabled={disabled}
        onClick={() => setOpen((v) => !v)}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" || event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            setOpen(true);
          }
        }}
      >
        <span className={cx("cs-combo__value", !selected && "cs-combo__value--empty")}>
          {selected ? selected.label : placeholder}
        </span>
      </button>
      {open ? (
        <div className="cs-combo__pop">
          <input
            className="input cs-combo__search"
            aria-label={label + " 搜索"}
            placeholder="输入以过滤"
            value={query}
            autoFocus
            onChange={(event) => {
              setQuery(event.target.value);
              setActive(0);
            }}
            onKeyDown={(event) => {
              if (event.key === "ArrowDown") {
                event.preventDefault();
                setActive((i) => Math.min(filtered.length - 1, i + 1));
              } else if (event.key === "ArrowUp") {
                event.preventDefault();
                setActive((i) => Math.max(0, i - 1));
              } else if (event.key === "Enter") {
                event.preventDefault();
                commit(filtered[active]);
              } else if (event.key === "Escape") {
                event.preventDefault();
                setOpen(false);
              }
            }}
          />
          <ul className="cs-combo__list" id={listId} role="listbox" aria-label={label}>
            {filtered.length === 0 ? <li className="cs-combo__empty">{emptyText}</li> : null}
            {filtered.map((option, index) => (
              <li key={option.value}>
                <button
                  type="button"
                  role="option"
                  aria-selected={option.value === value}
                  className="cs-combo__option"
                  data-active={index === active ? "true" : undefined}
                  disabled={option.disabled}
                  onMouseEnter={() => setActive(index)}
                  onClick={() => commit(option)}
                >
                  <span>{option.label}</span>
                  {option.hint ? <span className="cs-combo__hint">{option.hint}</span> : null}
                </button>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}

/** 多选（§8.2 MultiSelect）：值以数组表达，顺序即选择顺序。 */
export function MultiSelect({
  options,
  values,
  onChange,
  label,
  max,
  emptyText = "没有可选项",
}: {
  options: ComboOption[];
  values: string[];
  onChange: (next: string[]) => void;
  label: string;
  /** 服务端上限（例如"最多 6 个 Dataset"，§12.17）。达到上限后未选项禁用。 */
  max?: number;
  emptyText?: string;
}) {
  const full = typeof max === "number" && values.length >= max;
  return (
    <div className="cs-multiselect" role="group" aria-label={label}>
      {options.length === 0 ? <span className="cs-section__count">{emptyText}</span> : null}
      {options.map((option) => {
        const checked = values.includes(option.value);
        return (
          <label key={option.value} className="cs-multiselect__item" data-checked={checked ? "true" : "false"}>
            <input
              type="checkbox"
              checked={checked}
              disabled={option.disabled || (!checked && full)}
              onChange={(event) => {
                if (event.target.checked) onChange([...values, option.value]);
                else onChange(values.filter((v) => v !== option.value));
              }}
            />
            <span>{option.label}</span>
            {option.hint ? <span className="cs-combo__hint">{option.hint}</span> : null}
          </label>
        );
      })}
    </div>
  );
}

/**
 * 时间范围（§8.2 DateTimeRange）。
 * 只做输入与格式校验，"开始不得晚于结束"的判定由调用方按接口规则给出。
 */
export function DateTimeRange({
  from,
  to,
  onFromChange,
  onToChange,
  label,
  disabled,
}: {
  from: string;
  to: string;
  onFromChange: (next: string) => void;
  onToChange: (next: string) => void;
  label: string;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <div className="cs-range" role="group" aria-label={label}>
      <label className="cs-range__field">
        <span className="cs-range__label">开始</span>
        <input
          id={id + "-from"}
          type="datetime-local"
          className="input"
          aria-label={label + " 开始时间"}
          value={from}
          disabled={disabled}
          onChange={(event) => onFromChange(event.target.value)}
        />
      </label>
      <span className="cs-range__sep" aria-hidden="true">→</span>
      <label className="cs-range__field">
        <span className="cs-range__label">结束</span>
        <input
          id={id + "-to"}
          type="datetime-local"
          className="input"
          aria-label={label + " 结束时间"}
          value={to}
          disabled={disabled}
          onChange={(event) => onToChange(event.target.value)}
        />
      </label>
    </div>
  );
}

/** 区块范围：起止块高。空串表示不限，**不用 0 填充**（0 是合法块高）。 */
export function BlockRange({
  from,
  to,
  onFromChange,
  onToChange,
  label,
  onBlur,
}: {
  from: string;
  to: string;
  onFromChange: (next: string) => void;
  onToChange: (next: string) => void;
  label: string;
  onBlur?: () => void;
}) {
  return (
    <div className="cs-range" role="group" aria-label={label}>
      <label className="cs-range__field">
        <span className="cs-range__label">起始块</span>
        <input
          type="text"
          inputMode="numeric"
          className="input mono"
          aria-label={label + " 起始块高"}
          placeholder="不限"
          value={from}
          onBlur={onBlur}
          onChange={(event) => onFromChange(event.target.value.replace(/[^0-9]/g, ""))}
        />
      </label>
      <span className="cs-range__sep" aria-hidden="true">→</span>
      <label className="cs-range__field">
        <span className="cs-range__label">结束块</span>
        <input
          type="text"
          inputMode="numeric"
          className="input mono"
          aria-label={label + " 结束块高"}
          placeholder="不限"
          value={to}
          onBlur={onBlur}
          onChange={(event) => onToChange(event.target.value.replace(/[^0-9]/g, ""))}
        />
      </label>
    </div>
  );
}

/**
 * 文件拖放区（§8.2 FileDropzone）：
 * 拖放 + 文件选择 + 取消解析 + 失败文件与解析进度。
 *
 * 组件只负责把 File 交给调用方；**解析本身跑在现有 Web Worker 里**，
 * 组件不复制解析逻辑，也不改变现有的取消语义。
 */
export function FileDropzone({
  onFiles,
  accept,
  multiple = false,
  label,
  hint,
  disabled,
  progress,
  onCancel,
  fileName,
  busy,
}: {
  onFiles: (files: File[]) => void;
  accept?: string;
  multiple?: boolean;
  label: string;
  hint?: React.ReactNode;
  disabled?: boolean;
  progress?: React.ReactNode;
  onCancel?: () => void;
  fileName?: string;
  busy?: boolean;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const id = useId();

  const handle = useCallback(
    (files: FileList | null) => {
      if (!files || files.length === 0) return;
      onFiles(Array.from(files));
    },
    [onFiles],
  );

  return (
    <div className="cs-dropzone-wrap">
      <div
        className="cs-dropzone"
        data-drag={dragging ? "true" : "false"}
        role="button"
        tabIndex={0}
        aria-label={label}
        aria-disabled={disabled || busy}
        onClick={() => {
          if (!disabled && !busy) inputRef.current?.click();
        }}
        onKeyDown={(event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            if (!disabled && !busy) inputRef.current?.click();
          }
        }}
        onDragOver={(event) => {
          event.preventDefault();
          if (!disabled) setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(event) => {
          event.preventDefault();
          setDragging(false);
          if (!disabled && !busy) handle(event.dataTransfer.files);
        }}
      >
        <UploadCloud size={22} aria-hidden="true" />
        <span className="cs-dropzone__title">{fileName || label}</span>
        {hint ? <span className="cs-dropzone__hint">{hint}</span> : null}
        <input
          ref={inputRef}
          id={id}
          type="file"
          accept={accept}
          multiple={multiple}
          className="visually-hidden"
          tabIndex={-1}
          /* 关键：`input.click()` 派发的 click 事件**会冒泡**到外层 role="button" 的 onClick，
             外层又调用一次 input.click()，形成无限递归。这里必须截断冒泡。 */
          onClick={(event) => event.stopPropagation()}
          onChange={(event) => {
            handle(event.target.files);
            /* 允许重复选择同一个文件（否则第二次选它会因为 value 未变而不触发 change）。 */
            event.target.value = "";
          }}
        />
      </div>
      {progress ? (
        <div className="cs-dropzone__progress">
          {progress}
          {onCancel ? (
            <button type="button" className="btn btn--sm" onClick={onCancel}>
              取消解析
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/**
 * 批量地址输入（§8.2 AddressInputList）
 *
 * **职责边界（蓝图 §19.1）**：解析、链识别、去重判定全部由**调用方现有的
 * Web Worker / 服务端校验**给出，本组件只负责录入与呈现——
 * 它不做任何格式猜测，也不自己判重，因此不会与既有规则产生第二套真相。
 *
 * 呈现三件事：错误定位到具体行、重复项标记、链归属标记。
 */
export interface AddressInputEntry {
  /** 原始输入行（保留完整原文，不截断）。 */
  value: string;
  /** 输入里的行号（1 基），用于把错误定位到具体行。 */
  line: number;
  /** 链归属；由调用方按现有判定给出，组件不猜。 */
  chain?: string;
  /** 校验是否通过。 */
  valid: boolean;
  /** 失败原因（安全文案）。 */
  error?: string;
  /** 与前面某行重复。 */
  duplicate?: boolean;
  /** 服务端拒绝时的稳定原因码，便于用户对照。 */
  code?: string;
}

export function AddressInputList({
  value,
  onChange,
  label,
  placeholder = "每行一个地址；也可以用文件导入",
  max,
  entries,
  onRemove,
  disabled,
  rows = 8,
}: {
  value: string;
  onChange: (next: string) => void;
  label: string;
  placeholder?: string;
  /** 服务端上限（例如"最多 100 地址"，§12.17）。 */
  max?: number;
  /** 由调用方的现有解析/校验流程给出；未提供时只渲染纯文本域。 */
  entries?: AddressInputEntry[];
  onRemove?: (line: number) => void;
  disabled?: boolean;
  rows?: number;
}) {
  const id = useId();
  const list = entries ?? [];
  const accepted = list.filter((entry) => entry.valid && !entry.duplicate).length;
  const rejected = list.filter((entry) => !entry.valid).length;
  const duplicates = list.filter((entry) => entry.duplicate).length;

  return (
    <div className="field">
      <label className="field__label" htmlFor={id}>
        {label}
        {typeof max === "number" ? (
          <span className="cs-section__count">
            （上限 {max} 条，当前 {accepted} 条）
          </span>
        ) : null}
      </label>
      <textarea
        id={id}
        className="textarea mono"
        style={{ minHeight: rows * 22 }}
        aria-label={label}
        placeholder={placeholder}
        spellCheck={false}
        disabled={disabled}
        value={value}
        aria-invalid={rejected > 0 || undefined}
        onChange={(event) => onChange(event.target.value)}
      />
      {list.length > 0 ? (
        <>
          <div className="cs-addrs__summary">
            <span>
              接受 <b className="num">{accepted}</b>
            </span>
            <span data-tone={rejected > 0 ? "danger" : undefined}>
              拒绝 <b className="num">{rejected}</b>
            </span>
            <span data-tone={duplicates > 0 ? "warn" : undefined}>
              重复 <b className="num">{duplicates}</b>
            </span>
          </div>
          <ul className="cs-addrs" aria-label={label + " 逐行结果"}>
            {list.map((entry) => (
              <li className="cs-addrs__item" key={entry.line + ":" + entry.value} data-invalid={entry.valid && !entry.duplicate ? undefined : "true"}>
                <span className="cs-addrs__line num">第 {entry.line} 行</span>
                {/* 完整地址：列表里视觉省略由 CSS 负责，title 与复制仍是完整原文。 */}
                <span className="cs-addrs__value mono" title={entry.value}>
                  {entry.value}
                </span>
                {entry.chain ? <span className="badge badge--neutral">{entry.chain}</span> : null}
                {entry.duplicate ? <span className="badge badge--warn">重复</span> : null}
                {!entry.valid ? <span className="badge badge--danger">{entry.code ?? "无效"}</span> : null}
                {onRemove ? (
                  <button
                    type="button"
                    className="cs-copybtn"
                    aria-label={"移除第 " + entry.line + " 行"}
                    title="移除该行"
                    onClick={() => onRemove(entry.line)}
                  >
                    <X size={13} />
                  </button>
                ) : null}
                {entry.error && !entry.valid ? <span className="cs-addrs__error">{entry.error}</span> : null}
              </li>
            ))}
          </ul>
        </>
      ) : null}
    </div>
  );
}

/**
 * 链选择器（§8.2 ChainSelector）。
 * 选项来自服务端链元数据接口——**不在前端硬编码链列表**；
 * "链列表失败"继续按现有流程走 Dialog（§12.14）。
 */
export function ChainSelector({
  chains,
  value,
  onChange,
  label = "链",
  disabled,
  id,
}: {
  chains: Array<{ key: string; label: string }>;
  value: string;
  onChange: (next: string) => void;
  label?: string;
  disabled?: boolean;
  id?: string;
}) {
  const auto = useId();
  return (
    <label className="field" style={{ minWidth: 150 }}>
      <span className="field__label">{label}</span>
      <select
        id={id ?? auto}
        className="select"
        aria-label={label}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      >
        {chains.length === 0 ? <option value="">（链元数据不可用）</option> : null}
        {chains.map((chain) => (
          <option key={chain.key} value={chain.key}>
            {chain.label}
          </option>
        ))}
      </select>
    </label>
  );
}

/**
 * Dataset 选择（§8.2 DatasetSelector）。
 * 上限来自服务端约束（§12.17"最多 6 Dataset"），由 `max` 传入而不是写死在组件里。
 */
export function DatasetSelector({
  datasets,
  values,
  onChange,
  label = "Dataset",
  max,
}: {
  datasets: ComboOption[];
  values: string[];
  onChange: (next: string[]) => void;
  label?: string;
  max?: number;
}) {
  return (
    <div className="field">
      <span className="field__label">
        {label}
        {typeof max === "number" ? <span className="cs-section__count">（最多 {max} 项，已选 {values.length}）</span> : null}
      </span>
      <MultiSelect options={datasets} values={values} onChange={onChange} label={label} max={max} />
    </div>
  );
}
