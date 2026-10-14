import { useEffect, useRef } from "react";
import { Button, useDialog } from "@chain/ui";
import type { ContractRow } from "@chain/api-client";
export const EVM_CHAINS=["ethereum","bsc","polygon","arbitrum","optimism","base","avalanche","fantom","flare","gnosis","linea","scroll","somnia_mainnet","story_mainnet","taiko","telos","xlayer"];
export const STATUS:Record<string,string>={queued:"排队中",running:"分析中",completed:"已完成",failed:"失败",cancelled:"已取消"};
export function text(value:unknown):string {if(value===null||value===undefined||value==="")return "—";if(typeof value==="object")return JSON.stringify(value,null,2);return String(value)}
export function list(value:unknown):string[]{return Array.isArray(value)?value.map(String):[]}
export function object(value:unknown):ContractRow{return value&&typeof value==="object"&&!Array.isArray(value)?value as ContractRow:{}}
export function fieldText(value:unknown):string {return Array.isArray(value)&&value.every(item=>typeof item==="string")?(value.length?value.join("\n"):"—"):text(value)}
export function MethodArguments({value,title,onEvidence}:{value:unknown;title:string;onEvidence:(id:string)=>void}){
 const rows=Array.isArray(value)?value.map(object):[];
 return <section className="ca-method-section"><h3>{title}</h3>{rows.length?rows.map((row,index)=><div className="ca-argument" key={index}><strong className="ca-mono">{text(row.name||`arg${index}`)}</strong><dl className="ca-kv"><dt>类型</dt><dd className="ca-mono">{text(row.type)}</dd>{row.unit?<><dt>单位</dt><dd>{({wei:"wei",token_raw:"代币最小单位",seconds:"秒"} as Record<string,string>)[String(row.unit)]||text(row.unit)}</dd></>:null}</dl>{Array.isArray(row.constraints)?row.constraints.map((raw,index)=><div className="ca-body-text ca-mono" key={index}>{text(object(raw).condition)}</div>):null}<EvidenceLinks ids={row.evidence_ids} onOpen={onEvidence}/></div>):<div>—</div>}</section>;
}
export function useAnalysisError(error:unknown,key:string){
 const dialog=useDialog(),notified=useRef("");
 useEffect(()=>{if(!error)return;const message=error instanceof Error?error.message:String(error);const identity=key+message;if(notified.current===identity)return;notified.current=identity;dialog.notify("error","合约分析请求失败",message)},[dialog,error,key]);
}
export function EvidenceLinks({ids,onOpen}:{ids:unknown;onOpen:(id:string)=>void}){const values=list(ids),dialog=useDialog();return <div className="ca-evidence-links">{values.slice(0,2).map((id,index)=><Button key={id} size="sm" variant="ghost" onClick={()=>onOpen(id)} aria-label={`查看证据 ${id}`}>证据 {index+1}</Button>)}{values.length>2?<Button size="sm" variant="ghost" onClick={()=>dialog.notify("info","方法证据",<div className="ca-evidence-links">{values.map((id,index)=><Button key={id} variant="ghost" onClick={()=>{dialog.dismiss();onOpen(id)}} aria-label={`查看证据 ${id}`}>证据 {index+1} · {id}</Button>)}</div>)}>全部证据 ({values.length})</Button>:null}</div>}
export function RowDetails({row}:{row:ContractRow}){const dialog=useDialog();return <Button size="sm" variant="ghost" onClick={()=>dialog.notify("info","分析记录",<pre className="ca-json">{JSON.stringify(row,null,2)}</pre>)}>详情</Button>}
export function Pager({more,cursor,onNext,onFirst,count}:{more:boolean;cursor?:string|null;onNext:()=>void;onFirst:()=>void;count:number}){return <div className="ca-pager"><span>记录总数 {count}</span><div>{cursor?<Button variant="ghost" onClick={onFirst}>首页</Button>:null}<Button disabled={!more} onClick={onNext}>下一页</Button></div></div>}
