import type { ApiClient } from "./client";
export type ContractRow = Record<string, unknown>;
export interface ContractArtifact { artifact_id:string;kind:string;filename:string;media_type:string;byte_size:number;sha256:string }
export interface ContractStatus {
 analysis_id:string;version:string;status:"queued"|"running"|"completed"|"failed"|"cancelled";phase:string;attempts:number;
 snapshot:{chain?:string;point?:{as_of_block:number;as_of_block_hash:string}};
 metadata:{sections?:Record<string,number>;snapshot?:ContractRow;metrics?:ContractRow;completeness?:string};
 error_code?:string;error_message?:string;result_sha256?:string;created_at:string;updated_at:string;
}
export interface ContractPage extends ContractStatus {section:string;items:ContractRow[];record_count:number;returned_records:number;has_more:boolean;next_cursor:string|null}
export interface ContractSubmit {chain:string;address:string;block?:number|"latest"|"safe"|"finalized";timestamp?:string;from_block?:number;to_block?:number;tx_hashes?:string[];include_source?:boolean;engines?:string[];budget?:Record<string,number>;idempotency_key?:string}
export interface ProgramSubmit {schema_version:"contract-program-request-v1";vm_family:"tron_tvm"|"solana_sbf";address:string;tx_hashes?:string[];budget?:Record<string,number>;idempotency_key?:string}
export interface ContractArtifactChunk {artifact:ContractArtifact;offset:number;byte_length:number;sha256:string;chunk_sha256:string;content_base64:string;next_offset:number|null;complete:boolean}
const base="/api/v2/contract-analyses";
export const contractAnalysis = {
 list:(client:ApiClient,signal?:AbortSignal,cursor?:string)=>client.get<{items:ContractRow[];record_count:number;has_more:boolean;next_cursor:string|null}>(base,{signal,query:{cursor,page_size:50}}),
 submit:(client:ApiClient,request:ContractSubmit,signal?:AbortSignal)=>client.post<{analysis_id:string;status:string}>(base,request,{signal}),
 submitProgram:(client:ApiClient,request:ProgramSubmit,signal?:AbortSignal)=>client.post<{analysis_id:string;status:string}>(`${base}/programs`,request,{signal}),
 capabilities:(client:ApiClient,signal?:AbortSignal)=>client.get<ContractRow>(`${base}/capabilities`,{signal}),
 status:(client:ApiClient,id:string,signal?:AbortSignal)=>client.get<ContractStatus>(`${base}/${encodeURIComponent(id)}`,{signal}),
 page:(client:ApiClient,id:string,query:Record<string,string|number|undefined>,signal?:AbortSignal)=>client.get<ContractPage>(`${base}/${encodeURIComponent(id)}`,{query,signal}),
 method:(client:ApiClient,id:string,methodID:string,signal?:AbortSignal)=>client.get<ContractPage>(`${base}/${encodeURIComponent(id)}/methods/${encodeURIComponent(methodID)}`,{signal}),
 control:(client:ApiClient,id:string,command:"cancel"|"resume")=>client.post<ContractStatus>(`${base}/${encodeURIComponent(id)}/${command}`,{}),
 artifact:(client:ApiClient,id:string,artifactID:string,offset=0,chunkSize=1<<20,signal?:AbortSignal)=>client.get<ContractArtifactChunk>(`${base}/${encodeURIComponent(id)}/artifacts/${encodeURIComponent(artifactID)}`,{query:{offset,chunk_size:chunkSize},signal}),
 preview:(client:ApiClient,id:string,artifactID:string,offset=0,chunkSize=4096,signal?:AbortSignal)=>client.get<ContractArtifactChunk>(`${base}/${encodeURIComponent(id)}/artifacts/${encodeURIComponent(artifactID)}/preview`,{query:{offset,chunk_size:chunkSize},signal}),
 compare:(client:ApiClient,id:string,other:string,cursor?:string,signal?:AbortSignal)=>client.get<{items:ContractRow[];next_cursor:string|null;has_more:boolean;record_count:number}>(`${base}/${encodeURIComponent(id)}/compare/${encodeURIComponent(other)}`,{query:{cursor,page_size:50},signal}),
};
export async function contractArtifactBytes(client:ApiClient,id:string,artifact:ContractArtifact,signal?:AbortSignal,onProgress?:(bytes:number)=>void):Promise<Uint8Array>{
 if(artifact.byte_size>64<<20)throw new Error("文件超出读取预算");
 const parts:Uint8Array[]=[];let offset=0;
 do{
  const {data}=await contractAnalysis.artifact(client,id,artifact.artifact_id,offset,1<<20,signal);
  const bytes=Uint8Array.from(atob(data.content_base64),c=>c.charCodeAt(0));
  const digest=Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256",bytes))).map(n=>n.toString(16).padStart(2,"0")).join("");
  if(bytes.length!==data.byte_length||digest!==data.chunk_sha256||data.offset!==offset||data.sha256!==artifact.sha256||data.artifact.byte_size!==artifact.byte_size)throw new Error("文件片段校验失败");
  parts.push(bytes);offset+=bytes.length;onProgress?.(offset);
  if(data.next_offset===null){if(offset!==artifact.byte_size)throw new Error("文件长度校验失败");break}
  if(data.next_offset!==offset||!bytes.length)throw new Error("文件分页位置无效");
 }while(offset<artifact.byte_size);
 const all=new Uint8Array(offset);let start=0;for(const part of parts){all.set(part,start);start+=part.length}
 const sha=Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256",all))).map(n=>n.toString(16).padStart(2,"0")).join("");
 if(sha!==artifact.sha256)throw new Error("完整文件校验失败");return all;
}
