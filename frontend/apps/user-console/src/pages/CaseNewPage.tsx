import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError, useAuth } from "@chain/api-client";
import { Card, Field, PageHeader, useDialog } from "@chain/ui";

/** §18.1 U-CASE-02：新建案件。表单只收集业务字段，提交走 POST /api/v2/cases。 */
export default function CaseNewPage() {
  const { client } = useAuth();
  const dialog = useDialog();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState({
    name: "",
    case_no: "",
    description: "",
    classification: "internal",
    priority: "medium",
    status: "active",
  });

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name.trim()) {
      dialog.notify("warning", "请填写案件名称", "名称为 1-200 个字符。");
      return;
    }
    const answer = await dialog.confirm({
      kind: "confirm",
      title: "创建案件？",
      body: `将在当前工作区创建「${form.name}」，并把你加入为 lead 成员。`,
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "创建", result: "create", variant: "primary" },
      ],
    });
    if (answer !== "create") return;
    setBusy(true);
    try {
      const res = await client.post<{ id: string; name: string }>("/api/v2/cases", form, { idempotencyKey: crypto.randomUUID() });
      // 必须等成功 Dialog 关闭后再跳转：否则遮罩会盖住新页面并拦截后续交互。
      await dialog.open({
        kind: "success",
        title: "案件已创建",
        body: `案件编号 ${form.case_no || "（服务端自动生成）"}`,
        details: [
          { label: "案件 ID", value: res.data.id },
          { label: "名称", value: res.data.name },
        ],
        actions: [{ label: "查看案件", result: "ok", variant: "primary" }],
      });
      navigate(`/cases/${res.data.id}/overview`);
    } catch (err) {
      dialog.notify("error", "创建失败", err instanceof ApiError ? err.userMessage : "请稍后重试", err instanceof ApiError && err.traceId ? [{ label: "Trace", value: err.traceId }] : undefined);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <PageHeader title="新建案件" subtitle="案件状态、成员和对象都会写入审计与业务时间线。" />
      <Card>
        <form className="s4-form-grid" onSubmit={submit}>
          <Field label="案件名称" htmlFor="case-name">
            <input id="case-name" className="input" required maxLength={200} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </Field>
          <Field label="案件编号" htmlFor="case-no">
            <input id="case-no" className="input" maxLength={128} value={form.case_no} onChange={(e) => setForm({ ...form, case_no: e.target.value })} />
          </Field>
          <Field label="密级" htmlFor="case-class">
            <select id="case-class" className="select" value={form.classification} onChange={(e) => setForm({ ...form, classification: e.target.value })}>
              <option value="public">public</option>
              <option value="internal">internal</option>
              <option value="confidential">confidential</option>
              <option value="restricted">restricted</option>
            </select>
          </Field>
          <Field label="优先级" htmlFor="case-priority">
            <select id="case-priority" className="select" value={form.priority} onChange={(e) => setForm({ ...form, priority: e.target.value })}>
              <option value="low">low</option>
              <option value="medium">medium</option>
              <option value="high">high</option>
              <option value="critical">critical</option>
            </select>
          </Field>
          <Field label="初始状态" htmlFor="case-status">
            <select id="case-status" className="select" value={form.status} onChange={(e) => setForm({ ...form, status: e.target.value })}>
              <option value="draft">draft</option>
              <option value="active">active</option>
            </select>
          </Field>
          <div className="field field--wide">
            <Field label="调查目的说明" htmlFor="case-desc">
              <textarea id="case-desc" className="textarea" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
            </Field>
          </div>
          <div className="s4-sticky-actions field--wide">
            <button type="button" className="btn" onClick={() => navigate("/cases")}>
              取消
            </button>
            <button type="submit" className="btn btn--primary" disabled={busy}>
              {busy ? "正在创建…" : "创建案件"}
            </button>
          </div>
        </form>
      </Card>
    </>
  );
}
