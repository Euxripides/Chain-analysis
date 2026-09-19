import { useCallback, useEffect, useState } from "react";
import { useAuth } from "@chain/api-client";
import {
  Button,
  Card,
  DataTable,
  Field,
  PageSection,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import { formatTime, useAction, useAsync } from "../../sprint4/common";

/**
 * 案件成员 Tab（本轮从 CaseDetailPage 拆出）
 *
 * 拆出的理由不是"文件太长"，而是**数据最小化**：
 * 成员列表此前挂在父级详情页上，只要用户打开案件详情（哪怕停在概览），
 * `GET /api/v2/cases/{id}/members` 就会被请求一次。
 * 现在它跟着 Tab 走：不进入成员 Tab，就不发这个请求。
 *
 * 权限同样是分离的（与服务端 x-required-permission 一致）：
 *   · 进入该 Tab 需要 `case:member:list`（路由守卫负责，见 app/case-tabs.ts）；
 *   · 添加 / 移除需要 `case:member:manage`，只有查看权限的人看不到这两个入口。
 * 注意"看不到按钮"仍然只是 UI 层，服务端才是安全边界。
 */

interface Member {
  id: string;
  identity_id: string;
  email: string;
  display_name: string;
  role: string;
  created_at: string;
}

interface MemberOption {
  identity_id: string;
  email: string;
  display_name: string;
}

export function CaseMembersTab({ caseId }: { caseId: string }) {
  const { can } = useAuth();
  const dialog = useDialog();
  const action = useAction();
  const canManage = can("case:member:manage");

  const { data: members, loading, error, reload } = useAsync<Member[]>(`/api/v2/cases/${caseId}/members`, [caseId]);
  const [adding, setAdding] = useState(false);
  const [options, setOptions] = useState<MemberOption[]>([]);
  const [form, setForm] = useState({ identity_id: "", role: "investigator" });

  /* 候选成员只在真正点开"添加成员"时才拉取，而且是 manage 权限的前提下。 */
  useEffect(() => {
    if (!adding || !canManage) return;
    void action.client
      .get<MemberOption[]>("/api/v2/workspace-members", { query: { limit: 200 } })
      .then((res) => setOptions(res.data ?? []))
      .catch(() => setOptions([]));
  }, [adding, canManage, action.client]);

  const removeMember = useCallback(
    async (member: Member) => {
      const answer = await dialog.confirm({
        kind: "confirm",
        title: "移除案件成员？",
        details: [
          { label: "成员", value: member.display_name || member.email },
          { label: "角色", value: member.role },
        ],
      });
      if (answer !== "confirm") return;
      await action.run(() => action.client.delete(`/api/v2/cases/${caseId}/members/${member.identity_id}`), {
        successTitle: "成员已移除",
        onDone: () => reload(),
      });
    },
    [action, caseId, dialog, reload],
  );

  const rows = members ?? [];

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
      <PageSection title="案件成员" count={`${rows.length} 人`}>
        <Card
          flush
          actions={
            canManage ? (
              <Button size="sm" onClick={() => setAdding(true)}>
                添加成员
              </Button>
            ) : null
          }
        >
          <DataTable<Member>
            loading={loading}
            error={error}
            onRetry={reload}
            rows={rows}
            rowKey={(member) => member.id}
            caption="案件成员"
            columns={[
              {
                key: "name",
                header: "成员",
                render: (member) => (
                  <span>
                    <strong>{member.display_name || "—"}</strong>
                    <div className="s4-list__sub">{member.email}</div>
                  </span>
                ),
              },
              { key: "role", header: "角色", width: "120px", render: (member) => <StatusBadge tone={member.role === "lead" ? "ok" : "neutral"}>{member.role}</StatusBadge> },
              { key: "joined", header: "加入时间", width: "170px", render: (member) => <span>{formatTime(member.created_at)}</span> },
              {
                key: "ops",
                header: "操作",
                width: "100px",
                /* 没有 manage 权限时整列不渲染操作按钮，而不是渲染一个禁用的按钮：
                   禁用的按钮会让人以为"我差点就能点了"，而这本来就是不该出现的操作。 */
                render: (member) =>
                  canManage ? (
                    <Button size="sm" onClick={() => removeMember(member)}>
                      移除
                    </Button>
                  ) : null,
              },
            ]}
          />
        </Card>
      </PageSection>

      {adding && canManage ? (
        <PageSection title="添加案件成员">
          <Card>
            <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)" }}>
              <Field label="成员" htmlFor="member-id">
                <select id="member-id" className="select" value={form.identity_id} onChange={(e) => setForm({ ...form, identity_id: e.target.value })}>
                  <option value="">选择工作区成员</option>
                  {options.map((option) => (
                    <option key={option.identity_id} value={option.identity_id}>
                      {option.display_name || option.email}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="角色" htmlFor="member-role">
                <select id="member-role" className="select" value={form.role} onChange={(e) => setForm({ ...form, role: e.target.value })}>
                  <option value="lead">lead</option>
                  <option value="investigator">investigator</option>
                  <option value="reviewer">reviewer</option>
                  <option value="viewer">viewer</option>
                </select>
              </Field>
              <div className="toolbar" style={{ justifyContent: "flex-end" }}>
                <Button onClick={() => setAdding(false)}>取消</Button>
                <Button
                  variant="primary"
                  disabled={!form.identity_id}
                  busy={action.busy}
                  busyLabel="加入中…"
                  onClick={() =>
                    action.run(() => action.client.post(`/api/v2/cases/${caseId}/members`, form), {
                      successTitle: "成员已加入",
                      onDone: () => {
                        setAdding(false);
                        setForm({ identity_id: "", role: "investigator" });
                        void reload();
                      },
                    })
                  }
                >
                  加入
                </Button>
              </div>
            </div>
          </Card>
        </PageSection>
      ) : null}
    </div>
  );
}
