import { useMemo, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { Drawer } from "antd";
import {
  Button,
  Card,
  CompactProgress,
  DataTable,
  EmptyState,
  HashValue,
  HierarchicalProgress,
  KeyValueList,
  PageSection,
  PipelineProgress,
  StatusBadge,
  StatusBadgeForState,
  TabPanel,
  Tabs,
  Timeline,
  progressStateFor,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import type { HierarchicalNode, PipelineStage, TimelineEntry } from "@chain/ui";
import { formatBytes, formatRawAmount, formatTime } from "../../../sprint5/common";
import type { JobEvent } from "../../../sprint5/common";
import { modeReasonText, type ProviderRuntimeState } from "../../downloads/download.types";
import { rollupState, type AddressLayer, type DatasetLayer, type JobDetailData, type JobExport, type JobProgress, type JobRecord, type ShardLayer } from "./types";

/**
 * 技术详情（用户端重构规范 V4 §28.3 / §47）
 *
 * 这里**原样保留**了重构前 `/jobs/:jobId` 的全部诊断能力：
 *   Pipeline / 地址进度（分层 + 按地址核对表）/ Dataset / Shards / Provider 运行 /
 *   结构化事件 / Exports / 证据链，外加"概览"里的模式与时间键值。
 *
 * 「Provider 运行」（开发文档 §13.2）按 Provider × API 通道展示运行时档位：
 * 请求/实际模式、Shard 并发、请求并发、RPM/RPS、CU/分钟、已用请求、已用 CU、估算成本与降级原因。
 * **Data API 页数与 RPC 详情调用数是两个口径**，字段标签按通道区分，并在"计数口径"弹窗里说明。
 *
 * 唯一的变化是**信息层级**：它们不再占据首屏，而是收进一个默认关闭的 Drawer。
 * 规范 §64 #23 明确禁止在简化首屏时删除这些能力——排障人员仍然要能看到
 * last_event_id、shard block range、provider mode/reason、trace id 和原始字段。
 *
 * 为什么不给技术详情加 URL key（§28.3）：当前页面没有 tab 深链接契约，
 * 新增一个 key 等于新增一个对外契约。因此用本地状态 + Drawer。
 */

export interface JobTechnicalDetailsProps {
  open: boolean;
  onClose: () => void;
  detail: JobDetailData;
  job: JobRecord;
  /** 节流后的进度快照（与原实现共用同一份 throttled 值）。 */
  shown: JobProgress;
  status: string;
  events: JobEvent[];
  stream: { connected: boolean; lastEventID: number | string | null | undefined };
  lastTrace: string | null;
  busy: boolean;
  expandAll: boolean;
  onToggleExpandAll: () => void;
  onRefresh: () => void;
  onClearEvents: () => void;
  onDownloadExport: (item: JobExport) => void;
  onOpenExports: () => void;
  initialTab?: string;
  /**
   * 排障入口工具条（失败原因 / rows-s 口径 / Provider 说明）。
   *
   * 这些是**说明性内容**，按仓库规范只能通过弹窗呈现，因此这里只放触发按钮，
   * 正文由调用方用 `useDialog()` 打开。放在技术详情里而不是首屏，
   * 是为了让首屏保持 §28.2 的字段层级。
   */
  topActions?: ReactNode;
}

export function JobTechnicalDetails({
  open,
  onClose,
  detail,
  job,
  shown,
  status,
  events,
  stream,
  lastTrace,
  busy,
  expandAll,
  onToggleExpandAll,
  onRefresh,
  onClearEvents,
  onDownloadExport,
  onOpenExports,
  initialTab = "pipeline",
  topActions,
}: JobTechnicalDetailsProps) {
  const dialog = useDialog();
  const addressLayers = detail.layers.addresses ?? [];
  const datasetLayers = detail.layers.datasets ?? [];
  const shardLayers = detail.layers.shards ?? [];
  /* 每个 Provider 通道的运行时档位（开发文档 §6.2）：旧后端不返回时为 []，显示空态。 */
  const runtimeStates = detail.provider_runtime_states ?? [];

  /** 成功游标：取所有地址层里最新的一个 checkpoint_at（服务端时间，不做推断）。 */
  const latestCursor = useMemo(() => {
    let best: string | null = null;
    let bestMs = Number.NEGATIVE_INFINITY;
    for (const layer of addressLayers) {
      const at = layer.successful_cursor?.checkpoint_at;
      if (!at) continue;
      const parsed = Date.parse(at);
      if (Number.isFinite(parsed) && parsed > bestMs) {
        bestMs = parsed;
        best = at;
      }
    }
    return best;
  }, [addressLayers]);

  const totalShards = shown.total_shards ?? 0;
  const doneShards = shown.done_shards ?? 0;

  /* 展示用索引与求和：只做分组与加法，不产生任何比率（比率一律取服务端 progress）。 */
  const { datasetsBySubject, shardsBySubject, rowsBySubject, addressBySubject } = useMemo(() => {
    const datasets = new Map<string, DatasetLayer[]>();
    const shards = new Map<string, ShardLayer[]>();
    const rows = new Map<string, number>();
    const addresses = new Map<string, AddressLayer>();
    for (const layer of addressLayers) addresses.set(layer.subject_id, layer);
    for (const layer of datasetLayers) {
      const list = datasets.get(layer.subject_id);
      if (list) list.push(layer);
      else datasets.set(layer.subject_id, [layer]);
    }
    for (const layer of shardLayers) {
      const list = shards.get(layer.subject_id);
      if (list) list.push(layer);
      else shards.set(layer.subject_id, [layer]);
      rows.set(layer.subject_id, (rows.get(layer.subject_id) ?? 0) + (layer.downloaded_rows ?? 0));
    }
    return { datasetsBySubject: datasets, shardsBySubject: shards, rowsBySubject: rows, addressBySubject: addresses };
  }, [addressLayers, datasetLayers, shardLayers]);

  const pipelineStages: PipelineStage[] = [
    {
      key: "job",
      label: "任务受理与调度",
      state: progressStateFor(job.status),
      meta: job.status + " · effective_mode " + (detail.performance.effective_mode || job.effective_mode || "—"),
    },
    {
      key: "addresses",
      label: "地址层",
      state: rollupState(addressLayers.map((layer) => layer.status)),
      meta:
        addressLayers.length + " 个地址 · " +
        addressLayers.filter((layer) => progressStateFor(layer.status) === "completed").length + " 个已完成",
    },
    {
      key: "datasets",
      label: "Dataset 层",
      state: rollupState(datasetLayers.map((layer) => layer.status)),
      meta:
        datasetLayers.length + " 条 Dataset 进度 · " +
        datasetLayers.filter((layer) => layer.shards.failed > 0).length + " 条存在失败分片",
    },
    {
      key: "shards",
      label: "分片下载",
      state: rollupState(shardLayers.map((layer) => layer.status)),
      meta: "完成 " + doneShards + "/" + totalShards + " · 共 " + shardLayers.length + " 个分片",
    },
    {
      key: "exports",
      label: "导出",
      state: rollupState(detail.exports.map((item) => item.status)),
      meta: detail.exports.length + " 个关联导出",
    },
  ];

  const hierarchicalNodes: HierarchicalNode[] = addressLayers.map((layer, index) => {
    const subjectDatasets = datasetsBySubject.get(layer.subject_id) ?? [];
    const subjectShards = shardsBySubject.get(layer.subject_id) ?? [];
    const cursorAt = layer.successful_cursor?.checkpoint_at;
    return {
      key: layer.subject_id,
      label: layer.address,
      percent: Math.round(Math.max(0, Math.min(1, layer.progress)) * 100),
      state: progressStateFor(layer.status),
      defaultExpanded: expandAll,
      meta:
        "#" + (index + 1) + " · " + layer.status + " · 成功游标 " + (cursorAt ? formatTime(cursorAt) : "—") +
        " · rows " + formatRawAmount(String(rowsBySubject.get(layer.subject_id) ?? 0), 0),
      children: subjectDatasets.map((dataset) => ({
        key: dataset.dataset_id,
        label: dataset.dataset,
        percent: Math.round(Math.max(0, Math.min(1, dataset.progress)) * 100),
        state: progressStateFor(dataset.status),
        defaultExpanded: expandAll,
        meta:
          "Shard " + dataset.shards.completed + "/" + dataset.shards.total +
          (dataset.shards.failed > 0 ? " · 失败 " + dataset.shards.failed : "") +
          " · 更新 " + formatTime(dataset.updated_at),
        children: subjectShards
          .filter((shard) => shard.dataset === dataset.dataset)
          .map((shard) => ({
            key: shard.shard_id,
            label: shard.shard_id,
            percent: progressStateFor(shard.status) === "completed" ? 100 : null,
            state: progressStateFor(shard.status),
            meta:
              shard.from_block + " → " + shard.to_block + " · 重试 " + shard.retries +
              " · rows " + formatRawAmount(String(shard.downloaded_rows), 0) +
              " · " + formatBytes(shard.downloaded_bytes),
          })),
      })),
    };
  });

  const tabItems = [
    { key: "overview", label: "概览" },
    { key: "pipeline", label: "Pipeline" },
    { key: "addresses", label: "地址进度", count: addressLayers.length },
    { key: "datasets", label: "Dataset", count: datasetLayers.length },
    { key: "shards", label: "Shards", count: shardLayers.length },
    { key: "provider", label: "Provider 运行", count: runtimeStates.length },
    { key: "events", label: "结构化事件", count: events.length },
    { key: "exports", label: "Exports", count: detail.exports.length },
    { key: "evidence", label: "证据链" },
  ];

  return (
    <Drawer
      title="技术详情"
      placement="right"
      width={920}
      open={open}
      onClose={onClose}
      /* 抽屉内容只在打开时挂载：关闭后不占用任何渲染与内存。 */
      styles={{ body: { padding: 16 } }}
      data-testid="job-technical-details"
    >
      {open ? (
        <>
          {topActions ? (
            <div
              style={{
                paddingBottom: "var(--sp-3)",
                borderBottom: "1px solid var(--cs-border-subtle)",
                marginBottom: "var(--sp-3)",
              }}
            >
              {topActions}
            </div>
          ) : null}
        <TechnicalTabs
          initialTab={initialTab}
          tabItems={tabItems}
          expandAll={expandAll}
          onToggleExpandAll={onToggleExpandAll}
          onRefresh={onRefresh}
          onClearEvents={onClearEvents}
          onOpenExports={onOpenExports}
          onDownloadExport={onDownloadExport}
          dialog={dialog}
          job={job}
          shown={shown}
          status={status}
          detail={detail}
          events={events}
          stream={stream}
          lastTrace={lastTrace}
          busy={busy}
          latestCursor={latestCursor}
          pipelineStages={pipelineStages}
          hierarchicalNodes={hierarchicalNodes}
          addressLayers={addressLayers}
          datasetLayers={datasetLayers}
          shardLayers={shardLayers}
          runtimeStates={runtimeStates}
          rowsBySubject={rowsBySubject}
          addressBySubject={addressBySubject}
        />
        </>
      ) : null}
    </Drawer>
  );
}

/* ------------------------------------------------------------------------- */

interface TechnicalTabsProps {
  initialTab: string;
  tabItems: Array<{ key: string; label: string; count?: number }>;
  expandAll: boolean;
  onToggleExpandAll: () => void;
  onRefresh: () => void;
  onClearEvents: () => void;
  onOpenExports: () => void;
  onDownloadExport: (item: JobExport) => void;
  dialog: ReturnType<typeof useDialog>;
  job: JobRecord;
  shown: JobProgress;
  status: string;
  detail: JobDetailData;
  events: JobEvent[];
  stream: { connected: boolean; lastEventID: number | string | null | undefined };
  lastTrace: string | null;
  busy: boolean;
  latestCursor: string | null;
  pipelineStages: PipelineStage[];
  hierarchicalNodes: HierarchicalNode[];
  addressLayers: AddressLayer[];
  datasetLayers: DatasetLayer[];
  shardLayers: ShardLayer[];
  runtimeStates: ProviderRuntimeState[];
  rowsBySubject: Map<string, number>;
  addressBySubject: Map<string, AddressLayer>;
}

function TechnicalTabs(props: TechnicalTabsProps) {
  const {
    initialTab,
    tabItems,
    expandAll,
    onToggleExpandAll,
    onRefresh,
    onClearEvents,
    onOpenExports,
    onDownloadExport,
    dialog,
    job,
    shown,
    status,
    detail,
    events,
    stream,
    lastTrace,
    busy,
    latestCursor,
    pipelineStages,
    hierarchicalNodes,
    addressLayers,
    datasetLayers,
    shardLayers,
    runtimeStates,
    rowsBySubject,
    addressBySubject,
  } = props;

  /* Tab 选中项只在本地持有，不改路由契约（§28.3：当前页面没有 tab 深链接，不新增 URL key）。 */
  const [tab, setTab] = useState(initialTab);
  const navigate = useNavigate();
  const addressOf = (subjectId: string): ReactNode => {
    const layer = addressBySubject.get(subjectId);
    return layer ? <HashValue value={layer.address} head={12} tail={8} label="地址" /> : "—";
  };

  /**
   * 计数口径弹窗（提示性内容只走 Dialog，AGENTS.md）。
   *
   * 这张弹窗存在的唯一理由：Data API 的一次 HTTP 请求是"一页"（最多 100 条交易），
   * JSON-RPC 的一次请求是"一个方法调用"；不写清楚，用户会把 100 条交易当成 100 次请求。
   */
  const openCountingNote = (state: ProviderRuntimeState) => {
    dialog.notify("info", "计数口径", countingNote(state.api_channel), [
      { label: "Provider", value: state.provider || "—" },
      { label: "API 通道", value: state.api_channel || "—" },
      { label: "操作类别", value: state.operation_class || "—" },
      { label: usedRequestsLabel(state.api_channel), value: countText(state.used_requests) },
      { label: "已用 CU", value: countText(state.used_cu) },
    ]);
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)", minWidth: 0 }}>
      <Tabs items={tabItems} value={tab} onChange={setTab} ariaLabel="任务技术详情" />

      {tab === "overview" ? (
        <TabPanel tabKey="overview">
          <PageSection title="模式与时间">
            <Card>
              <KeyValueList
                ariaLabel="任务模式与时间"
                items={[
                  { label: "requested_mode", value: detail.performance.requested_mode || job.requested_mode || "—" },
                  { label: "effective_mode", value: detail.performance.effective_mode || job.effective_mode || "—" },
                  { label: "mode_reason", value: detail.performance.mode_reason || job.mode_reason || "—", wide: true },
                  { label: "成功游标时间", value: latestCursor ? formatTime(latestCursor) : "—" },
                  { label: "任务更新时间", value: formatTime(job.updated_at) },
                  { label: "Progress 时点", value: formatTime(shown.ts) },
                  { label: "复用策略", value: job.reuse_policy || "—" },
                  { label: "Dataset", value: job.datasets.length > 0 ? job.datasets.join("、") : "—", wide: true },
                ]}
              />
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}

      {tab === "pipeline" ? (
        <TabPanel tabKey="pipeline">
          <PageSection title="Pipeline" count={status || "—"}>
            <Card>
              <PipelineProgress stages={pipelineStages} ariaLabel="任务流水线阶段" />
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}

      {tab === "addresses" ? (
        <TabPanel tabKey="addresses">
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-5)", minWidth: 0 }}>
            <PageSection
              title="地址进度"
              count={addressLayers.length + " 个地址"}
              actions={
                <Button size="sm" onClick={onToggleExpandAll}>
                  {expandAll ? "全部收起" : "全部展开"}
                </Button>
              }
            >
              <Card>
                {addressLayers.length === 0 ? (
                  <EmptyState title="暂无地址层数据" />
                ) : (
                  <HierarchicalProgress
                    key={expandAll ? "hier-open" : "hier-closed"}
                    nodes={hierarchicalNodes}
                    ariaLabel="地址与 Dataset 分层进度"
                  />
                )}
              </Card>
            </PageSection>
            <PageSection title="按地址核对" count={addressLayers.length + " 行"}>
              <Card>
                <DataTable<AddressLayer>
                  rows={addressLayers}
                  rowKey={(row) => row.subject_id}
                  caption="每个地址的服务端状态、进度与成功游标"
                  columns={[
                    { key: "address", header: "地址", width: "220px", frozen: true, render: (row) => <HashValue value={row.address} head={12} tail={8} label="地址" /> },
                    { key: "status", header: "状态", width: "130px", render: (row) => <StatusBadgeForState value={row.status} /> },
                    {
                      key: "progress",
                      header: "进度",
                      width: "200px",
                      render: (row) => (
                        <CompactProgress
                          percent={Math.round(Math.max(0, Math.min(1, row.progress)) * 100)}
                          state={progressStateFor(row.status)}
                          label={"地址进度 " + row.status}
                        />
                      ),
                    },
                    { key: "rows", header: "rows（分片合计）", width: "160px", render: (row) => formatRawAmount(String(rowsBySubject.get(row.subject_id) ?? 0), 0) },
                    { key: "cursor", header: "成功游标", width: "180px", render: (row) => (row.successful_cursor?.checkpoint_at ? formatTime(row.successful_cursor.checkpoint_at) : "—") },
                    { key: "updated", header: "更新", width: "180px", render: (row) => formatTime(row.updated_at) },
                  ]}
                  empty={<EmptyState title="暂无地址层数据" />}
                />
              </Card>
            </PageSection>
          </div>
        </TabPanel>
      ) : null}

      {tab === "datasets" ? (
        <TabPanel tabKey="datasets">
          <PageSection title="Dataset 进度" count={datasetLayers.length + " 条"}>
            <Card>
              <DataTable<DatasetLayer>
                rows={datasetLayers}
                rowKey={(row) => row.dataset_id}
                caption="每个地址在每个 Dataset 上的状态与进度"
                columns={[
                  { key: "address", header: "地址", width: "220px", frozen: true, render: (row) => addressOf(row.subject_id) },
                  { key: "dataset", header: "Dataset", width: "150px", render: (row) => row.dataset },
                  { key: "status", header: "状态", width: "130px", render: (row) => <StatusBadgeForState value={row.status} /> },
                  {
                    key: "progress",
                    header: "进度",
                    width: "200px",
                    render: (row) => (
                      <CompactProgress
                        percent={Math.round(Math.max(0, Math.min(1, row.progress)) * 100)}
                        state={progressStateFor(row.status)}
                        label={row.dataset + " 进度"}
                      />
                    ),
                  },
                  { key: "shards", header: "Shard 完成 / 总数", width: "170px", render: (row) => row.shards.completed + " / " + row.shards.total },
                  { key: "failed", header: "失败分片", width: "110px", render: (row) => (row.shards.failed > 0 ? <StatusBadge tone="danger">{String(row.shards.failed)}</StatusBadge> : "0") },
                  { key: "updated", header: "更新", width: "180px", render: (row) => formatTime(row.updated_at) },
                ]}
                empty={<EmptyState title="暂无 Dataset 层数据" />}
              />
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}

      {tab === "shards" ? (
        <TabPanel tabKey="shards">
          <PageSection title="Shards" count={shardLayers.length + " 个分片"}>
            <Card>
              <DataTable<ShardLayer>
                rows={shardLayers}
                rowKey={(row) => row.shard_id}
                caption="每个分片的范围、分页游标、重试、行数、原始对象与错误"
                columns={[
                  { key: "shard", header: "分片", width: "180px", frozen: true, render: (row) => <HashValue value={row.shard_id} head={10} tail={6} label="分片 ID" /> },
                  { key: "address", header: "地址", width: "200px", render: (row) => addressOf(row.subject_id) },
                  { key: "dataset", header: "Dataset", width: "140px", render: (row) => row.dataset },
                  { key: "range", header: "区块范围", width: "180px", render: (row) => <span className="mono">{row.from_block + " → " + row.to_block}</span> },
                  { key: "status", header: "状态", width: "130px", render: (row) => <StatusBadgeForState value={row.status} /> },
                  {
                    key: "cursor",
                    header: "页 / cursor",
                    width: "200px",
                    render: (row) => {
                      const page = row.checkpoint?.page_no;
                      const token = row.checkpoint?.next_page_token;
                      return (
                        <span style={{ display: "inline-flex", alignItems: "center", gap: "var(--sp-2)", minWidth: 0 }}>
                          <span className="mono">{page === undefined || page === null ? "—" : "p" + String(page)}</span>
                          {token ? <HashValue value={token} head={8} tail={6} label="cursor" /> : null}
                        </span>
                      );
                    },
                  },
                  { key: "retries", header: "重试", width: "80px", render: (row) => String(row.retries) },
                  { key: "rows", header: "rows", width: "120px", render: (row) => formatRawAmount(String(row.downloaded_rows), 0) },
                  { key: "bytes", header: "字节", width: "120px", render: (row) => formatBytes(row.downloaded_bytes) },
                  { key: "raw", header: "raw object", width: "230px", render: (row) => (row.raw_object ? <HashValue value={row.raw_object} head={14} tail={10} label="raw object" /> : "—") },
                  {
                    key: "error",
                    header: "错误",
                    width: "130px",
                    render: (row) =>
                      row.error_safe ? (
                        <Button
                          size="sm"
                          onClick={() =>
                            dialog.notify("error", "Shard 错误", row.error_safe, [
                              { label: "shard_id", value: row.shard_id },
                              { label: "dataset", value: row.dataset },
                              { label: "retries", value: String(row.retries) },
                              { label: "raw_object", value: row.raw_object || "—" },
                              { label: "Trace", value: lastTrace ?? "—" },
                            ])
                          }
                        >
                          查看错误
                        </Button>
                      ) : (
                        "—"
                      ),
                  },
                ]}
                empty={<EmptyState title="暂无 Shard 层数据" />}
              />
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}

      {tab === "provider" ? (
        <TabPanel tabKey="provider">
          {runtimeStates.length === 0 ? (
            <PageSection title="Provider 运行">
              <Card>
                <EmptyState title="该任务暂无 Provider 运行状态" />
              </Card>
            </PageSection>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-5)", minWidth: 0 }}>
              {runtimeStates.map((state, index) => (
                <PageSection
                  key={state.provider + "|" + state.api_channel + "|" + (state.operation_class || "") + "|" + index}
                  title={state.provider + " · " + state.api_channel}
                  count={state.operation_class || undefined}
                  actions={
                    <Button size="sm" onClick={() => openCountingNote(state)}>
                      计数口径
                    </Button>
                  }
                >
                  <Card>
                    {/*
                     * 字段标签按通道区分计数单位（开发文档 §13.2）：Data API 的 used_requests 是
                     * **页数**，JSON-RPC 的是**方法调用数**，两者不能混成"请求数"。
                     */}
                    <KeyValueList
                      ariaLabel={state.provider + " " + state.api_channel + " 运行时档位"}
                      items={[
                        { label: "请求模式", value: state.requested_mode || "—" },
                        { label: "实际模式", value: state.effective_mode || "—" },
                        { label: "Shard 并发", value: countText(state.shard_concurrency) },
                        { label: "请求并发", value: countText(state.request_concurrency) },
                        { label: "RPM/RPS", value: countText(state.requests_per_minute) },
                        { label: "CU/分钟", value: countText(state.cu_per_minute) },
                        { label: usedRequestsLabel(state.api_channel), value: countText(state.used_requests) },
                        { label: "已用 CU", value: countText(state.used_cu) },
                        { label: "估算成本（USD）", value: state.estimated_cost_usd || "—" },
                        {
                          label: "降级原因",
                          value: state.reason ? reasonText(state.reason) : "—",
                          wide: true,
                        },
                      ]}
                    />
                  </Card>
                </PageSection>
              ))}
            </div>
          )}
        </TabPanel>
      ) : null}

      {tab === "events" ? (
        <TabPanel tabKey="events">
          <PageSection
            title="结构化事件"
            count={events.length + " 条"}
            actions={
              <span className="toolbar">
                <StatusBadge tone={stream.connected ? "ok" : "warn"}>{stream.connected ? "SSE 已连接" : "SSE 未连接"}</StatusBadge>
                <span className="cs-section__count">{stream.lastEventID === undefined ? "" : `last_event_id ${stream.lastEventID}`}</span>
                <Button size="sm" onClick={onClearEvents}>清空</Button>
                <Button size="sm" onClick={onRefresh}>刷新</Button>
              </span>
            }
          >
            <Card>
              {events.length === 0 ? (
                <EmptyState title="尚未收到结构化任务事件" action={<Button size="sm" onClick={onRefresh}>刷新任务</Button>} />
              ) : (
                <div style={{ maxHeight: 520, overflowY: "auto", minWidth: 0 }}>
                  <Timeline
                    ariaLabel="任务结构化事件"
                    items={events.map((event, index): TimelineEntry => {
                      const errorMessage = eventErrorMessage(event);
                      const compact = ["command", "reason", "reset_shards", "failed_count"]
                        .map((key) => {
                          const value = event[key];
                          if (typeof value === "string" || typeof value === "number") return key + "=" + String(value);
                          return null;
                        })
                        .filter(Boolean)
                        .join(" · ");
                      const statusText = typeof event.status === "string" ? event.status : "";
                      const progressText = typeof event.progress === "number" ? percent(event.progress) : "";
                      const metaParts = [
                        formatTime(typeof event.ts === "string" ? event.ts : null),
                        statusText || null,
                        progressText || null,
                        compact || null,
                      ].filter(Boolean);
                      return {
                        key: `${String(event.event_id ?? event._sse_id ?? "snapshot")}-${index}`,
                        title: typeof event.event_type === "string" ? event.event_type : "snapshot",
                        tone: statusText ? statusToneFor(statusText) : "neutral",
                        meta: metaParts.join(" · "),
                        aside: (
                          <span className="toolbar">
                            <Button size="sm" onClick={() => dialog.notify("info", "结构化事件详情", null, eventFacts(event))}>字段</Button>
                            {errorMessage ? (
                              <Button
                                size="sm"
                                variant="danger"
                                onClick={() =>
                                  dialog.notify("error", "事件错误", errorMessage, [
                                    { label: "Trace", value: lastTrace ?? "—" },
                                    ...eventFacts(event),
                                  ])
                                }
                              >
                                错误
                              </Button>
                            ) : null}
                          </span>
                        ),
                      };
                    })}
                  />
                </div>
              )}
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}

      {tab === "exports" ? (
        <TabPanel tabKey="exports">
          <PageSection
            title="Exports"
            count={detail.exports.length + " 个"}
            actions={<Button size="sm" onClick={() => (onOpenExports ? onOpenExports() : navigate("/exports"))}>导出中心</Button>}
          >
            <Card>
              <DataTable<JobExport>
                rows={detail.exports}
                rowKey={(row) => row.id}
                caption="与该任务关联的导出及下载状态"
                columns={[
                  { key: "format", header: "格式", width: "150px", render: (row) => <span className="mono">{row.format + (row.zip ? " · zip" : "")}</span> },
                  { key: "status", header: "状态", width: "130px", render: (row) => <StatusBadgeForState value={row.status} /> },
                  { key: "rows", header: "行数", width: "120px", render: (row) => formatRawAmount(String(row.rows), 0) },
                  { key: "sha", header: "SHA-256", width: "250px", render: (row) => (row.sha256 ? <HashValue value={row.sha256} head={12} tail={8} label="导出 SHA-256" /> : "—") },
                  { key: "bytes", header: "大小", width: "110px", render: () => <span className="mono" title="API 未返回字节数">{formatBytes(undefined)}</span> },
                  { key: "expires", header: "有效期", width: "180px", render: (row) => (row.expires_at ? formatTime(row.expires_at) : "—") },
                  {
                    key: "actions",
                    header: "操作",
                    width: "140px",
                    render: (row) => {
                      const available = row.status === "COMPLETED" && (!row.expires_at || Date.parse(row.expires_at) > Date.now());
                      return (
                        <Button size="sm" disabled={!available || busy} onClick={() => onDownloadExport(row)}>
                          下载票据
                        </Button>
                      );
                    },
                  },
                ]}
                empty={<EmptyState title="该任务暂无导出" action={<Button size="sm" onClick={() => (onOpenExports ? onOpenExports() : navigate("/exports"))}>新建导出</Button>} />}
              />
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}

      {tab === "evidence" ? (
        <TabPanel tabKey="evidence">
          <PageSection title="证据链" count={detail.exports.length + " 个文件"}>
            <Card>
              <KeyValueList
                ariaLabel="任务证据链"
                items={[
                  { label: "任务 ID", value: <HashValue value={job.id} wrap label="任务 ID" />, wide: true },
                  { label: "任务号", value: job.job_no || "—" },
                  { label: "链", value: job.chain || "—" },
                  { label: "对象", value: String(detail.subjects.length) + " 个" },
                  { label: "Dataset 层", value: String(datasetLayers.length) + " 条" },
                  { label: "分片", value: String(shardLayers.length) + " 个" },
                  { label: "文件（导出）", value: String(detail.exports.length) + " 个" },
                  { label: "请求（控制面）", value: <span className="mono">{"/api/v2/jobs/" + job.id}</span>, wide: true },
                  { label: "审计关联", value: <span className="mono">{"/api/v2/jobs/" + job.id + "/commands/{command}"}</span>, wide: true },
                  { label: "最近 Trace", value: lastTrace ? <HashValue value={lastTrace} label="Trace" /> : "—" },
                  { label: "控制面来源", value: detail.source.control || "—" },
                  { label: "使用量来源", value: detail.source.usage || "—" },
                  { label: "数据时点", value: formatTime(detail.source.as_of) },
                  { label: "任务更新时间", value: formatTime(job.updated_at) },
                  { label: "Progress 时点", value: formatTime(shown.ts) },
                  { label: "成功游标时间", value: latestCursor ? formatTime(latestCursor) : "—" },
                  { label: "last_event_id", value: String(stream.lastEventID ?? "—") },
                  { label: "SSE", value: stream.connected ? "已连接" : "未连接" },
                ]}
              />
            </Card>
          </PageSection>
        </TabPanel>
      ) : null}
    </div>
  );
}

/* -- 内部小工具（保持原实现的纯函数语义，避免把业务判断散布到 JSX 里） -- */

/** 数值字段：缺失或非法显示 —，不补 0、不做任何推算。 */
function countText(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "—";
  return formatRawAmount(String(value), 0);
}

/**
 * "已用请求"的字段标签：单位随通道变化（开发文档 §13.2）。
 *
 * Data API 的 used_requests 是页数，JSON-RPC 的是方法调用数 —— 把一页 100 条交易
 * 显示成 100 次请求，是这一层要避免的核心错误。
 */
function usedRequestsLabel(apiChannel: string): string {
  if (apiChannel === "data_api") return "已用请求（Data API 页数）";
  if (apiChannel === "solana_rpc") return "已用请求（RPC 详情调用数）";
  return "已用请求（HTTP 请求数）";
}

/** 计数口径说明（只在 Dialog 的正文里出现）。 */
function countingNote(apiChannel: string): string {
  if (apiChannel === "data_api") {
    return "Data API 按页计数：1 次 HTTP 请求 = 1 页，单页最多 100 条交易；页内交易数不重复计入请求数。";
  }
  if (apiChannel === "solana_rpc") {
    return "JSON-RPC 按方法调用计数：1 次请求 = 1 个方法调用；首期 Batch=1，Batch 内的多个方法调用不合并计数。";
  }
  return "已用请求 = 该 Provider 通道实际发出的 HTTP 请求数；不按行数或交易数放大。";
}

/** 降档原因：中文可读文案 + 原始原因码（不认识的原因码原样显示，不编造）。 */
function reasonText(code: string): string {
  const text = modeReasonText(code);
  return text === code ? code : text + "（原因码：" + code + "）";
}

function percent(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return "—";
  return `${(value * 100).toFixed(value >= 1 ? 0 : 1)}%`;
}

function eventFacts(event: JobEvent): Array<{ label: string; value: string }> {
  const facts: Array<{ label: string; value: string }> = [];
  for (const [key, value] of Object.entries(event)) {
    if (key.startsWith("_")) continue;
    if (typeof value === "string" || typeof value === "number" || typeof value === "boolean" || value === null) {
      facts.push({ label: key, value: value === null ? "null" : String(value) });
    }
  }
  return facts;
}

function eventErrorMessage(event: JobEvent): string | null {
  const candidate = event.error_safe ?? event.message_safe ?? event.error;
  return typeof candidate === "string" && candidate.trim() !== "" ? candidate : null;
}
