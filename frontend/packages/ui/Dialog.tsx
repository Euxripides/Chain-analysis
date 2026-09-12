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
    }),
    [open],
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

function DialogHost({ request, onResolve }: { request: DialogRequest; onResolve: (v: string | null) => void }) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const previouslyFocused = useRef<Element | null>(null);
  const [word, setWord] = useState("");
  const [inputValue, setInputValue] = useState(request.input?.defaultValue ?? "");
  const [busy, setBusy] = useState(false);

  const actions = request.actions ?? [{ label: "关闭", variant: "primary", result: "ok" }];
  const requiresWord = Boolean(request.confirmWord);
  const wordOk = !requiresWord || word.trim() === request.confirmWord;
  const needsInput = Boolean(request.input);
  const inputOk =
    !needsInput || !request.input?.pattern || request.input.pattern.test(inputValue.trim());

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
        if (e.target === e.currentTarget) onResolve(null);
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
          <button className="dialog__close" aria-label="关闭对话框" onClick={() => onResolve(null)} type="button">
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
        </div>
        <div className="dialog__foot">
          {actions.map((action) => (
            <button
              key={action.label}
              type="button"
              className={`btn${action.variant === "primary" ? " btn--primary" : action.variant === "danger" ? " btn--danger" : ""}`}
              disabled={busy || (requiresWord && action.variant !== "default" && !wordOk) || (needsInput && action.variant !== "default" && !inputOk)}
              onClick={() => {
                if (action.variant === "default") {
                  onResolve(action.result ?? null);
                  return;
                }
                setBusy(true);
                // 输入类弹窗把用户输入编码进结果，调用方用 toDialogInput 解出。
                onResolve(needsInput ? `input:${inputValue.trim()}` : (action.result ?? action.label));
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
