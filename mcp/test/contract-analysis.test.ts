import test from "node:test";
import assert from "node:assert/strict";
import {createHash} from "node:crypto";
import {z} from "zod";
import {contractAnalysisTools,validateContractTool,contractArtifactResources} from "../src/contract-analysis.js";
import {nextInvestigationTools} from "../src/next-investigation.js";
import {RESTClient} from "../src/rest-client.js";
import {createServer} from "node:http";
import {once} from "node:events";
const address="0x"+"1".repeat(40),tx="0x"+"2".repeat(64),job="00000000-0000-4000-8000-000000000001";
test("contract operation definitions constrain chains, budgets and exact identities",()=>{
 assert.equal(contractAnalysisTools.length,8);const tool=contractAnalysisTools.find(t=>t.name==="analyze_contract")!;
 const schema=z.object(tool.inputSchema as z.ZodRawShape).strict();const valid=schema.parse({chain:"ethereum",address,tx_hashes:[tx]});validateContractTool(tool.name,valid);
 for(const bad of [{chain:"solana",address},{chain:"tron",address},{chain:"ethereum",address:"0x123"},{chain:"ethereum",address,budget:{max_contracts:1000}}])assert.equal(schema.safeParse(bad).success,false);
 assert.throws(()=>validateContractTool(tool.name,{...valid,block:1,timestamp:"2026-01-01T00:00:00Z"}));
 assert.throws(()=>validateContractTool(tool.name,{...valid,from_block:1}));
 assert.throws(()=>validateContractTool(tool.name,{...valid,tx_hashes:Array(21).fill(tx)}));
 assert.equal(contractAnalysisTools.find(t=>t.name==="get_contract_analysis_artifact")?.readOnly,true);
 assert.equal(tool.readOnly,false);
 const start=nextInvestigationTools.find(t=>t.name==="start_analysis")!;assert.ok((start.inputSchema as z.ZodType).safeParse({operation:"analyze_contract",arguments:{chain:"ethereum",address}}).success);
});
test("contract files expose verified embedded chunks and reject corrupt bytes",()=>{
 const bytes=Buffer.from("合约执行证据"),sha=createHash("sha256").update(bytes).digest("hex");
 const result={success:true,data:{content_base64:bytes.toString("base64"),resource_uri:`contract-analysis://${job}/${"a".repeat(40)}/0/1048576`,byte_length:bytes.length,chunk_sha256:sha,artifact:{media_type:"text/plain"}},meta:{},error:null};
 const files=contractArtifactResources(result);assert.equal(files[0].type,"resource");assert.equal("content_base64" in result.data,false);
 assert.throws(()=>contractArtifactResources({...result,data:{...result.data,content_base64:Buffer.from("corrupt").toString("base64")}}),/INTEGRITY_FAILED/);
});
test("feature REST routing leaves legacy tool requests on the deployed API",async()=>{
 const seen:string[]=[];
 const make=(name:string)=>createServer((req,res)=>{seen.push(name+req.url);res.setHeader("content-type","application/json");res.end(JSON.stringify({success:true,data:{name},meta:{},error:null}));});
 const legacy=make("legacy"),feature=make("feature");legacy.listen(0,"127.0.0.1");feature.listen(0,"127.0.0.1");await Promise.all([once(legacy,"listening"),once(feature,"listening")]);
 try{
  const port=(server:ReturnType<typeof createServer>)=>(server.address() as {port:number}).port;
  const client=new RESTClient({baseUrl:`http://127.0.0.1:${port(legacy)}`,contractBaseUrl:`http://127.0.0.1:${port(feature)}`,apiKey:"fixture",timeoutMs:1000});
  await client.post("/analysis/contracts/analyze_contract",{});await client.post("/export/contracts/get_contract_analysis_artifact",{});await client.get("/read/address/profile",{});await client.post("/analysis/start_analysis",{operation:"analyze_contract"});await client.post("/analysis/start_analysis",{operation:"fund_trace"});
  assert.deepEqual(seen,["feature/analysis/contracts/analyze_contract","feature/export/contracts/get_contract_analysis_artifact","legacy/read/address/profile","feature/analysis/start_analysis","legacy/analysis/start_analysis"]);
 }finally{await Promise.all([new Promise<void>(resolve=>legacy.close(()=>resolve())),new Promise<void>(resolve=>feature.close(()=>resolve()))]);}
});
