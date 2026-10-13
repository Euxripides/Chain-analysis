import assert from "node:assert/strict";
import test from "node:test";
import { createServer } from "node:http";
import { once } from "node:events";
import type { AddressInfo } from "node:net";
import { z } from "zod";
import { RESTClient } from "../src/rest-client.js";
import { nextInvestigationTools } from "../src/next-investigation.js";

test("REST rejects proxy HTML and malformed envelopes with a stable safe error, without retries", async t => {
 let requests=0;
 const server=createServer((req,res)=>{
  requests++;
  assert.equal(req.headers.accept,"application/json");
  assert.equal(req.headers["user-agent"],"blockchain-platform-mcp/1.0.0");
  if(req.url==="/html") {res.statusCode=502;res.end("<html>private upstream message</html>");return}
  res.setHeader("content-type","application/json");
  res.end(req.url==="/null"?"null":req.url==="/shape"?'{"success":true}':JSON.stringify({success:true,data:{amount_raw:"9007199254740993123456789"},meta:{},error:null}));
 });
 server.listen(0,"127.0.0.1");await once(server,"listening");
 t.after(async()=>{server.closeAllConnections();await new Promise<void>(resolve=>server.close(()=>resolve()))});
 const rest=new RESTClient({baseUrl:`http://127.0.0.1:${(server.address() as AddressInfo).port}`,apiKey:"test",timeoutMs:2000});
 for(const path of ["/html","/null","/shape"]) await assert.rejects(rest.post(path,{}),e=>e instanceof Error&&e.message.startsWith("REST_INVALID_RESPONSE:")&&!e.message.includes("private upstream"));
 assert.equal(requests,3);
 assert.equal((await rest.post("/ok",{})).data&&requests,4);
});

test("compact analysis schemas default to compatibility and support explicit compact mode",()=>{
 for(const name of ["get_analysis_status","get_analysis_results"]) {
  const definition=nextInvestigationTools.find(t=>t.name===name)!;
  const schema=z.object(definition.inputSchema as Record<string,z.ZodTypeAny>);
  const analysis_id="00000000-0000-4000-8000-000000000001";
  assert.equal(schema.parse({analysis_id}).include_metadata,true);
  assert.equal(schema.parse({analysis_id,include_metadata:false}).include_metadata,false);
 }
 const definition=nextInvestigationTools.find(t=>t.name==="create_watch_rule")!;
 const name=(definition.inputSchema as Record<string,z.ZodTypeAny>).name;
 assert.equal(name.safeParse("汉".repeat(200)).success,true);
 assert.equal(name.safeParse("😀".repeat(200)).success,true);
 assert.equal(name.safeParse("😀".repeat(201)).success,false);
});
