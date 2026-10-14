import test from "node:test";
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {once} from "node:events";
import {legacyOAuthAllowed} from "../src/legacy-oauth.js";
test("additive MCP release preserves the running authority's revocations",async()=>{
 let method="";
 const authority=createServer((req,res)=>{const chunks:Buffer[]=[];req.on("data",chunk=>chunks.push(chunk));req.on("end",()=>{method=JSON.parse(Buffer.concat(chunks).toString()).method;res.statusCode=req.headers.authorization==="Bearer revoked"?401:200;res.end("{}");});});
 authority.listen(0,"127.0.0.1");await once(authority,"listening");
 const endpoint=`http://127.0.0.1:${(authority.address() as {port:number}).port}/mcp`;
 try{assert.equal(await legacyOAuthAllowed(endpoint,"Bearer active","192.0.2.1"),true);assert.equal(await legacyOAuthAllowed(endpoint,"Bearer revoked","192.0.2.1"),false);assert.equal(method,"initialize");await assert.rejects(legacyOAuthAllowed("http://example.com/mcp","Bearer active","127.0.0.1"),/CONFIGURATION/);}
 finally{await new Promise<void>(resolve=>authority.close(()=>resolve()));}
 await assert.rejects(legacyOAuthAllowed(endpoint,"Bearer active","127.0.0.1"));
});
