import assert from "node:assert/strict";
import test from "node:test";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { registerTools } from "../src/tools.js";
import { bridgeQuerySchema, collisionSchema, crossChainTools } from "../src/crosschain.js";
test("strict mode defaults and summaries never promote legacy verification", () => {
  const query = {bridge:"lifi",chain:"bsc",tx_hash:hash};
  assert.equal(bridgeQuerySchema.parse(query).verification_mode, "funds");
  assert.equal(bridgeQuerySchema.parse({...query,verification_mode:"discovery"}).verification_mode, "discovery");
  for (const fields of [{verification_mode:"relaxed"}, {funds_verified:true}, {proof:{}}, {blocking_reasons:[]}]) assert.equal(bridgeQuerySchema.safeParse({...query,...fields}).success, false);
  const tool = crossChainTools.find(t => t.name === "query_cross_chain_transfer")!;
  const legacy = tool.summarize!({success:true,data:{verification_mode:"discovery",transfers:[{on_chain_verified:true,pairing_verified:true,funds_verified:false}]},meta:{},error:null});
  assert.match(legacy,/0 strict funding-verified/);
  assert.match(legacy,/1 pending\/unsupported/);
  const strict = tool.summarize!({success:true,data:{verification_mode:"funds",transfers:[{funds_verified:true}],pending_transfers:[{on_chain_verified:true,funds_verified:false}]},meta:{},error:null});
  assert.match(strict,/1 strict funding-verified/);
  assert.match(strict,/1 pending\/unsupported/);
});
import type { RESTClient } from "../src/rest-client.js";

const hash = "0x" + "1".repeat(64);
const signature = "3QvRpJyWnMomnxPTApBeMHdZvb5BAeWd9Bu8A4rDBQnJ8hMvbX49GUrwFJuBVNQmhH7u4EopmS3MJQSzf34ouma5";
const collision = { anchor: { chain: "solana", tx_hash: signature, dataset: "token_transfers", event_index: 2 }, search_chain: "bsc", search_addresses: ["0x" + "1".repeat(40)], search_direction: "out", direction: "forward", asset_kind: "native", asset_relation: "swap", expected_amount: "0.007631125135013813", start_time: "2026-05-18T00:20:00Z", end_time: "2026-05-18T00:35:00Z" };

test("cross-chain schemas enforce bounded and protocol-specific queries", () => {
  for (const value of [{}, {bridge: "near_intents", order_id: "a"}, {bridge: "lifi", chain: "bsc", tx_hash: "0x123"}, {bridge: "cbridge", transfer_id: "partial"}, {bridge: "auto", deposit_address: "a"}, {bridge: "relay", order_id: "a", deposit_memo: "b"}]) assert.equal(bridgeQuerySchema.safeParse(value).success, false);
  assert.equal(bridgeQuerySchema.parse({bridge: "lifi", chain: "bsc", tx_hash: hash}).max_results, 20);
  for (const bridge of ["wormhole", "cctp"]) {
    assert.equal(bridgeQuerySchema.safeParse({bridge,chain:"ethereum",tx_hash:hash}).success,true);
    assert.equal(bridgeQuerySchema.safeParse({bridge,order_id:"untrusted",hints:[{identifier:hash}]}).success,false);
    assert.equal(bridgeQuerySchema.safeParse({bridge,chain:"ethereum",tx_hash:hash,hints:[]}).success,false);
  }
  assert.equal(collisionSchema.parse(collision).expected_amount, "0.007631125135013813");
  for (const changed of [{expected_amount: undefined}, {expected_amount: "1e18"}, {expected_amount: "0"}, {max_parts: 4}, {scan_limit: 1001}, {max_combinations: 20001}, {end_time: "2026-05-20T00:35:00Z"}, {token_contract: "0x" + "1".repeat(40)}]) assert.equal(collisionSchema.safeParse({...collision, ...changed}).success, false);
});

test("registered bridge tools expose real schemas, preserve evidence and reject invalid calls before REST", async () => {
  const calls: {path: string; args: unknown}[] = [];
  const rest = {get: async(path: string, args: unknown) => {calls.push({path,args});return {success:true,data:{protocols:[]},meta:{},error:null}}, post: async(path: string, args: unknown) => {calls.push({path,args});return {success:true,data: path.endsWith("candidates") ? {candidates: [{total_amount: "0.007631125135013813", association: "candidate"}], scan_truncated: true} : {transfers: [{on_chain_verified: false, evidence:{response_sha256: "a".repeat(64)}}], attempts:[{bridge:"lifi",status:"found"}]},meta:{coverage:{complete:false}},error:null}}} as unknown as RESTClient;
  const server = new McpServer({name: "test",version: "1"});registerTools(server, rest);
  const client = new Client({name: "test",version: "1"});const [ct,st] = InMemoryTransport.createLinkedPair();await server.connect(st);await client.connect(ct);
  try {
    const tools = (await client.listTools()).tools;
    for (const name of ["get_cross_chain_capabilities", "query_cross_chain_transfer", "find_cross_chain_candidates"]) {
      const tool = tools.find(t => t.name === name)!;assert.ok(tool);assert.equal(tool.annotations?.readOnlyHint,true);
      if (name !== "get_cross_chain_capabilities") assert.ok(Object.keys(tool.inputSchema.properties ?? {}).length > 5);
    }
    const invalid = await client.callTool({name:"query_cross_chain_transfer",arguments:{bridge:"near_intents",order_id:"a"}});assert.equal(invalid.isError,true);assert.equal(calls.length,0);
    for (const extra of [{proof:{}},{funds_verified:true},{hints:[]}]) {
      const rejected = await client.callTool({name:"query_cross_chain_transfer",arguments:{bridge:"lifi",chain:"bsc",tx_hash:hash,...extra}});
      assert.equal(rejected.isError,true);assert.equal(calls.length,0);
    }
    const transfer = await client.callTool({name:"query_cross_chain_transfer",arguments:{bridge:"lifi",chain:"bsc",tx_hash:hash}});assert.equal((transfer.structuredContent as any).data.transfers[0].on_chain_verified,false);assert.equal(calls[0].path,"/bridge/transfer");
    const result = await client.callTool({name:"find_cross_chain_candidates",arguments:collision});assert.equal((result.structuredContent as any).data.candidates[0].total_amount,"0.007631125135013813");assert.equal((result.content as any)[0].text.includes("truncated=true"),true);assert.equal(calls[1].path,"/bridge/candidates");
    const batch = await client.callTool({name:"batch_call_tools",arguments:{requests:[{tool:"query_cross_chain_transfer",arguments:{bridge:"lifi",chain:"bsc",tx_hash:hash}}]}});assert.equal((batch.structuredContent as any).data.succeeded,1);
  } finally {await client.close();await server.close()}
});
