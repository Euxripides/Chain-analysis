import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Check, CircleAlert, Info, ShieldQuestion, TriangleAlert } from "lucide-react";

/**
 * Dialog **Core**（用户端重构规范 V4 §21.0）
 *
 * 这一层只负责"一个弹窗请求的生命周期与语义"，**完全不含视觉**：
 *   DialogContext / queue / open·notify·confirm·prompt·dismiss / promise resolve /
 *   confirmWord / fields / acknowledge / 焦点与关闭语义。
 *
 * 视觉由 renderer 决定（`DialogRendererProps`）：
 *   - `LegacyDialogRenderer`：后台端默认，形态与重构前完全一致；
 *   - `UserDialogRenderer`（@chain/user-ui）：用户端，Ant Modal + ChainSight Motion。
 *
 * 为什么拆：规范 §21.0 明确"不能简单在 @chain/user-ui 外面再创建一个新的 Context"——
 * 那会让两个控制台各自维护一份队列、确认词与 acknowledge 规则，
 * 迟早出现"用户端能 ESC 关掉恢复码、后台端不能"这类语义漂移。
 * 因此 **queue 与全部业务语义只有这一份**，renderer 只是换了一层皮。
 *
 * 约束来源：AGENTS.md / docs/code-standards.md —— 警告、说明、成功、错误、风险与
 * 确认类内容只能通过弹窗呈现。因此 `useDialog()` 是两个控制台唯一的业务提示通道。
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
  testId?: string;
}

/**
 * 多字段表单里的一个字段。
 *
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

export interface DialogContextValue {
  open: (req: DialogRequest) => Promise<string | null>;
  /** 更新同一次操作的弹窗，按独立请求标识定位，不会修改队列中的其他弹窗。 */
  openSession: (req: DialogRequest) => {
    result: Promise<string | null>;
    update: (patch: Partial<Omit<DialogRequest, "onResolve">>) => void;
    dismiss: () => void;
  };
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
export function dialogFieldOk(field: DialogField, raw: string): boolean {
  if (field.type === "checkbox") return field.required === false || raw === "true";
  const value = raw.trim();
  if (field.required !== false && value === "") return false;
  return field.pattern ? field.pattern.test(value) : true;
}

/** 表单初始值：只接受显式 defaultValue，其余字段留空（绝不预填敏感值）。 */
function initialFormValues(fields: DialogField[] | undefined): Record<string, string> {
  const values: Record<string, string> = {};
  for (const field of fields ?? []) {
    values[field.name] =
      field.type === "checkbox" ? (field.defaultValue === "true" ? "true" : "") : (field.defaultValue ?? "");
  }
  return values;
}

/**
 * 类型图标 + 色调（规范 §21.2）。
 *
 * 图标是**形状通道**：即使把界面转成灰度，五种弹窗仍然可区分，
 * 不会退化成"只能靠红/黄/蓝判断这是不是危险操作"（§55 not color-only status）。
 * `label` 用于屏幕阅读器与旧 renderer 的文字徽标，不进入可视读的标题区以外的位置。
 */
export const DIALOG_KIND_META: Record<DialogKind, { tone: string; label: string; icon: React.ReactNode }> = {
  info: { tone: "info", label: "提示", icon: <Info size={17} strokeWidth={2.3} /> },
  success: { tone: "ok", label: "已完成", icon: <Check size={17} strokeWidth={2.8} /> },
  warning: { tone: "warn", label: "注意", icon: <TriangleAlert size={17} strokeWidth={2.3} /> },
  error: { tone: "danger", label: "错误", icon: <CircleAlert size={17} strokeWidth={2.3} /> },
  confirm: { tone: "warn", label: "请确认", icon: <ShieldQuestion size={17} strokeWidth={2.3} /> },
};

/* ------------------------------------------------------------------ renderer */

/**
 * Renderer 契约。
 *
 * renderer 拿到一个请求和 resolve 回调，必须完整实现：
 *   - confirmWord 未匹配时主操作 disabled；
 *   - 必填字段/勾选框未就绪时主操作 disabled；
 *   - acknowledge 未勾选时：关闭按钮、ESC、遮罩点击全部无效；
 *   - 结果编码：fields → `form:<json>`，input → `input:<value>`，其余 → action.result。
 *
 * 为了让两个 renderer 不必各写一遍这些规则，它们共用 `useDialogRequestState()`。
 */
export interface DialogRendererProps {
  request: DialogRequest;
  onResolve: (value: string | null) => void;
}

export type DialogRenderer = React.ComponentType<DialogRendererProps>;

/**
 * 请求级状态机（Core 的一部分，不属于任何 renderer 的视觉）。
 *
 * 两个 renderer 共用同一份实现，保证"确认词/多字段/一次性勾选"的判定在
 * 用户端与后台端**逐字一致**——这正是规范 §21.0 要防的漂移。
 */
export function useDialogRequestState(request: DialogRequest, onResolve: (value: string | null) => void) {
  const [word, setWord] = useState("");
  const [inputValue, setInputValue] = useState(request.input?.defaultValue ?? "");
  const [formValues, setFormValues] = useState<Record<string, string>>(() => initialFormValues(request.fields));
  const [busy, setBusy] = useState(false);
  const [acked, setAcked] = useState(!request.acknowledge);
  /* 键盘/遮罩处理器挂在稳定的事件监听上，因此用 ref 读取最新勾选状态。 */
  const ackedRef = useRef(!request.acknowledge);

  const requiresAck = Boolean(request.acknowledge);
  const ackPending = requiresAck && !acked;
  const setAck = useCallback((next: boolean) => {
    ackedRef.current = next;
    setAcked(next);
  }, []);

  /**
   * 队列前移到下一个请求时重置弹窗自身的输入状态。
   * renderer 不随请求重新挂载，若不重置，上一个弹窗的确认词/输入值会被带进下一个。
   */
  useEffect(() => {
    setWord("");
    setInputValue(request.input?.defaultValue ?? "");
    setFormValues(initialFormValues(request.fields));
    setBusy(false);
    ackedRef.current = !request.acknowledge;
    setAcked(!request.acknowledge);
  }, [request]);

  const actions = useMemo(
    () => request.actions ?? [{ label: "关闭", variant: "primary" as const, result: "ok" }],
    [request.actions],
  );
  const requiresWord = Boolean(request.confirmWord);
  const wordOk = !requiresWord || word.trim() === request.confirmWord;
  const needsInput = Boolean(request.input);
  const inputOk = !needsInput || !request.input?.pattern || request.input.pattern.test(inputValue.trim());
  const formFields = request.fields ?? [];
  const needsFields = formFields.length > 0;
  const fieldsOk = !needsFields || formFields.every((field) => dialogFieldOk(field, formValues[field.name] ?? ""));
  const setFormValue = useCallback((name: string, value: string) => {
    setFormValues((current) => ({ ...current, [name]: value }));
  }, []);

  /** 某个动作当前是否被禁用（不含 busy 与 ackPending，那两个是所有动作共有的）。 */
  const actionDisabled = useCallback(
    (action: DialogAction) => {
      if (action.disabled) return true;
      if (action.variant === "default") return false;
      if (requiresWord && !wordOk) return true;
      if (needsInput && !inputOk) return true;
      if (needsFields && !fieldsOk) return true;
      return false;
    },
    [fieldsOk, inputOk, needsFields, needsInput, requiresWord, wordOk],
  );

  /** 提交一个动作：default 直接按取消处理，其余按编码规则 resolve。 */
  const commit = useCallback(
    (action: DialogAction) => {
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
    },
    [formValues, inputValue, needsFields, needsInput, onResolve],
  );

  /**
   * 取消（ESC / 遮罩 / 关闭按钮的统一入口）。
   *
   * 一次性内容未被确认保存前**一律不生效**——这条规则放在 Core 里，
   * 因此任何 renderer 都不可能"忘记"实现它。
   */
  const cancel = useCallback(() => {
    if (!ackedRef.current) return;
    onResolve(null);
  }, [onResolve]);

  /** 关闭按钮专用：一次性内容未确认时按钮保持 disabled（而不是点了没反应）。 */
  const canCancel = acked;

  return {
    word,
    setWord,
    inputValue,
    setInputValue,
    formValues,
    setFormValue,
    busy,
    acked,
    setAck,
    requiresAck,
    ackPending,
    requiresWord,
    wordOk,
    needsInput,
    inputOk,
    formFields,
    needsFields,
    fieldsOk,
    actions,
    actionDisabled,
    commit,
    cancel,
    canCancel,
    meta: DIALOG_KIND_META[request.kind],
  };
}

/* ------------------------------------------------------------------ provider */

function defaultConfirmActions(): DialogAction[] {
  return [
    { label: "取消", result: "", variant: "default" },
    { label: "确认", result: "confirm", variant: "primary" },
  ];
}

export interface DialogProviderProps {
  children: React.ReactNode;
  /**
   * 视觉层。默认 `LegacyDialogRenderer`（后台端形态，与重构前完全一致）。
   * 用户端在 main.tsx 传入 `UserDialogRenderer`。
   *
   * **只有一个 Provider、一份 queue**（规范 §21.0）：renderer 不能创建自己的 Context。
   */
  renderer?: DialogRenderer;
}

/**
 * 队列 + renderer 装配。
 *
 * 队列不变式（规范 §21.4）：任意时刻**只有一个**请求在前台。
 * 下一个请求必须等当前请求 resolve 之后才会成为队首被渲染，
 * 因此两个 Dialog 不可能叠在一起，也不需要 renderer 自己处理排队。
 */
export function makeDialogProvider(LegacyRenderer: DialogRenderer) {
  return function DialogProviderWithDefault({ children, renderer: Renderer = LegacyRenderer }: DialogProviderProps) {
    const [queue, setQueue] = useState<DialogRequest[]>([]);
    const active = queue[0] ?? null;

    const openSession = useCallback((req: DialogRequest) => {
      let resolve!: (value: string | null) => void;
      const result = new Promise<string | null>((done) => { resolve = done; });
      setQueue((prev) => [...prev, { ...req, onResolve: resolve }]);
      return {
        result,
        update: (patch: Partial<Omit<DialogRequest, "onResolve">>) => {
          setQueue((prev) => prev.map((item) => item.onResolve === resolve ? { ...item, ...patch, onResolve: resolve } : item));
        },
        dismiss: () => {
          setQueue((prev) => prev.filter((item) => item.onResolve !== resolve));
          resolve(null);
        },
      };
    }, []);
    const open = useCallback((req: DialogRequest) => openSession(req).result, [openSession]);

    const resolveActive = useCallback((value: string | null) => {
      setQueue((prev) => {
        const [head, ...rest] = prev;
        head?.onResolve?.(value);
        return rest;
      });
    }, []);

    const value = useMemo<DialogContextValue>(
      () => ({
        open,
        openSession,
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
      [open, openSession, resolveActive],
    );

    return (
      <DialogContext.Provider value={value}>
        {children}
        {active ? <Renderer request={active} onResolve={resolveActive} /> : null}
      </DialogContext.Provider>
    );
  };
}
