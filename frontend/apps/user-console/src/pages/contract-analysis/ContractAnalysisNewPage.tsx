import {withContractQueries} from "./provider";
import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useAuth, contractAnalysis, type ContractSubmit } from "@chain/api-client";
import { Button, Card, useDialog } from "@chain/ui";
import { EVM_CHAINS } from "./shared";
import "./contract-analysis.css";
function ContractAnalysisNewPage(){
 const {client}=useAuth(),dialog=useDialog(),navigate=useNavigate();const [params]=useSearchParams();
 const [chain,setChain]=useState(params.get("chain")==="eth"?"ethereum":params.get("chain")||"ethereum"),[address,setAddress]=useState(params.get("address")||"");
 const [block,setBlock]=useState("latest"),[from,setFrom]=useState(""),[to,setTo]=useState(""),[transactions,setTransactions]=useState("");
 const [maxContracts,setMaxContracts]=useState(20),[maxRPC,setMaxRPC]=useState(400),[source,setSource]=useState(true),[busy,setBusy]=useState(false);
 async function submit(event:React.FormEvent){event.preventDefault();if(busy)return;
  try{
   if(!/^0x[0-9a-fA-F]{40}$/.test(address.trim()))throw new Error("请输入完整的 EVM 合约地址");
   const blockText=block.trim();if(!blockText)throw new Error("请输入分析区块");
   const parsedBlock=["latest","safe","finalized"].includes(blockText)?blockText:Number(blockText);if(typeof parsedBlock==="number"&&(!Number.isSafeInteger(parsedBlock)||parsedBlock<0))throw new Error("区块号无效");
   if(!Number.isInteger(maxContracts)||maxContracts<1||maxContracts>50||!Number.isInteger(maxRPC)||maxRPC<10||maxRPC>2000)throw new Error("关联合约预算为 1–50，RPC 请求预算为 10–2000");
   if(Boolean(from)!==Boolean(to))throw new Error("请同时填写起始区块和结束区块");
   const hashes=transactions.split(/[\s,;]+/).filter(Boolean);if(hashes.length>20||hashes.some(h=>!/^0x[0-9a-fA-F]{64}$/.test(h)))throw new Error("最多填写 20 个完整交易哈希");
   const request:ContractSubmit={chain,address:address.trim(),block:parsedBlock as ContractSubmit["block"],tx_hashes:hashes,include_source:source,budget:{max_contracts:maxContracts,max_rpc_requests:maxRPC},idempotency_key:crypto.randomUUID()};
   if(from){const start=Number(from),end=Number(to);if(!Number.isSafeInteger(start)||!Number.isSafeInteger(end)||start<0||end<start||end-start>100000)throw new Error("历史范围须为有效区块，跨度不超过 100000");request.from_block=start;request.to_block=end}
   setBusy(true);const {data}=await contractAnalysis.submit(client,request);navigate(`/contract-analyses/${data.analysis_id}`);
  }catch(error){dialog.notify("error","提交分析失败",error instanceof Error?error.message:String(error))}finally{setBusy(false)}
 }
 return <div className="ca-page"><header className="ca-header"><div><Link to="/contract-analyses">合约分析记录</Link><h1>新建合约分析</h1></div><Button variant="ghost" onClick={()=>dialog.notify("info","分析范围","方法分析包含执行条件、步骤、状态读写、资金表达式、外部调用和原始证据。所选交易另外取得回执、调用轨迹与交易状态差异。预算达到上限时保留已解析数据和待分析对象。")}>分析范围</Button></header>
  <Card><form className="ca-form" noValidate onSubmit={event=>void submit(event)}>
   <label>链<select value={chain} onChange={e=>setChain(e.target.value)}>{EVM_CHAINS.map(c=><option key={c} value={c}>{c.toUpperCase()}</option>)}</select></label>
   <label>合约地址<input value={address} onChange={e=>setAddress(e.target.value)} spellCheck={false} autoComplete="off" required aria-label="合约地址"/></label>
   <label>分析区块<input value={block} onChange={e=>setBlock(e.target.value)} required aria-label="分析区块"/></label>
   <div className="ca-form-grid"><label>历史起始区块<input inputMode="numeric" value={from} onChange={e=>setFrom(e.target.value)}/></label><label>历史结束区块<input inputMode="numeric" value={to} onChange={e=>setTo(e.target.value)}/></label></div>
   <label>执行交易哈希<textarea rows={4} value={transactions} onChange={e=>setTransactions(e.target.value)} spellCheck={false}/></label>
   <div className="ca-form-grid"><label>关联合约预算<input type="number" min={1} max={50} value={maxContracts} onChange={e=>setMaxContracts(Number(e.target.value))}/></label><label>RPC 请求预算<input type="number" min={10} max={2000} value={maxRPC} onChange={e=>setMaxRPC(Number(e.target.value))}/></label></div>
   <label className="ca-checkbox"><input type="checkbox" checked={source} onChange={e=>setSource(e.target.checked)}/>使用已验证源码</label>
   <Button type="submit" busy={busy} busyLabel="提交中">开始分析</Button>
  </form></Card></div>;
}

export default withContractQueries(ContractAnalysisNewPage);
