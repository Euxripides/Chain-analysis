// A-VERSION-01 版本与迁移（路由 /system/version · 权限 admin:version:read）。
//
// 构建 SHA / 运行环境 / Go 版本 / schema 迁移水位 / 组件矩阵 / 最近迁移。
// 约束（AGENTS.md / docs/code-standards.md / AD-009）：正文只放业务数据、字段标签与操作控件，
// 说明 / 警告 / 成功 / 错误 / 风险 / 确认一律通过 useDialog() 弹窗。

import { useMemo, type ReactNode } from "react";
import { Card, DataTable, EmptyState, ErrorState, EvidenceRail, Metric, PageHeader, SkeletonRows, StatusBadge, useDialog } from "@chain/ui";
import { KeyValueList } from "../components/s6";
import { fmtDateTime, fmtNumber, useAdminResource } from "../lib/adminApi";

const PERM_READ = "admin:version:read";

interface MigrationRow {
  name?: string;
  applied_at?: string;
}

interface SystemVersion {
  build_sha?: string;
  environment?: string;
  go_version?: string;
  schema_migrations_max?: string;
  schema_migration_count?: number;
  recent_migrations?: MigrationRow[];
  components?: Record<string, unknown>;
  as_of?: string;
}

interface ComponentCell {
  component: string;
  field: string;
  value: string;
}

const COMPONENT_LABELS: Record<string, string> = {
  api: "API 进程",
  worker: "Worker 进程",
  frontend: "前端",
  storage: "存储",
};

const FIELD_LABELS: Record<string, string> = {
  env: "运行环境",
  auth_mode: "认证模式",
  status: "状态",
  admin_base: "后台基址",
  user_base: "用户端基址",
  postgres_migrations: "PostgreSQL 迁移数",
  clickhouse_tables: "ClickHouse 表数",
  raw_bucket: "原始数据桶",
  export_bucket: "导出桶",
  backups_bucket: "备份桶",
};

function text(value: unknown): string {
  return typeof value === "string" && value !== "" ? value : "—";
}

function cellValue(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

export default function SystemVersionPage() {
  const dialog = useDialog();
  const version = useAdminResource<SystemVersion>("/api/admin/v1/system/version");
  const data = version.data;

  const buildSha = typeof data?.build_sha === "string" && data.build_sha !== "" ? data.build_sha : "";
  const components = useMemo(() => (data?.components ?? {}) as Record<string, unknown>, [data]);

  const componentRows = useMemo(() => {
    const rows: ComponentCell[] = [];
    Object.entries(components).forEach(([name, value]) => {
      if (value === null || value === undefined) {
        rows.push({ component: name, field: "—", value: "—" });
        return;
      }
      if (typeof value !== "object") {
        rows.push({ component: name, field: "—", value: String(value) });
        return;
      }
      Object.entries(value as Record<string, unknown>).forEach(([field, fieldValue]) => {
        rows.push({ component: name, field, value: cellValue(fieldValue) });
      });
    });
    return rows;
  }, [components]);

  const workerNote = (() => {
    const worker = components.worker;
    if (worker && typeof worker === "object" && "note" in worker) {
      return text((worker as Record<string, unknown>).note);
    }
    return "—";
  })();

  const showCaliber = () => {
    dialog.notify("info", "版本信息口径", null, [
      { label: "接口", value: "GET /api/admin/v1/system/version" },
      { label: "build_sha 来源", value: "进程环境变量 BUILD_SHA；未注入时显示「未注入」" },
      { label: "schema 水位", value: "schema_migrations 中按 name 倒序的第一条" },
      { label: "迁移条数", value: "SELECT count(*) FROM schema_migrations" },
      { label: "ClickHouse 表数", value: "system.tables where database='blockchain'" },
      { label: "Worker 状态", value: workerNote },
      { label: "读取时间", value: fmtDateTime(data?.as_of) },
    ]);
  };

  return (
    <div className="workbench">
      <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
        <PageHeader
          title="版本与迁移"
          subtitle={
            <>
              读取 {PERM_READ} · 数据截至 <span className="mono">{fmtDateTime(data?.as_of)}</span>
            </>
          }
          actions={
            <>
              <button type="button" className="btn btn--sm" onClick={showCaliber}>
                口径
              </button>
              <button type="button" className="btn btn--sm" onClick={version.reload}>
                刷新
              </button>
            </>
          }
        />

        {version.loading && !data ? (
          <SkeletonRows rows={4} columns={4} />
        ) : version.error && !data ? (
          <Card>
            <ErrorState title="版本信息加载失败" message={version.error} onRetry={version.reload} />
          </Card>
        ) : !data ? (
          <Card>
            <EmptyState title="没有版本信息" />
          </Card>
        ) : (
          <>
            <Card title="构建与运行">
              <div className="metric-grid">
                <Metric
                  label="build_sha"
                  value={<span className="mono" style={{ fontSize: "var(--fs-16)" }}>{buildSha === "" ? "未注入" : buildSha}</span>}
                  tone={buildSha === "" ? "warn" : "ok"}
                  hint="环境变量 BUILD_SHA"
                />
                <Metric label="environment" value={text(data.environment)} tone={data.environment === "production" ? "ok" : "warn"} />
                <Metric label="go_version" value={<span className="mono" style={{ fontSize: "var(--fs-16)" }}>{text(data.go_version)}</span>} tone="neutral" />
                <Metric
                  label="schema_migrations_max"
                  value={<span className="mono" style={{ fontSize: "var(--fs-16)" }}>{data.schema_migrations_max ? data.schema_migrations_max : "无迁移记录"}</span>}
                  tone={data.schema_migrations_max ? "ok" : "warn"}
                  hint={"已应用迁移 " + fmtNumber(data.schema_migration_count) + " 条"}
                />
              </div>
            </Card>

            <Card title="组件矩阵" flush>
              <DataTable<ComponentCell>
                rows={componentRows}
                rowKey={(row) => row.component + ":" + row.field}
                empty={<EmptyState title="没有组件信息" />}
                columns={[
                  {
                    key: "component",
                    header: "组件",
                    width: "160px",
                    render: (row) => (
                      <span>
                        <div>{COMPONENT_LABELS[row.component] ?? row.component}</div>
                        <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{row.component}</div>
                      </span>
                    ),
                  },
                  { key: "field", header: "字段", width: "200px", render: (row) => FIELD_LABELS[row.field] ?? row.field },
                  {
                    key: "value",
                    header: "值",
                    render: (row) => <span className="mono" style={{ wordBreak: "break-word" }}>{row.value}</span>,
                  },
                ]}
              />
            </Card>

            <Card title={"最近迁移（" + fmtNumber((data.recent_migrations ?? []).length) + "）"} flush>
              <DataTable<MigrationRow>
                rows={data.recent_migrations ?? []}
                rowKey={(row, index) => (row.name ?? "migration") + ":" + String(index)}
                empty={<EmptyState title="没有迁移记录" />}
                columns={[
                  { key: "name", header: "迁移", render: (row) => <span className="mono">{row.name ?? "—"}</span> },
                  { key: "applied", header: "应用时间", width: "220px", render: (row) => fmtDateTime(row.applied_at) },
                ]}
              />
            </Card>

            <Card title="存储与桶">
              <KeyValueList
                items={[
                  { label: "PostgreSQL 迁移数", value: fmtNumber(readComponent(components, "storage", "postgres_migrations")) },
                  { label: "ClickHouse 表数", value: fmtNumber(readComponent(components, "storage", "clickhouse_tables")) },
                  { label: "原始数据桶", value: text(readComponent(components, "storage", "raw_bucket")), mono: true },
                  { label: "导出桶", value: text(readComponent(components, "storage", "export_bucket")), mono: true },
                  { label: "备份桶", value: text(readComponent(components, "storage", "backups_bucket")), mono: true },
                  { label: "后台基址", value: text(readComponent(components, "frontend", "admin_base")), mono: true },
                  { label: "用户端基址", value: text(readComponent(components, "frontend", "user_base")), mono: true },
                  { label: "API 认证模式", value: text(readComponent(components, "api", "auth_mode")), mono: true },
                ]}
              />
            </Card>
          </>
        )}
      </div>

      <EvidenceRail
        title="版本证据链"
        items={[
          { label: "构建 SHA", value: buildSha === "" ? "未注入" : buildSha },
          { label: "运行环境", value: text(data?.environment) },
          { label: "Go 版本", value: text(data?.go_version) },
          { label: "schema 水位", value: data?.schema_migrations_max || "无迁移记录" },
          { label: "迁移条数", value: fmtNumber(data?.schema_migration_count) },
          { label: "最近迁移", value: text(data?.recent_migrations?.[0]?.name) },
          { label: "最近迁移时间", value: fmtDateTime(data?.recent_migrations?.[0]?.applied_at) },
          { label: "读取时间", value: fmtDateTime(data?.as_of) },
          { label: "状态", value: <StatusBadge tone={buildSha === "" ? "warn" : "ok"}>{buildSha === "" ? "SHA 未注入" : "SHA 已注入"}</StatusBadge> },
        ]}
      />
    </div>
  );
}

function readComponent(components: Record<string, unknown>, component: string, field: string): unknown {
  const value = components[component];
  if (!value || typeof value !== "object") return undefined;
  return (value as Record<string, unknown>)[field];
}
