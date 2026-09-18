import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError, useAuth } from "@chain/api-client";
import { Button, Card, Field, PageHeader, PageSection, StickyActionBar, useDialog } from "@chain/ui";

/**
 * §12.5 新建案件 /cases/new
 *
 * 请求完全不变：`POST /api/v2/cases`，请求体仍是 { name, case_no, description,
 * classification, priority, status }，仍然带 Idempotency-Key，成功后仍然先弹出
 * 成功 Dialog 再跳转 `/cases/{id}/overview`（审计与跳转行为保持原样）。
 *
 * 表单只收集接口当前已有的字段；`member_ids` / `tag_ids` 虽然服务端接受，
 * 但本次重构**不新增请求字段**，因此界面上不出现成员/标签控件。
 */

interface CaseForm {
  name: string;
  case_no: string;
  description: string;
  classification: string;
  priority: string;
  status: string;
}

export default function CaseNewPage() {
  const { client } = useAuth();
  const dialog = useDialog();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  /** 校验错误靠近字段显示（蓝图 §12.5）；服务端返回的错误仍走 Dialog。 */
  const [nameError, setNameError] = useState<string | null>(null);
  const [form, setForm] = useState<CaseForm>({
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
      setNameError("案件名称为必填项，长度 1-200 个字符。");
      dialog.notify("warning", "请填写案件名称", "名称为 1-200 个字符。");
      return;
    }
    setNameError(null);
    const answer = await dialog.confirm({
      kind: "confirm",
      title: "创建案件？",
      body: `将在当前工作区创建「${form.name}」，并把你加入为 lead 成员。案件状态、成员和对象都会写入审计与业务时间线。`,
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
    <div className="cs-page--reading" style={{ display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
      <PageHeader
        title="新建案件"
        breadcrumb={[{ label: "工作台", to: "/home" }, { label: "案件中心", to: "/cases" }, { label: "新建案件" }]}
      />

      <form onSubmit={submit}>
        <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
          <PageSection title="基本信息">
            <Card>
              <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)" }}>
                <Field label="案件名称" htmlFor="case-name" required error={nameError ?? undefined}>
                  <input
                    id="case-name"
                    className="input"
                    required
                    maxLength={200}
                    aria-invalid={nameError ? true : undefined}
                    value={form.name}
                    onChange={(e) => {
                      setForm({ ...form, name: e.target.value });
                      if (nameError) setNameError(null);
                    }}
                  />
                </Field>
                <Field label="案件编号" htmlFor="case-no" description="留空时由服务端生成。">
                  <input id="case-no" className="input" maxLength={128} value={form.case_no} onChange={(e) => setForm({ ...form, case_no: e.target.value })} />
                </Field>
                <Field label="调查目的说明" htmlFor="case-desc">
                  <textarea id="case-desc" className="textarea" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
                </Field>
              </div>
            </Card>
          </PageSection>

          <PageSection title="范围与状态">
            <Card>
              <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)" }}>
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
              </div>
            </Card>
          </PageSection>

          {/* 提交区：吸底操作栏（移动端自动避开 safe area）。 */}
          <StickyActionBar>
            <Button variant="ghost" onClick={() => navigate("/cases")}>
              取消
            </Button>
            <Button type="submit" variant="primary" busy={busy} busyLabel="正在创建…">
              创建案件
            </Button>
          </StickyActionBar>
        </div>
      </form>
    </div>
  );
}
