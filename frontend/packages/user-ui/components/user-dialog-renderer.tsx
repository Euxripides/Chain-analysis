import React from "react";
import { Modal } from "antd";
import { X } from "lucide-react";
import {
  useDialogRequestState,
  type DialogRendererProps,
} from "@chain/ui/dialog-core";
import { CsButton } from "./buttons";

/**
 * 用户端 Dialog 视觉层（用户端重构规范 V4 §21）
 *
 * 结构（§21.2）：
 *   [Type Icon] Title                         [×]
 *   ────────────────────────────────────────────
 *   Body / Details / Input / Fields / Acknowledge
 *   ────────────────────────────────────────────
 *                            Cancel   Primary
 *
 * **业务语义一行都不在这里**：queue、confirmWord、fields、acknowledge、Escape/遮罩禁用、
 * 结果编码全部由 `@chain/ui/dialog-core` 的 `useDialogRequestState()` 提供。
 * 本文件只把这些状态画出来——这是 §21.0 拆分的目的：
 * 用户端与后台端不可能出现"同一个弹窗两种关闭规则"。
 *
 * 动画所有权（§9.6）：Modal 的进出场由本 renderer 独占。
 * Ant 默认的 zoom 动效（scale .2→1）不符合规范 §21.3 的
 * `scale .96→1 / y 10→0 / 220ms`，因此改用自定义 transitionName，
 * 关键帧定义在 `styles/motion.css`。
 *
 * Exit 动画的取舍：请求 resolve 后 Provider 会立刻卸载 renderer，
 * 因此这里**不做**退场动画。这是刻意的——退场动画必须把 resolve 延后 160ms，
 * 而每一次 `await dialog.confirm()` 后面都跟着真实的服务端写操作，
 * 为了装饰性动画给每个业务动作加 160ms 延迟不划算（规范 §64 #9 的取向一致：
 * 确认流程的可靠性优先于观感）。队列本身仍然保证同一时刻只有一个弹窗（§21.4）。
 */
export function UserDialogRenderer({ request, onResolve }: DialogRendererProps) {
  /*
   * 焦点归还（§55：dialog focus restore）。
   *
   * 这是**真实缺陷修复**，不是装饰：antd Modal 的 `focusTriggerAfterClose` 在本项目里
   * 会把焦点还原到 `<body>`（弹窗打开时 activeElement 是 rc-drawer 的包装层，
   * 而不是被点的按钮）。后果是——从技术详情 Drawer 里打开一个说明弹窗、
   * 用 Escape 关掉它之后，焦点落在 body 上，而 rc-drawer 的 ESC 监听依赖
   * "焦点在抽屉内部"，于是**用户再按 Escape 关不掉抽屉了**，必须先用鼠标点一下抽屉。
   *
   * 由本 renderer 独占焦点还原（同时关掉 antd 自己的还原，避免两个所有者打架），
   * 行为与 legacy renderer 的 `previouslyFocused` 完全一致。
   */
  const previouslyFocused = React.useRef<Element | null>(null);
  React.useEffect(() => {
    previouslyFocused.current = document.activeElement;
    return () => {
      const element = previouslyFocused.current;
      if (element instanceof HTMLElement && document.contains(element)) {
        element.focus();
      }
    };
  }, []);

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
    canCancel,
    meta,
  } = state;

  const wide = Boolean(request.details && request.details.length > 4);

  return (
    <Modal
      open
      centered
      keyboard={canCancel}
      maskClosable={canCancel}
      /* 焦点还原由上面的 effect 独占（只有一个动画/焦点所有者，§9.6 的同一原则）。 */
      focusTriggerAfterClose={false}
      /* 关闭按钮自己渲染（见下），这样才能做到"可见但 disabled + title 说明原因"。 */
      closable={false}
      footer={null}
      width={wide ? 720 : 520}
      transitionName="cs-dialog"
      maskTransitionName="cs-dialog-mask"
      onCancel={cancel}
      rootClassName="cs-dialog-root"
      className="cs-dialog"
      data-kind={request.kind}
      aria-labelledby="cs-dialog-title"
    >
      <header className="cs-dialog__head">
        <span className={`cs-dialog__kind cs-dialog__kind--${meta.tone}`} aria-hidden="true">
          {meta.icon}
        </span>
        {/* 类型同时以文字暴露给读屏：形状是给眼睛的，文字是给所有人的。 */}
        <span className="visually-hidden">{meta.label}</span>
        <h2 className="cs-dialog__title" id="cs-dialog-title">
          {request.title}
        </h2>
        <button
          type="button"
          className="cs-iconbtn cs-dialog__close"
          aria-label="关闭对话框"
          disabled={!canCancel}
          title={ackPending ? "请先勾选确认后再关闭" : undefined}
          onClick={() => onResolve(null)}
        >
          <X size={17} />
        </button>
      </header>

      <div className="cs-dialog__body">
        {request.body ? <div>{request.body}</div> : null}

        {request.details && request.details.length > 0 ? (
          <dl className="dialog__kv" style={{ marginTop: request.body ? "var(--sp-4)" : 0 }}>
            {request.details.map((detail) => (
              <React.Fragment key={detail.label}>
                <dt>{detail.label}</dt>
                <dd>{detail.value}</dd>
              </React.Fragment>
            ))}
          </dl>
        ) : null}

        {needsInput ? (
          <div className="field" style={{ marginTop: "var(--sp-4)" }}>
            <label className="field__label" htmlFor="cs-dialog-input">
              {request.input!.label}
            </label>
            <input
              id="cs-dialog-input"
              className="input"
              autoComplete="off"
              aria-invalid={!inputOk}
              placeholder={request.input!.placeholder}
              value={inputValue}
              onChange={(event) => setInputValue(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && inputOk) {
                  event.preventDefault();
                  onResolve(`input:${inputValue.trim()}`);
                }
              }}
            />
            {!inputOk && request.input!.patternHint ? (
              <span className="field__hint field__hint--error">{request.input!.patternHint}</span>
            ) : null}
          </div>
        ) : null}

        {needsFields ? (
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)", marginTop: "var(--sp-4)" }}>
            {formFields.map((field) => {
              const id = `cs-dialog-field-${field.name}`;
              const value = formValues[field.name] ?? "";
              const hint =
                field.patternHint && field.pattern && !field.pattern.test(value.trim()) ? (
                  <span className="field__hint field__hint--error">{field.patternHint}</span>
                ) : null;
              if (field.type === "checkbox") {
                return (
                  <div className="field" key={field.name}>
                    <label className="cs-dialog__check" htmlFor={id}>
                      <input
                        id={id}
                        type="checkbox"
                        checked={value === "true"}
                        onChange={(event) => setFormValue(field.name, event.target.checked ? "true" : "")}
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
                    required={field.required !== false}
                    placeholder={field.placeholder}
                    value={value}
                    onChange={(event) => setFormValue(field.name, event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" && fieldsOk) {
                        event.preventDefault();
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
            <label className="field__label" htmlFor="cs-dialog-confirm-word">
              请输入 <span className="mono">{request.confirmWord}</span> 以继续
            </label>
            <input
              id="cs-dialog-confirm-word"
              className="input mono"
              autoComplete="off"
              value={word}
              onChange={(event) => setWord(event.target.value)}
            />
            {/*
              confirmWord 规则（§21.5）：未匹配时主按钮保持 disabled，
              匹配后由 disabled 淡入 active；**不自动提交**。
              这里只表达"当前是否已匹配"，判定本身在 Core 里。
            */}
            <span className="field__hint">{word.trim() === request.confirmWord ? "确认词已匹配" : "尚未匹配"}</span>
          </div>
        ) : null}

        {requiresAck ? (
          <div className="field" style={{ marginTop: "var(--sp-4)" }}>
            <label className="cs-dialog__check">
              <input
                id="cs-dialog-acknowledge"
                type="checkbox"
                checked={acked}
                onChange={(event) => setAck(event.target.checked)}
              />
              <span className="field__label" style={{ margin: 0 }}>
                {request.acknowledge!.label}
              </span>
            </label>
            {request.acknowledge!.hint ? <span className="field__hint">{request.acknowledge!.hint}</span> : null}
          </div>
        ) : null}
      </div>

      <footer className="cs-dialog__foot">
        {actions.map((action) => (
          <CsButton
            key={action.label}
            size="middle"
            variant={action.variant === "primary" ? "primary" : action.variant === "danger" ? "danger" : "secondary"}
            title={action.disabled ? action.title : undefined}
            disabled={busy || ackPending || actionDisabled(action)}
            onClick={() => commit(action)}
          >
            {action.label}
          </CsButton>
        ))}
      </footer>
    </Modal>
  );
}
