import assert from "node:assert/strict";
import test from "node:test";
import {createHash} from "node:crypto";
import {McpServer} from "@modelcontextprotocol/sdk/server/mcp.js";
import {Client} from "@modelcontextprotocol/sdk/client/index.js";
import {InMemoryTransport} from "@modelcontextprotocol/sdk/inMemory.js";
import {registerInvestigationResources,investigationFileResources} from "../src/investigation.js";
import {RESTClient} from "../src/rest-client.js";
test("evidence bundles use authenticated resources and validate chunks",async()=>{
 const id="00000000-0000-4000-8000-000000000001";const bytes=Buffer.from("evidence ZIP bytes");const calls:any[]=[];
 const rest={post:async(path:string,args:unknown)=>{calls.push({path,args});return {success:true,error:null,meta:{},data:{content_base64:bytes.toString("base64"),chunk_bytes:bytes.length,chunk_sha256:createHash("sha256").update(bytes).digest("hex"),mime_type:"application/zip",resource_uri:`evidence://${id}/0/1024`}}}} as unknown as RESTClient;
 const server=new McpServer({name:"test",version:"1"});registerInvestigationResources(server,rest);const client=new Client({name:"test",version:"1"});const [ct,st]=InMemoryTransport.createLinkedPair();await server.connect(st);await client.connect(ct);
 try {assert.equal((await client.listResourceTemplates()).resourceTemplates.length,2);const resource=await client.readResource({uri:`evidence://${id}/0/1024`});assert.equal(Buffer.from((resource.contents[0] as any).blob,"base64").toString(),bytes.toString());assert.deepEqual(calls,[{path:"/export/get_evidence_bundle",args:{bundle_id:id,offset:0,chunk_size:1024}}]);await assert.rejects(client.readResource({uri:`evidence://${id}/-1/1024`}));assert.equal(calls.length,1)}finally{await client.close();await server.close()}
});
test("completed export is readable as authenticated MCP blob and corrupted bytes are rejected",async()=>{
 const job="00000000-0000-4000-8000-000000000000";const bytes=Buffer.from("payer,amount\n0x1,9007199254740993.123456789\n");
 const envelope=()=>({success:true,error:null,meta:{},data:{content_base64:bytes.toString("base64"),chunk_bytes:bytes.length,chunk_sha256:createHash("sha256").update(bytes).digest("hex"),mime_type:"text/csv",resource_uri:`export://${job}/0/1024`}});
 const result=envelope();const content=investigationFileResources(result);assert.equal(content[0].type,"resource");assert.equal("content_base64" in result.data,false);
 const bad=envelope();bad.data.content_base64="AAAA";assert.throws(()=>investigationFileResources(bad),/INTEGRITY/);
 const calls:any[]=[];const rest={post:async(path:string,args:unknown)=>{calls.push({path,args});return envelope()}} as unknown as RESTClient;
 const server=new McpServer({name:"test",version:"1"});registerInvestigationResources(server,rest);const client=new Client({name:"test",version:"1"});const [ct,st]=InMemoryTransport.createLinkedPair();await server.connect(st);await client.connect(ct);
 try{const resource=await client.readResource({uri:`export://${job}/0/1024`});assert.equal(Buffer.from((resource.contents[0] as any).blob,"base64").toString(),bytes.toString());assert.deepEqual(calls,[{path:"/export/get_export_file",args:{job_id:job,offset:0,chunk_size:1024}}]);await assert.rejects(client.readResource({uri:"export://bad/0/99999999"}));assert.equal(calls.length,1)}finally{await client.close();await server.close()}
});
