import React, { useEffect, useRef } from "react";
import { X } from "lucide-react";
import {
  dialogFieldOk,
  useDialogRequestState,
  type DialogRendererProps,
} from "./dialog-core";

/**
 * 后台端 Dialog 视觉层（用户端重构规范 V4 §21.0）
 *
 * 形态、类名、DOM 结构与重构前**逐字一致**：`.dialog-backdrop / .dialog / .dialog__head`
 * 等类名被 @chain/ui/ui.css 与后台端样式表依赖，改一个字符后台端就会被动变样。
 *
 * 拆分只是把"语义"移到了 dialog-core：
 *   本文件不再持有 queue、不再决定 acknowledge/confirmWord/fields 的判定规则，
 *   只负责把这些状态画出来。因此"用户端能 ESC 关掉恢复码、后台端不能"这类漂移
 *   在结构上就不可能发生。
 */
export function LegacyDialogRenderer({ request, onResolve }: DialogRendererProps) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const previouslyFocused = useRef<Element | null>(null);
  const state = useDialogRequestState(request, onResolve);
  const {
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
    needsInput,
    inputOk,
    formFields,
    needsFields,
    fieldsOk,
    actions,
    actionDisabled,
    commit,
    cancel,
    meta,
  } = state;

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
        cancel();
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
  }, [cancel]);

  return (
    <div
      className="dialog-backdrop"
      role="presentation"
      onMouseDown={(e) => {
        // 同上：一次性内容未被确认保存前，点击遮罩也不关闭。
        if (e.target === e.currentTarget) cancel();
      }}
    >
      <div
        ref={dialogRef}
        className={`dialog${request.details && request.details.length > 4 ? " dialog--wide" : ""}`}
        data-kind={request.kind}
        role="dialog"
        aria-modal="true"
        aria-labelledby="dialog-title"
        aria-describedby="dialog-body"
      >
        <div className="dialog__head">
          {/*
            类型标记同时包含"图标"和"文字"两个节点，由各端 CSS 决定显示哪一个：
              · 用户端（html[data-app="user"]）：显示图标 —— 形状通道，灰度下也能区分五种弹窗；
              · 后台端（默认，无 data-app）：显示原来的文字徽标 —— 与重构前完全一致。
          */}
          <span className={`dialog__kindbadge badge badge--${meta.tone}`} aria-hidden="true">
            <span className="dialog__kindicon">{meta.icon}</span>
            <span className="dialog__kindlabel">{meta.label}</span>
          </span>
          <span className="visually-hidden">{meta.label}</span>
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
                /* 与重构前完全一致：非 checkbox 字段未满足 pattern 时也要显示 patternHint。 */
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
              data-testid={action.testId}
              type="button"
              className={`btn${action.variant === "primary" ? " btn--primary" : action.variant === "danger" ? " btn--danger" : ""}`}
              title={action.disabled ? action.title : undefined}
              disabled={busy || ackPending || actionDisabled(action)}
              onClick={() => commit(action)}
            >
              {action.label}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
