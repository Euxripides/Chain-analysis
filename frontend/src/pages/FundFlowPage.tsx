import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useLocation } from "react-router-dom";
import {
  Background,
  Controls,
  MarkerType,
  MiniMap,
  ReactFlow,
  type Edge,
  type Node,
  useEdgesState,
  useNodesState,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import {
  Activity,
  ArrowDown,
  ArrowUp,
  ChevronsLeft,
  CircleDollarSign,
  Crosshair,
  Filter,
  Network,
  Search,
  ShieldCheck,
  SlidersHorizontal,
  GitBranch,
  Save,
  PanelsTopLeft,
  Info,
} from "lucide-react";
import { api, type FlowProfileEdge } from "../api/client";
import ConfirmDialog from "../components/ConfirmDialog";
import NoticeDialog from "../components/NoticeDialog";
import "./fund-flow.css";

type Direction = "all" | "in" | "out";
type FlowNodeData = {
  label: string;
  address: string;
  kind: "root" | "in" | "out";
};
const DEFAULT_ADDRESS = "TAkT2zbS81TbsNBLRvqv5fyAq4vCUWBk9m";

export default function FundFlowPage() {
  const queryClient = useQueryClient();
  const location = useLocation();
  const params = useMemo(
    () => new URLSearchParams(location.search),
    [location.search],
  );
  const initialAddress = params.get("address") || DEFAULT_ADDRESS;
  const [chain, setChain] = useState(params.get("chain") || "tron");
  const [draft, setDraft] = useState(initialAddress);
  const [address, setAddress] = useState(initialAddress);
  const [direction, setDirection] = useState<Direction>("all");
  const [lens, setLens] = useState("资金主干");
  const [asset, setAsset] = useState("all");
  const [minRatio, setMinRatio] = useState(0);
  const [selected, setSelected] = useState("root");
  const [caseID,setCaseID]=useState("");
  const [canvasID,setCanvasID]=useState("");
  const [dialog,setDialog]=useState<"case"|"canvas"|"branch"|"snapshot"|null>(null);
  const [dialogName,setDialogName]=useState("");
  const [notice,setNotice]=useState<{title:string;message:string}|null>(null);
  const [viewport,setViewport]=useState({x:0,y:0,zoom:1});
  const investigationID=params.get("investigation")||undefined;
  const casesQ=useQuery({queryKey:["cases"],queryFn:api.listCases});
  useEffect(()=>{if(!caseID&&casesQ.data?.cases[0])setCaseID(casesQ.data.cases[0].id)},[caseID,casesQ.data]);
  const canvasesQ=useQuery({queryKey:["canvases",caseID],queryFn:()=>api.listCanvases(caseID),enabled:!!caseID});
  useEffect(()=>{if(!canvasID&&canvasesQ.data?.items[0])setCanvasID(canvasesQ.data.items[0].id)},[canvasID,canvasesQ.data]);
  const currentCanvas=canvasesQ.data?.items.find(item=>item.id===canvasID);
  const [inspectorOpen, setInspectorOpen] = useState(
    () => window.innerWidth > 680,
  );
  const profileQ = useQuery({
    queryKey: ["flow-profile", chain, address],
    queryFn: () => api.getFlowProfile(chain, address),
    enabled: !!address,
    staleTime: 30_000,
  });
  useEffect(()=>{if(profileQ.error)setNotice({title:"资金图加载失败",message:profileQ.error.message})},[profileQ.error]);
  const assets = useMemo(
    () =>
      Array.from(
        new Set(profileQ.data?.edges.map((edge) => edge.asset) || []),
      ).sort(),
    [profileQ.data],
  );
  const visible = useMemo(
    () =>
      profileQ.data?.edges
        .filter(
          (edge) =>
            (direction === "all" || edge.direction === direction) &&
            (asset === "all" || edge.asset === asset) &&
            Number(edge.flow_ratio) >= minRatio,
        )
        .slice(0, 100) || [],
    [profileQ.data, direction, asset, minRatio],
  );
  const canvasRef = useRef<HTMLElement | null>(null);
  const idleTimer = useRef<number>(0);
  const graph = useMemo(() => buildGraph(address, visible, selected), [address, visible, selected]);
  const [nodes, setNodes, onNodesChange] = useNodesState<Node<FlowNodeData>>(
    [],
  );
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  useEffect(() => {
    setNodes(graph.nodes);
    setEdges(graph.edges);
  }, [graph, setNodes, setEdges]);
  const selectedEdge =
    selected === "root"
      ? undefined
      : visible[Number(selected.replace("cp-", ""))];
  const chooseLens = (next: string, threshold: number) => {
    setLens(next);
    setMinRatio(threshold);
  };
  const startCanvasMotion = () => {
    window.clearTimeout(idleTimer.current);
    canvasRef.current?.classList.add("is-interacting");
  };
  const stopCanvasMotion = () => {
    window.clearTimeout(idleTimer.current);
    idleTimer.current = window.setTimeout(() => canvasRef.current?.classList.remove("is-interacting"), 140);
  };
  const canvasState=()=>({node_refs:nodes.map(node=>node.id==="root"?`${chain}:${address}`:`${chain}:${(node.data as FlowNodeData).address}`),edge_refs:visible.flatMap(edge=>edge.evidence),groups:currentCanvas?.groups||[],annotations:currentCanvas?.annotations||[],filters:{direction,asset,min_ratio:minRatio,lens},layout:Object.fromEntries(nodes.map(node=>[node.id,node.position])),camera:viewport,view_state:{chain,address,selected}});
  const runDialog=async()=>{try{
    if(dialog==="case"){const created=await api.createCase(dialogName);setCaseID(created.id);setCanvasID("");await queryClient.invalidateQueries({queryKey:["cases"]});setNotice({title:"案件已创建",message:`“${created.name}”现在可以保存多个独立资金画布。`})}
    else if(dialog==="canvas"){const created=await api.createCanvas(caseID,{name:dialogName,investigation_id:investigationID,...canvasState()});setCanvasID(created.id);await queryClient.invalidateQueries({queryKey:["canvases",caseID]});setNotice({title:"画布已创建",message:`“${created.name}”已保存当前资金图状态。`})}
    else if(dialog==="branch"&&currentCanvas){const created=await api.branchCanvas(currentCanvas.id,{name:dialogName,node_refs:selected==="root"?canvasState().node_refs:[`${chain}:${address}`,`${chain}:${selectedEdge?.counterparty}`],edge_refs:selectedEdge?.evidence||[],filters:canvasState().filters,layout:canvasState().layout,camera:viewport,view_state:canvasState().view_state});setCanvasID(created.id);await queryClient.invalidateQueries({queryKey:["canvases",caseID]});setNotice({title:"分支画布已创建",message:`“${created.name}”仅保留当前选择及其证据关系。`})}
    else if(dialog==="snapshot"&&currentCanvas){const saved=await api.updateCanvas(currentCanvas.id,currentCanvas.revision,{name:currentCanvas.name,...canvasState()});const snap=await api.createCanvasSnapshot(saved.id,dialogName);await queryClient.invalidateQueries({queryKey:["canvases",caseID]});setNotice({title:"快照已固化",message:`${snap.name} 已按画布修订 ${snap.canvas_revision} 不可变保存。`})}
  }catch(error){setNotice({title:"画布操作未完成",message:error instanceof Error?error.message:"未知错误"})}finally{setDialog(null);setDialogName("")}};

  return (
    <div className="flow-workspace">
      <header className="flow-workspace-header">
        <div className="flow-title">
          <CircleDollarSign size={20} />
          <span>
            <b>资金分析图</b>
            <small>独立资金流画布 · 确定性聚合</small>
          </span>
        </div>
        <form
          className="flow-search"
          onSubmit={(event) => {
            event.preventDefault();
            if (draft.trim()) {
              setAddress(draft.trim());
              setSelected("root");
            }
          }}
        >
          <select
            aria-label="区块链"
            value={chain}
            onChange={(event) => setChain(event.target.value)}
          >
            <option value="tron">TRON</option>
            <option value="ethereum">Ethereum</option>
            <option value="bsc">BSC</option>
            <option value="polygon">Polygon</option>
          </select>
          <Search size={15} />
          <input
            aria-label="完整地址"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder="输入完整地址，Enter 定位"
          />
          <button type="submit">
            <Crosshair size={14} />
            定位
          </button>
        </form>
        <div className="flow-segments">
          <button
            aria-pressed={direction === "all"}
            onClick={() => setDirection("all")}
          >
            全局视图
          </button>
          <button
            aria-pressed={direction === "in"}
            onClick={() => setDirection("in")}
          >
            <ArrowUp size={13} />
            上游
          </button>
          <button
            aria-pressed={direction === "out"}
            onClick={() => setDirection("out")}
          >
            <ArrowDown size={13} />
            下游
          </button>
          <button aria-pressed="true">1 层</button>
          <button disabled title="多跳追踪将在后续分析阶段接入">
            2 层
          </button>
          <button disabled title="多跳追踪将在后续分析阶段接入">
            3 层
          </button>
        </div>
      </header>
      <nav className="flow-canvas-toolbar" aria-label="案件画布工具栏">
        <PanelsTopLeft size={15}/><select aria-label="案件" value={caseID} onChange={e=>{setCaseID(e.target.value);setCanvasID("")}}><option value="">选择案件</option>{casesQ.data?.cases.map(item=><option key={item.id} value={item.id}>{item.name}</option>)}</select>
        <button onClick={()=>{setDialogName("");setDialog("case")}}>新建案件</button>
        <select aria-label="分析画布" value={canvasID} onChange={e=>setCanvasID(e.target.value)} disabled={!caseID}><option value="">选择画布</option>{canvasesQ.data?.items.map(item=><option key={item.id} value={item.id}>{item.parent_canvas_id?"↳ ":""}{item.name} · r{item.revision}</option>)}</select>
        <button disabled={!caseID} onClick={()=>{setDialogName(`资金分析 · ${short(address)}`);setDialog("canvas")}}>新建画布</button>
        <button disabled={!currentCanvas} onClick={()=>{setDialogName(`${currentCanvas?.name} · 分支`);setDialog("branch")}}><GitBranch size={14}/>从选择分支</button>
        <button className="canvas-save" disabled={!currentCanvas} onClick={()=>{setDialogName(`快照 · ${new Date().toLocaleString("zh-CN")}`);setDialog("snapshot")}}><Save size={14}/>保存快照</button>
        {currentCanvas&&<span>{currentCanvas.parent_canvas_id?"分支画布":"主画布"} · r{currentCanvas.revision}</span>}
        <button className="canvas-info" aria-label="查看画布说明" onClick={()=>setNotice({title:"案件画布",message:"画布保存筛选器、节点位置、镜头和证据引用；分支画布不会改变主画布；快照创建后不可修改。"})}><Info size={14}/></button>
      </nav>
      <div
        className={`flow-workspace-body${inspectorOpen ? "" : " inspector-closed"}`}
      >
        <aside className="flow-lens-panel">
          <section>
            <h3>分析上下文</h3>
            {params.get("investigation") ? (
              <p className="mapped-investigation">
                <Activity size={13} />
                来自智能调查<code>{params.get("investigation")}</code>
              </p>
            ) : (
              <p className="flow-muted">独立分析会话</p>
            )}
            <code className="flow-root-address">{address}</code>
          </section>
          <section>
            <h3>分析透镜</h3>
            <div className="flow-lenses">
              <button
                className={lens === "资金主干" ? "active" : ""}
                onClick={() => chooseLens("资金主干", 0)}
              >
                资金主干
              </button>
              <button
                className={lens === "大额流向" ? "active" : ""}
                onClick={() => chooseLens("大额流向", 0.05)}
              >
                大额流向
              </button>
              <button disabled title="需要时间序列规则">
                快速转移
              </button>
              <button disabled title="需要地址标签库">
                交易所落点
              </button>
              <button disabled title="需要风险标签库">
                风险暴露
              </button>
              <button disabled title="需要跨链映射">
                跨链
              </button>
            </div>
          </section>
          <section>
            <h3>
              <Filter size={13} />
              过滤器
            </h3>
            <label>
              资产
              <select
                value={asset}
                onChange={(event) => setAsset(event.target.value)}
              >
                <option value="all">全部资产</option>
                {assets.map((item) => (
                  <option key={item}>{item}</option>
                ))}
              </select>
            </label>
            <label>
              最小资产内占比 <b>{(minRatio * 100).toFixed(0)}%</b>
              <input
                type="range"
                min="0"
                max="0.5"
                step="0.01"
                value={minRatio}
                onChange={(event) => setMinRatio(Number(event.target.value))}
              />
            </label>
          </section>
          <section>
            <h3>证据口径</h3>
            <button onClick={()=>setNotice({title:"证据口径",message:profileQ.data?.evidence_boundary||"当前尚未加载分析口径。"})}>查看分析边界</button>
          </section>
        </aside>
        <main className={`flow-canvas-shell${selected !== "root" ? " has-focus" : ""}`} ref={canvasRef}>
          <div className="flow-canvas-meta">
            <span>
              {profileQ.isFetching
                ? "正在计算…"
                : `当前显示 ${nodes.length} 个地址 · ${edges.length} 组关系`}
            </span>
            <em>{profileQ.data?.algorithm_version || "basic-flow-v1.0"}</em>
            <i className="flow-live-pulse" title="资金流动画运行中" />
          </div>
          {profileQ.error ? (
            <div className="flow-canvas-empty">
              <Network size={28} />
              <b>暂无资金图数据</b>
            </div>
          ) : (
          <ReactFlow
            key={`${address}-${direction}-${asset}-${minRatio}`}
            nodes={nodes}
              edges={edges}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onMoveStart={startCanvasMotion}
              onMoveEnd={(_,next)=>{stopCanvasMotion();setViewport(next)}}
              onNodeClick={(_, node) => {
                setSelected(node.id);
                setInspectorOpen(true);
              }}
              onPaneClick={() => setSelected("root")}
              fitView
              fitViewOptions={{ padding: 0.18, maxZoom: 1 }}
              minZoom={0.2}
              maxZoom={2}
              nodesDraggable
              proOptions={{ hideAttribution: true }}
            >
              <Background color="#d7e0ec" gap={18} size={1} />
              <MiniMap
                pannable
                zoomable
                nodeColor={(node) =>
                  node.id === "root"
                    ? "#1768e5"
                    : (node.data as FlowNodeData).kind === "in"
                      ? "#13a88a"
                      : "#f59e32"
                }
              />
              <Controls showInteractive={false} />
            </ReactFlow>
          )}
          <div className="flow-legend">
            <span>
              <i className="root" />
              核心地址
            </span>
            <span>
              <i className="up" />
              上游来源
            </span>
            <span>
              <i className="down" />
              下游去向
            </span>
            <span>
              <b>•••</b>流动方向
            </span>
          </div>
        </main>
        {inspectorOpen ? (
          <aside className="flow-inspector">
            <header>
              <div>
                <h3>{selected === "root" ? "地址详情" : "关系详情"}</h3>
                <span>
                  {selected === "root" ? "分析核心地址" : "聚合边与交易证据"}
                </span>
              </div>
              <button
                aria-label="收起详情"
                onClick={() => setInspectorOpen(false)}
              >
                <ChevronsLeft size={16} />
              </button>
            </header>
            <section className="flow-inspector-identity">
              <span
                className={
                  selectedEdge?.direction === "in"
                    ? "incoming"
                    : selectedEdge
                      ? "outgoing"
                      : "root"
                }
              >
                {selectedEdge?.direction === "in" ? (
                  <ArrowUp />
                ) : selectedEdge ? (
                  <ArrowDown />
                ) : (
                  <Network />
                )}
              </span>
              <code>{selectedEdge?.counterparty || address}</code>
            </section>
            <section className="flow-stat-grid">
              <div>
                <small>交易笔数</small>
                <b>
                  {selectedEdge?.transactions ??
                    profileQ.data?.transaction_count ??
                    0}
                </b>
              </div>
              <div>
                <small>直接对手方</small>
                <b>
                  {selectedEdge
                    ? 1
                    : (profileQ.data?.unique_counterparties ?? 0)}
                </b>
              </div>
              <div>
                <small>资产数</small>
                <b>{selectedEdge ? 1 : assets.length}</b>
              </div>
              <div>
                <small>资产内占比</small>
                <b>
                  {selectedEdge
                    ? `${(Number(selectedEdge.flow_ratio) * 100).toFixed(2)}%`
                    : "—"}
                </b>
              </div>
            </section>
            {selectedEdge && (
              <section>
                <h4>聚合关系</h4>
                <dl className="flow-facts">
                  <div>
                    <dt>方向</dt>
                    <dd>
                      {selectedEdge.direction === "in"
                        ? "流入核心地址"
                        : "从核心地址流出"}
                    </dd>
                  </div>
                  <div>
                    <dt>金额</dt>
                    <dd>
                      {selectedEdge.amount} {selectedEdge.asset}
                    </dd>
                  </div>
                  <div>
                    <dt>首次发生</dt>
                    <dd>{formatTime(selectedEdge.first_seen)}</dd>
                  </div>
                  <div>
                    <dt>最后发生</dt>
                    <dd>{formatTime(selectedEdge.last_seen)}</dd>
                  </div>
                </dl>
              </section>
            )}
            <section>
              <h4>链上交易证据</h4>
              <div className="flow-evidence-list">
                {selectedEdge?.evidence.map((tx) => (
                  <code key={tx} title={tx}>
                    {tx}
                  </code>
                )) || <p>选择关系节点后查看完整交易哈希。</p>}
              </div>
            </section>
            <footer>
              <button disabled title="调查证据写入将在证据固化阶段接入">
                <ShieldCheck size={14} />
                证据写入待接入
              </button>
            </footer>
          </aside>
        ) : (
          <button
            className="flow-open-inspector"
            onClick={() => setInspectorOpen(true)}
            aria-label="展开详情"
          >
            <SlidersHorizontal size={16} />
          </button>
        )}
      </div>
      <footer className="flow-statusbar">
        <span>
          <b>{nodes.length}</b> 节点
        </span>
        <span>
          <b>{edges.length}</b> 关系
        </span>
        <span>
          <b>{profileQ.data?.transaction_count || 0}</b> 交易
        </span>
        <span>
          上游 <b>{visible.filter((edge) => edge.direction === "in").length}</b>
        </span>
        <span>
          下游{" "}
          <b>{visible.filter((edge) => edge.direction === "out").length}</b>
        </span>
        <span className="complete">完整性：Top 100 聚合关系</span>
        <span>
          {chain.toUpperCase()} / {asset === "all" ? "全部 Token" : asset}
        </span>
      </footer>
      {dialog&&<ConfirmDialog eyebrow="CASE CANVAS" title={dialog==="case"?"新建案件":dialog==="canvas"?"新建资金画布":dialog==="branch"?"从当前选择创建分支":"保存不可变快照"} confirmLabel={dialog==="snapshot"?"保存快照":"创建"} onCancel={()=>setDialog(null)} onConfirm={runDialog}><label className="canvas-dialog-field">名称<input autoFocus value={dialogName} onChange={e=>setDialogName(e.target.value)} /></label></ConfirmDialog>}
      {notice&&<NoticeDialog title={notice.title} message={notice.message} onClose={()=>setNotice(null)}/>} 
    </div>
  );
}

function buildGraph(
  root: string,
  items: FlowProfileEdge[],
  selected: string,
): { nodes: Node<FlowNodeData>[]; edges: Edge[] } {
  const nodes: Node<FlowNodeData>[] = [
    {
      id: "root",
      position: { x: 520, y: 310 },
      data: { label: `核心地址\n${short(root)}`, address: root, kind: "root" },
      className: `flow-node flow-node-root${selected === "root" ? " is-focused" : ""}`,
    },
  ];
  const edges: Edge[] = [];
  let inboundCount = 0;
  let outboundCount = 0;
  items.forEach((item, index) => {
    const inbound = item.direction === "in";
    const lane = inbound ? inboundCount++ : outboundCount++;
    const column = Math.floor(lane / 12);
    const row = lane % 12;
    const id = `cp-${index}`;
    nodes.push({
      id,
      position: {
        x: inbound ? 230 - column * 245 : 810 + column * 245,
        y: 25 + row * 52,
      },
      data: {
        label: `${inbound ? "流入" : "流出"} · ${item.asset}\n${short(item.counterparty)}\n${item.amount}`,
        address: item.counterparty,
        kind: item.direction,
      },
      className: `flow-node flow-node-${item.direction}${selected === id ? " is-focused" : selected !== "root" ? " is-muted" : ""}`,
      style: { width: 190, "--node-order": Math.min(index, 18) } as React.CSSProperties,
    });
    edges.push({
      id: `edge-${index}`,
      source: inbound ? id : "root",
      target: inbound ? "root" : id,
      type: "smoothstep",
      animated: true,
      label: `${item.amount} ${item.asset} · ${item.transactions}笔`,
      className: `flow-edge flow-edge-${item.direction}${selected === id ? " is-focused" : selected !== "root" ? " is-muted" : ""}`,
      markerEnd: {
        type: MarkerType.ArrowClosed,
        width: 14,
        height: 14,
        color: inbound ? "#13a88a" : "#f59e32",
      },
    });
  });
  return { nodes, edges };
}

function short(value: string) {
  return value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value;
}
function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString("zh-CN") : "—";
}
