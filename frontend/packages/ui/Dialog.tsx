import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { X } from "lucide-react";

/**
 * Dialog 是两套系统里唯一的提示通道。
 *
 * 约束来源：AGENTS.md 与 docs/code-standards.md —— 警告、说明、成功、错误、
 * 风险和确认类内容只能通过弹窗呈现，禁止以横幅/提示卡/说明段落嵌入页面布局。
 * 因此这里同时导出命令式的 `useDialog()`，任何组件都不需要自己排布提示。
 *
 * 无障碍：焦点陷阱、ESC 关闭、关闭后焦点回到触发元素、长内容在 body 内滚动。
 */

export type DialogKind = "info" | "success" | "warning" | "error" | "confirm";

export interface DialogAction {
  label: string;
  value?: string;
  variant?: "primary" | "danger" | "default";
  /** confirm 类对话框的语义结果；未提供时使用 label。 */
  result?: string;
  /**
   * 该动作在本次会话里不可用（例如"该账号未启用 MFA，无法重置"）。
   *
   * 按钮保持可见并 disabled —— 能力边界必须让用户看得见，而不是把入口藏起来；
   * 禁用原因写在 title 上（同时建议在 details 里给出一条可读的说明，
   * 因为部分浏览器不会为 disabled 按钮显示 tooltip）。
   */
  disabled?: boolean;
  title?: string;
}

/**
 * 多字段表单里的一个字段。
 *
 * 视觉上沿用既有 field / input / btn 样式，不引入新的视觉语言；
 * checkbox 类型的 required 表示"必须勾选"（用于破坏性操作的"我已确认上述影响"）。
 */
export interface DialogField {
  name: string;
  label: string;
  type?: "text" | "password" | "checkbox";
  placeholder?: string;
  defaultValue?: string;
  /** 默认 true：必填字段未填写（或未勾选）前，非 default 动作按钮保持 disabled。 */
  required?: boolean;
  pattern?: RegExp;
  patternHint?: string;
  autoComplete?: string;
}

export interface DialogRequest {
  kind: DialogKind;
  title: string;
  /** 正文支持 React 节点；纯文本会自动换行。 */
  body?: React.ReactNode;
  /** 关键字段明细（键值对表格）。 */
  details?: Array<{ label: string; value: React.ReactNode }>;
  actions?: DialogAction[];
  /** 危险操作需要输入确认词。 */
  confirmWord?: string;
  /** 需要用户输入一个值时使用（替代原生 window.prompt）。 */
  input?: { label: string; placeholder?: string; defaultValue?: string; pattern?: RegExp; patternHint?: string };
  /**
   * 多字段表单（例如破坏性命令的"原因 + step-up 密码 + 影响确认"）。
   *
   * 与 input（单值简写）互斥，两项都给出时以 fields 为准。所有必填字段就绪前，
   * 非 default 动作按钮保持 disabled；提交结果编码为 `form:<json>`，
   * 调用方用 dialogFormValues() 解出。取消（variant=default）始终可用。
   */
  fields?: DialogField[];
  /**
   * 需要用户主动勾选确认后才能关闭。
   *
   * 用途是一次性内容：例如 MFA 恢复码明文只出现这一次，
   * 不勾选"我已保存"就既不能点按钮、也不能用 ESC/点击遮罩关闭
   * （否则用户会以为自己还能再看一次）。
   */
  acknowledge?: { label: string; hint?: string };
  onResolve?: (value: string | null) => void;
}

interface DialogContextValue {
  open: (req: DialogRequest) => Promise<string | null>;
  notify: (kind: DialogKind, title: string, body?: React.ReactNode, details?: DialogRequest["details"]) => void;
  confirm: (req: Omit<DialogRequest, "kind"> & { kind?: DialogKind }) => Promise<string | null>;
  /** 单值输入弹窗；用户取消时 resolve(null)。 */
  prompt: (req: {
    title: string;
    body?: React.ReactNode;
    input: NonNullable<DialogRequest["input"]>;
    submitLabel?: string;
    kind?: DialogKind;
  }) => Promise<string | null>;
  /**
   * 命令式关闭当前弹窗（把队首请求按"取消"结果 resolve 掉）。
   * 用途：长任务型弹窗（例如隔离解析进度）在任务正常结束后必须自行退场，
   * 否则会一直占据页面并挡住后续提示。调用方需自行区分"用户取消"与"任务已结束"。
   */
  dismiss: () => void;
}

const DialogContext = createContext<DialogContextValue | null>(null);

export function useDialog(): DialogContextValue {
  const ctx = useContext(DialogContext);
  if (!ctx) {
    throw new Error("useDialog 必须在 <DialogProvider> 内使用");
  }
  return ctx;
}

/** 从 prompt() 的返回值中取出用户输入；未输入或取消时返回 null。 */
export function dialogInputValue(result: string | null): string | null {
  if (!result || !result.startsWith("input:")) return null;
  const v = result.slice("input:".length).trim();
  return v === "" ? null : v;
}

/** 从 open()/confirm() 的结果中取出多字段表单值；取消或非表单结果返回 null。 */
export function dialogFormValues(result: string | null): Record<string, string> | null {
  if (!result || !result.startsWith("form:")) return null;
  try {
    const parsed: unknown = JSON.parse(result.slice("form:".length));
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
    const values: Record<string, string> = {};
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      values[key] = value === null || value === undefined ? "" : String(value);
    }
    return values;
  } catch {
    return null;
  }
}

/** 单个字段是否满足提交条件（必填、勾选、pattern）。 */
function dialogFieldOk(field: DialogField, raw: string): boolean {
  if (field.type === "checkbox") return field.required === false || raw === "true";
  const value = raw.trim();
  if (field.required !== false && value === "") return false;
  return field.pattern ? field.pattern.test(value) : true;
}

export function DialogProvider({ children }: { children: React.ReactNode }) {
  const [queue, setQueue] = useState<DialogRequest[]>([]);
  const active = queue[0] ?? null;

  const open = useCallback((req: DialogRequest) => {
    return new Promise<string | null>((resolve) => {
      setQueue((prev) => [...prev, { ...req, onResolve: resolve }]);
    });
  }, []);

  const resolveActive = useCallback(
    (value: string | null) => {
      setQueue((prev) => {
        const [head, ...rest] = prev;
        head?.onResolve?.(value);
        return rest;
      });
    },
    [],
  );

  const value = useMemo<DialogContextValue>(
    () => ({
      open,
      notify: (kind, title, body, details) => {
        void open({ kind, title, body, details, actions: [{ label: "知道了", variant: "primary", result: "ok" }] });
      },
      confirm: (req) => open({ kind: req.kind ?? "confirm", actions: req.actions ?? defaultConfirmActions(), ...req }),
      prompt: (req) =>
        open({
          kind: req.kind ?? "info",
          title: req.title,
          body: req.body,
          input: req.input,
          actions: [
            { label: "取消", result: "", variant: "default" },
            { label: req.submitLabel ?? "确定", result: "ok", variant: "primary" },
          ],
        }),
      dismiss: () => resolveActive(null),
    }),
    [open, resolveActive],
  );

  return (
    <DialogContext.Provider value={value}>
      {children}
      {active ? <DialogHost request={active} onResolve={resolveActive} /> : null}
    </DialogContext.Provider>
  );
}

function defaultConfirmActions(): DialogAction[] {
  return [
    { label: "取消", result: "", variant: "default" },
    { label: "确认", result: "confirm", variant: "primary" },
  ];
}

const KIND_META: Record<DialogKind, { tone: string; label: string }> = {
  info: { tone: "info", label: "提示" },
  success: { tone: "ok", label: "已完成" },
  warning: { tone: "warn", label: "注意" },
  error: { tone: "danger", label: "错误" },
  confirm: { tone: "warn", label: "请确认" },
};

/** 表单初始值：只接受显式 defaultValue，其余字段留空（绝不预填敏感值）。 */
function initialFormValues(fields: DialogField[] | undefined): Record<string, string> {
  const values: Record<string, string> = {};
  for (const field of fields ?? []) {
    values[field.name] = field.type === "checkbox" ? (field.defaultValue === "true" ? "true" : "") : (field.defaultValue ?? "");
  }
  return values;
}

function DialogHost({ request, onResolve }: { request: DialogRequest; onResolve: (v: string | null) => void }) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const previouslyFocused = useRef<Element | null>(null);
  const [word, setWord] = useState("");
  const [inputValue, setInputValue] = useState(request.input?.defaultValue ?? "");
  const [formValues, setFormValues] = useState<Record<string, string>>(() => initialFormValues(request.fields));
  const [busy, setBusy] = useState(false);
  const [acked, setAcked] = useState(!request.acknowledge);
  // 键盘/遮罩处理器挂在稳定的事件监听上，因此用 ref 读取最新勾选状态。
  const ackedRef = useRef(!request.acknowledge);

  const requiresAck = Boolean(request.acknowledge);
  const ackPending = requiresAck && !acked;
  const setAck = (next: boolean) => {
    ackedRef.current = next;
    setAcked(next);
  };

  /**
   * 队列前移到下一个请求时重置弹窗自身的输入状态。
   * DialogHost 不随请求重新挂载，若不重置，上一个弹窗的确认词/输入值会被带进下一个。
   */
  useEffect(() => {
    setWord("");
    setInputValue(request.input?.defaultValue ?? "");
    setFormValues(initialFormValues(request.fields));
    setBusy(false);
    ackedRef.current = !request.acknowledge;
    setAcked(!request.acknowledge);
  }, [request]);

  const actions = request.actions ?? [{ label: "关闭", variant: "primary", result: "ok" }];
  const requiresWord = Boolean(request.confirmWord);
  const wordOk = !requiresWord || word.trim() === request.confirmWord;
  const needsInput = Boolean(request.input);
  const inputOk =
    !needsInput || !request.input?.pattern || request.input.pattern.test(inputValue.trim());
  const formFields = request.fields ?? [];
  const needsFields = formFields.length > 0;
  const fieldsOk = !needsFields || formFields.every((field) => dialogFieldOk(field, formValues[field.name] ?? ""));
  const setFormValue = (name: string, value: string) => setFormValues((current) => ({ ...current, [name]: value }));

  useEffect(() => {
    previouslyFocused.current = document.activeElement;
    const node = dialogRef.current;
    const focusables = () =>
      Array.from(
        node?.querySelectorAll<HTMLElement>(
          'button:not([disabled]), [href], input:not([disabled]), select, textarea, [tabindex]:not([tabindex="-1"])',
        ) ?? [],
      ).filter((el) => el.offsetParent !== null);
    focusables()[0]?.focus();

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        // 一次性内容未被确认保存前，ESC 不关闭弹窗。
        if (!ackedRef.current) return;
        onResolve(null);
        return;
      }
      if (e.key !== "Tab") return;
      const list = focusables();
      if (list.length === 0) return;
      const first = list[0];
      const last = list[list.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown, true);
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      document.body.style.overflow = prevOverflow;
      if (previouslyFocused.current instanceof HTMLElement) {
        previouslyFocused.current.focus();
      }
    };
  }, [onResolve]);

  const meta = KIND_META[request.kind];

  return (
    <div
      className="dialog-backdrop"
      role="presentation"
      onMouseDown={(e) => {
        // 同上：一次性内容未被确认保存前，点击遮罩也不关闭。
        if (e.target === e.currentTarget && ackedRef.current) onResolve(null);
      }}
    >
      <div
        ref={dialogRef}
        className={`dialog${request.details && request.details.length > 4 ? " dialog--wide" : ""}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby="dialog-title"
        aria-describedby="dialog-body"
      >
        <div className="dialog__head">
          <span className={`badge badge--${meta.tone}`} aria-hidden="true">
            {meta.label}
          </span>
          <h2 className="dialog__title" id="dialog-title">
            {request.title}
          </h2>
          <button
            className="dialog__close"
            aria-label="关闭对话框"
            type="button"
            disabled={ackPending}
            title={ackPending ? "请先勾选确认后再关闭" : undefined}
            onClick={() => onResolve(null)}
          >
            <X size={18} />
          </button>
        </div>
        <div className="dialog__body" id="dialog-body">
          {request.body ? <div>{request.body}</div> : null}
          {request.details && request.details.length > 0 ? (
            <dl className="dialog__kv" style={{ marginTop: request.body ? "var(--sp-4)" : 0 }}>
              {request.details.map((d) => (
                <React.Fragment key={d.label}>
                  <dt>{d.label}</dt>
                  <dd>{d.value}</dd>
                </React.Fragment>
              ))}
            </dl>
          ) : null}
          {needsInput ? (
            <div className="field" style={{ marginTop: "var(--sp-4)" }}>
              <label className="field__label" htmlFor="dialog-input">
                {request.input!.label}
              </label>
              <input
                id="dialog-input"
                className="input"
                autoComplete="off"
                aria-invalid={!inputOk}
                placeholder={request.input!.placeholder}
                value={inputValue}
                onChange={(e) => setInputValue(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && inputOk) {
                    e.preventDefault();
                    onResolve(`input:${inputValue.trim()}`);
                  }
                }}
              />
              {!inputOk && request.input!.patternHint ? (
                <span className="field__hint" style={{ color: "var(--c-danger)" }}>
                  {request.input!.patternHint}
                </span>
              ) : null}
            </div>
          ) : null}
          {needsFields ? (
            <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)", marginTop: "var(--sp-4)" }}>
              {formFields.map((field) => {
                const id = `dialog-field-${field.name}`;
                const value = formValues[field.name] ?? "";
                const ok = dialogFieldOk(field, value);
                const hint =
                  !ok && field.patternHint ? (
                    <span className="field__hint" style={{ color: "var(--c-danger)" }}>
                      {field.patternHint}
                    </span>
                  ) : null;
                if (field.type === "checkbox") {
                  return (
                    <div className="field" key={field.name}>
                      <label style={{ display: "flex", alignItems: "center", gap: "var(--sp-2)", cursor: "pointer" }} htmlFor={id}>
                        <input
                          id={id}
                          type="checkbox"
                          required={field.required !== false}
                          checked={value === "true"}
                          onChange={(e) => setFormValue(field.name, e.target.checked ? "true" : "")}
                        />
                        <span className="field__label" style={{ margin: 0 }}>
                          {field.label}
                        </span>
                      </label>
                      {hint}
                    </div>
                  );
                }
                return (
                  <div className="field" key={field.name}>
                    <label className="field__label" htmlFor={id}>
                      {field.label}
                    </label>
                    <input
                      id={id}
                      className="input"
                      type={field.type === "password" ? "password" : "text"}
                      autoComplete={field.autoComplete ?? (field.type === "password" ? "current-password" : "off")}
                      aria-invalid={!ok}
                      required={field.required !== false}
                      placeholder={field.placeholder}
                      value={value}
                      onChange={(e) => setFormValue(field.name, e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" && fieldsOk) {
                          e.preventDefault();
                          onResolve(`form:${JSON.stringify(formValues)}`);
                        }
                      }}
                    />
                    {hint}
                  </div>
                );
              })}
            </div>
          ) : null}
          {requiresWord ? (
            <div className="field" style={{ marginTop: "var(--sp-4)" }}>
              <label className="field__label" htmlFor="dialog-confirm-word">
                请输入 <span className="mono">{request.confirmWord}</span> 以继续
              </label>
              <input
                id="dialog-confirm-word"
                className="input"
                autoComplete="off"
                value={word}
                onChange={(e) => setWord(e.target.value)}
              />
            </div>
          ) : null}
          {requiresAck ? (
            <div className="field" style={{ marginTop: "var(--sp-4)" }}>
              <label style={{ display: "flex", alignItems: "center", gap: "var(--sp-2)", cursor: "pointer" }}>
                <input
                  id="dialog-acknowledge"
                  type="checkbox"
                  checked={acked}
                  onChange={(e) => setAck(e.target.checked)}
                />
                <span className="field__label" style={{ margin: 0 }}>
                  {request.acknowledge!.label}
                </span>
              </label>
              {request.acknowledge!.hint ? <span className="field__hint">{request.acknowledge!.hint}</span> : null}
            </div>
          ) : null}
        </div>
        <div className="dialog__foot">
          {actions.map((action) => (
            <button
              key={action.label}
              type="button"
              className={`btn${action.variant === "primary" ? " btn--primary" : action.variant === "danger" ? " btn--danger" : ""}`}
              title={action.disabled ? action.title : undefined}
              disabled={
                busy ||
                ackPending ||
                action.disabled ||
                (requiresWord && action.variant !== "default" && !wordOk) ||
                (needsInput && action.variant !== "default" && !inputOk) ||
                // 三项齐全才允许提交：必填字段/勾选框未就绪时，提交按钮保持 disabled。
                (needsFields && action.variant !== "default" && !fieldsOk)
              }
              onClick={() => {
                if (action.variant === "default") {
                  onResolve(action.result ?? null);
                  return;
                }
                setBusy(true);
                // 输入类弹窗把用户输入编码进结果：单值用 dialogInputValue 解出，
                // 多字段表单用 dialogFormValues 解出。密码等敏感值只随本次 Promise 传递，不落任何持久化状态。
                onResolve(
                  needsFields
                    ? `form:${JSON.stringify(formValues)}`
                    : needsInput
                      ? `input:${inputValue.trim()}`
                      : (action.result ?? action.label),
                );
              }}
            >
              {action.label}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
