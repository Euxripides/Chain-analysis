import assert from "node:assert/strict";
import { createServer, type Server } from "node:http";
import { once } from "node:events";
import { createHash } from "node:crypto";
import type { AddressInfo } from "node:net";
import test, { type TestContext } from "node:test";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { createApp } from "../src/server.js";
import { sha256, type Config } from "../src/config.js";
import { toolDefinitions } from "../src/tools.js";

const a = "0x11111111111111111111111111111111111111aa";
const b = "0x2222222222222222222222222222222222222222";
const c = "0x3333333333333333333333333333333333333333";
const job = "00000000-0000-4000-8000-000000000001";
const precise = "900719925474099312.345678901234567893";
type Received = { path: string; method: string; input: Record<string, unknown> };

async function harness(t: TestContext) {
  const received: Received[] = [];
  let active = 0, peak = 0;
  const rest = createServer((req, res) => {
    assert.equal(req.headers.authorization, "Bearer rest-test");
    const chunks: Buffer[] = [];
    req.on("data", data => chunks.push(Buffer.from(data)));
    req.on("end", () => {
      const url = new URL(req.url!, "http://localhost");
      const input: Record<string, unknown> = req.method === "POST"
        ? JSON.parse(Buffer.concat(chunks).toString("utf8"))
        : Object.fromEntries(url.searchParams);
      received.push({ path: url.pathname.replace("/api/v1", ""), method: req.method!, input });
      active++; peak = Math.max(peak, active);
      setTimeout(() => {
        active--;
        res.setHeader("content-type", "application/json");
        if (input.address === "bad" || input.job_id === "00000000-0000-4000-8000-000000000999") {
          res.statusCode = 400;
          res.end(JSON.stringify({ success: false, data: null, meta: {}, error: { code: "INVALID_ADDRESS", message: "Invalid complete address" } }));
        } else if (["/api/v1/export/get_export_file", "/api/v1/export/download_export"].includes(url.pathname)) {
          const bytes = Buffer.from("payer,amount\n" + a + "," + precise + "\n");
          res.end(JSON.stringify({ success: true, error: null, meta: {}, data: { content_base64: bytes.toString("base64"),
            chunk_bytes: bytes.length, chunk_sha256: createHash("sha256").update(bytes).digest("hex"),
            resource_uri: `export://${input.job_id}/0/${input.chunk_size}`, mime_type: "text/csv" } }));
        } else if(url.pathname==="/api/v1/export/contracts/get_contract_analysis_artifact") {
          const bytes=Buffer.from("contract evidence\n"),digest=createHash("sha256").update(bytes).digest("hex");
          res.end(JSON.stringify({success:true,error:null,meta:{},data:{content_base64:bytes.toString("base64"),byte_length:bytes.length,sha256:digest,chunk_sha256:digest,artifact:{media_type:"text/plain"},resource_uri:`contract-analysis://${input.analysis_id}/${input.artifact_id}/0/1048576`}}));
        } else {
          res.end(JSON.stringify({ success: true, error: null,
            data: { address: input.address, total_records: 3, total_amount: precise,
              data_cutoff_block: "113953471", next_cursor: `next.${input.address}`,
              next_detail_cursor: `detail.${input.address}`, input,
              addresses: [{ from_address: a, total_amount: precise }],
              details: [{ amount: precise, fields: { log_index: "123", amount_raw: "900719925474099312345678901234567893" } }],
            }, meta: { source: "completed_export_file", evidence: "original" },
          }));
        }
      }, input.address === a ? 70 : 15);
    });
  });
  rest.listen(0, "127.0.0.1"); await once(rest, "listening");
  const config: Config = {
    host: "127.0.0.1", allowedHosts: ["127.0.0.1"], port: 0,
    restBaseUrl: `http://127.0.0.1:${(rest.address() as AddressInfo).port}/api/v1`, restApiKey: "rest-test",
    mcpKeyHashes: [sha256("mcp-test")], oauthAccessCodeHashes: [sha256("mcp-test")],
    oauthSigningKey: sha256("signing-test"), publicBaseUrl: "https://aurore.online", publicMcpUrl: "https://aurore.online/mcp",
    requestTimeoutMs: 5000, maxConcurrent: 4, rateLimitPerMinute: 200,
  };
  const mcp: Server = createServer(createApp(config));
  mcp.listen(0, "127.0.0.1"); await once(mcp, "listening");
  const client = new Client({ name: "batch-test", version: "1.0.0" });
  await client.connect(new StreamableHTTPClientTransport(new URL(`http://127.0.0.1:${(mcp.address() as AddressInfo).port}/mcp`), { requestInit: { headers: { authorization: "Bearer mcp-test" } } }));
  t.after(async () => {
    await client.close();
    await Promise.all([new Promise<void>(resolve => rest.close(() => resolve())), new Promise<void>(resolve => mcp.close(() => resolve()))]);
  });
  return { client, received, peak: () => peak };
}

function data(result: Awaited<ReturnType<Client["callTool"]>>) {
  assert.equal(result.isError, undefined, JSON.stringify(result.content));
  return (result.structuredContent as any).data;
}

test("200 address batch executes every item with bounded concurrency and preserves a partial error", async t => {
  const h = await harness(t);
  const addresses = Array.from({ length: 199 }, (_, i) => `0x${(i + 100).toString(16).padStart(40, "0")}`);
  addresses.push("bad");
  const out = data(await h.client.callTool({ name: "batch_get_transactions", arguments: { chain: "bsc", addresses, max_concurrent: 4, page_size: 1 } }));
  assert.deepEqual([out.requested, out.succeeded, out.failed], [200, 199, 1]);
  assert.equal(h.received.length, 200);
  assert.equal(h.peak(), 4);
  assert.equal(out.results[199].address, "bad");
  assert.equal(out.results[199].error.code, "INVALID_ADDRESS");
  assert.deepEqual(out.results.map((item: any) => item.index), addresses.map((_, i) => i));
});

test("address batches bound concurrency, deduplicate EVM addresses and retain request order after partial failure", async t => {
  const h = await harness(t);
  const result = await h.client.callTool({ name: "batch_get_token_transfers", arguments: {
    chain: "bsc", addresses: [a, b, a.toUpperCase(), "bad", c], max_concurrent: 2,
    pagination: [{ address: a.toUpperCase(), page: 2, page_size: 7 }, { address: b, page: 9 }],
  } });
  const out = data(result);
  assert.equal((result.structuredContent as any).success, false);
  assert.equal((result.structuredContent as any).error.code, "BATCH_PARTIAL_FAILURE");
  assert.deepEqual([out.requested, out.succeeded, out.failed], [4, 3, 1]);
  assert.deepEqual(out.results.map((x: any) => x.address), [a, b, "bad", c]);
  assert.deepEqual(out.results.map((x: any) => x.index), [0, 1, 2, 3]);
  assert.equal(out.results[0].response.data.total_amount, precise);
  assert.equal(out.results[0].response.meta.evidence, "original");
  assert.equal(out.results[0].response.data.input.page, "2");
  assert.equal(out.results[0].response.data.input.page_size, "7");
  assert.equal(out.results[1].response.data.input.page, "9");
  assert.equal(out.results[1].response.data.input.page_size, "25");
  assert.equal(out.results[2].error.code, "INVALID_ADDRESS");
  assert.equal(h.peak(), 2);
});

test("batch summaries preserve precise totals, original evidence and independent address/detail cursors", async t => {
  const h = await harness(t);
  const result = await h.client.callTool({ name: "batch_get_export_address_summary", arguments: {
    job_id: job, token: "USDT", addresses: [a, b], direction: "in", page_size: 1, detail_page_size: 2,
    pagination: [{ address: a, cursor: "a.current", detail_cursor: "a.detail" }, { address: b, cursor: "b.current", detail_cursor: "b.detail", detail_page_size: 3 }],
  } });
  const out = data(result);
  assert.equal(out.failed, 0);
  assert.equal(h.peak(), 2);
  assert.equal(result.content.length, 1);
  for (const [index, address] of [a, b].entries()) {
    const original = out.results[index].response;
    assert.equal(original.data.total_amount, precise);
    assert.equal(original.data.data_cutoff_block, "113953471");
    assert.equal(original.data.next_cursor, `next.${address}`);
    assert.equal(original.data.details[0].fields.amount_raw, "900719925474099312345678901234567893");
    assert.equal(original.data.input.detail_cursor, index ? "b.detail" : "a.detail");
    assert.equal(original.data.input.cursor, index ? "b.current" : "a.current");
  }
});

test("Solana batch cursors remain independent and address case is preserved", async t => {
  const h = await harness(t);
  const result = await h.client.callTool({ name: "batch_get_solana_address_signatures", arguments: {
    addresses: ["So11111111111111111111111111111111111111112", "so11111111111111111111111111111111111111112"], pagination: [{ address: "So11111111111111111111111111111111111111112", before: "5eDPXxyeaHuK9YJKPXpXbWUne9At11oQzdJv6hLmuVqQqneiiYdAMFDn7CYn9Edc9RQWbULAnv33GK34mnoSinfZ" }, { address: "so11111111111111111111111111111111111111112", before: "6eDPXxyeaHuK9YJKPXpXbWUne9At11oQzdJv6hLmuVqQqneiiYdAMFDn7CYn9Edc9RQWbULAnv33GK34mnoSinfZ" }],
  } });
  const out = data(result);
  assert.deepEqual(out.results.map((x: any) => x.address), ["So11111111111111111111111111111111111111112", "so11111111111111111111111111111111111111112"]);
  assert.deepEqual(h.received.map(x => x.input.before), ["5eDPXxyeaHuK9YJKPXpXbWUne9At11oQzdJv6hLmuVqQqneiiYdAMFDn7CYn9Edc9RQWbULAnv33GK34mnoSinfZ", "6eDPXxyeaHuK9YJKPXpXbWUne9At11oQzdJv6hLmuVqQqneiiYdAMFDn7CYn9Edc9RQWbULAnv33GK34mnoSinfZ"]);
});

test("all address/hash aliases advertise batch schemas and dispatch their original REST operation", async t => {
  const h = await harness(t);
  const listed = (await h.client.listTools()).tools;
  const aliases = toolDefinitions.filter(x => x.batch?.kind === "addresses");
  assert.equal(aliases.length, 24);
  for (const alias of aliases) {
    const schema = listed.find(x => x.name === alias.name)!;
    assert.equal(schema.annotations?.readOnlyHint, alias.readOnly !== false);
    assert.equal((schema.inputSchema.properties![alias.batch?.kind === "addresses" && alias.batch.key === "tx_hash" ? "tx_hashes" : "addresses"] as any).maxItems, 200);
    let args: Record<string,unknown> = alias.batch?.kind === "addresses" && alias.batch.key === "tx_hash" ? { chain: "bsc", tx_hashes: ["0x" + "1".repeat(64)] } : ["batch_get_balances", "batch_get_contract_control", "batch_get_labels"].includes(alias.name) ? { chain: "bsc", addresses: [a] } : alias.name === "batch_get_export_address_summary"
      ? { addresses: [a], job_id: job, token: "USDT" }
      : alias.name === "batch_get_solana_address_signatures" ? { addresses: ["So11111111111111111111111111111111111111112"] }
      : { chain: "bsc", addresses: [a], token: "USDT" };
    if (alias.name === "batch_reconcile_address_ledgers") args={chain:"bsc",addresses:[a],token_contract:b,from_block:1,to_block:2};
    if (alias.name === "batch_get_token_allowances") args={chain:"bsc",addresses:[a],token_contract:b,spenders:[b]};
    if (alias.name === "batch_get_control_history") args={chain:"bsc",addresses:[a],from_block:1,to_block:2};
    if (alias.name === "batch_match_bridge_transfers") Object.assign(args,{destination_chain:"ethereum",destination_tx_hash:"0x"+"2".repeat(64),protocol:"cctp_v1"});
    const out = data(await h.client.callTool({ name: alias.name, arguments: args }));
    assert.equal(out.failed, 0, alias.name);
    assert.equal(h.received.at(-1)!.path, alias.path);
    assert.equal(h.received.at(-1)!.method, alias.method ?? "GET");
    if (alias.name === "batch_find_fund_paths") assert.equal(h.received.at(-1)!.input.source, a);
  }
});

test("invalid batch inputs are rejected before any REST request including an earlier write", async t => {
  const h = await harness(t);
  const validExport = { chain: "bsc", address: a, data_types: ["token_transfers"], format: "csv", compression: "zip" };
  const calls = [
    { name: "batch_get_transactions", arguments: { chain: "bsc", addresses: [a], pagination: [{ address: b, page: 2 }] } },
    { name: "batch_get_transactions", arguments: { chain: "bsc", addresses: [a], pagination: [{ address: a, page: 2 }, { address: a, page: 3 }] } },
    { name: "batch_get_transactions", arguments: { chain: "bsc", addresses: [a], pagination: [{ address: a, cursor: "unsupported" }] } },
    { name: "batch_get_transactions", arguments: { chain: "bsc", addresses: [" "] } },
    { name: "batch_get_transactions", arguments: { chain: "bsc", addresses: Array(201).fill(a) } },
    { name: "batch_call_tools", arguments: { requests: [{ tool: "create_large_export", arguments: validExport }, { tool: "find_fund_paths", arguments: { chain: "bsc", source: a } }] } },
    { name: "batch_call_tools", arguments: { requests: [{ tool: "create_large_export", arguments: validExport }, { tool: "start_analysis", arguments: {operation:"get_token_allowances",arguments:{chain:"bsc",address:a,token_contract:b}} }] } },
    { name: "batch_call_tools", arguments: { requests: [{ tool: "create_large_export", arguments: validExport }, { tool: "reconcile_address_ledger", arguments: {chain:"bsc",address:a,token_contract:b,from_block:5,to_block:1} }] } },
    { name: "batch_call_tools", arguments: { requests: [{ tool: "create_large_export", arguments: validExport }, { tool: "create_large_export", arguments: { ...validExport, addresses: [b] } }] } },
    { name: "batch_call_tools", arguments: { requests: [{ tool: "batch_get_transactions", arguments: { chain: "bsc", addresses: [a] } }] } },
    { name: "batch_call_tools", arguments: { requests: [{ tool: "get_transactions", arguments: { chain: "bsc", address: a, page_size: 501 } }] } },
  ];
  for (const call of calls) {
    const result = await h.client.callTool(call);
    assert.equal(result.isError, true, JSON.stringify(call));
    assert.equal(h.received.length, 0, "invalid batch must have no side effects");
  }
});

test("generic batches dispatch every existing single operation with its original GET/POST semantics", async t => {
  const h = await harness(t);
  const samples: Record<string, Record<string, unknown>> = {
    get_contract_analysis_capabilities: {},
    get_address_overview: { chain: "bsc", address: a }, get_address_assets: { chain: "bsc", address: a },
    get_token_transfers: { chain: "bsc", address: a }, get_nft_transfers: { chain: "flare", address: a }, get_address_logs: { chain: "gnosis", address: a }, get_balance_snapshots: { chain: "linea", address: a }, get_transactions: { chain: "bsc", address: a },
    get_internal_transactions: { chain: "bsc", address: a }, get_counterparties: { chain: "bsc", address: a },
    get_flow_summary: { chain: "bsc", address: a }, get_top_flows: { chain: "bsc", address: a }, get_first_funding: { chain: "bsc", address: a },
    get_transaction_detail: { chain: "bsc", tx_hash: "hash" }, get_solana_address_signatures: { address: "So11111111111111111111111111111111111111112" },
    get_solana_transaction: { signature: "5eDPXxyeaHuK9YJKPXpXbWUne9At11oQzdJv6hLmuVqQqneiiYdAMFDn7CYn9Edc9RQWbULAnv33GK34mnoSinfZ" }, get_bridge_source_transaction: { source_chain: "solana", source_tx_hash: "5eDPXxyeaHuK9YJKPXpXbWUne9At11oQzdJv6hLmuVqQqneiiYdAMFDn7CYn9Edc9RQWbULAnv33GK34mnoSinfZ" },
    get_transaction_trace: { chain: "bsc", tx_hash: "hash" }, get_transaction_logs: { chain: "bsc", tx_hash: "hash" }, decode_transaction: { chain: "bsc", tx_hash: "hash" },
    find_fund_paths: { chain: "bsc", source: a, token: "USDT" }, build_address_network: { chain: "bsc", seed_addresses: [a, b], token: "USDT" },
    create_large_export: { chain: "bsc", addresses: [a, b], data_types: ["token_transfers"], format: "parquet", compression: "zstd" },
    get_export_status: { job_id: job }, get_export_address_summary: { job_id: job, token: "USDT" },
    ensure_chain_data: { chain: "bsc", addresses: [a], datasets: ["token_transfers"], range: { type: "full" } },
    get_ai_download_status: { download_job_id: job }, retry_ai_download_failures: { download_job_id: job },
  };
  const ih = "0x" + "1".repeat(64);
  Object.assign(samples, {
    get_cross_chain_capabilities:{},
    query_cross_chain_transfer:{bridge:"lifi",chain:"bsc",tx_hash:ih},
    find_cross_chain_candidates:{anchor:{chain:"ethereum",tx_hash:ih,dataset:"transactions"},search_chain:"bsc",search_addresses:[a],search_direction:"out",direction:"forward",asset_kind:"native",asset_relation:"same_asset",start_time:"2026-09-29T00:00:00Z",end_time:"2026-09-29T01:00:00Z"},
    get_investigation_capabilities:{chain:"bsc"},
    analyze_contract:{chain:"bsc",address:a},
    get_contract_analysis:{analysis_id:job},
    list_contract_methods:{analysis_id:job},
    get_contract_method_analysis:{analysis_id:job,method_id:"1".repeat(40)},
    get_contract_execution_flow:{analysis_id:job},
    explain_contract_transaction:{analysis_id:job,tx_hash:ih},
    compare_contract_versions:{analysis_id:job,compare_analysis_id:job},
    get_contract_analysis_artifact:{analysis_id:job,artifact_id:"1".repeat(40)},
    reconcile_address_ledger:{chain:"bsc",address:a,token_contract:b,from_block:1,to_block:2},
    trace_source_set:{chain:"bsc",token_contract:b,sources:[{tx_hash:ih,log_index:0}]},
    explain_transaction:{chain:"bsc",tx_hash:ih},
    match_bridge_transfer:{chain:"ethereum",tx_hash:ih,destination_chain:"base",destination_tx_hash:ih,protocol:"cctp_v1"},
    get_token_allowances:{chain:"bsc",address:a,token_contract:b,spenders:[b]},
    get_control_history:{chain:"bsc",address:a,from_block:1,to_block:2},
    start_analysis:{operation:"explain_transaction",arguments:{chain:"bsc",tx_hash:ih},idempotency_key:"test"},
    get_analysis_status:{analysis_id:job},get_analysis_results:{analysis_id:job},cancel_analysis:{analysis_id:job},resume_analysis:{analysis_id:job},
    create_evidence_bundle:{analysis_id:job},get_evidence_bundle:{bundle_id:job},verify_evidence_bundle:{bundle_id:job},
    create_watch_rule:{chain:"bsc",address:a,name:"test",rule_type:"balance_change"},
    update_watch_rule:{chain:"bsc",address:a,name:"test",rule_type:"balance_change",rule_id:job,expected_revision:1},
    set_watch_rule_state:{rule_id:job,expected_revision:1,state:"paused"},list_watch_rules:{},get_watch_events:{}
  });
  for (const definition of toolDefinitions.filter(x => !x.batch && x.path.startsWith("/analysis/"))) {
    if (samples[definition.name]) continue;
    const base: Record<string, unknown> = { chain: "bsc" };
    const shape = definition.inputSchema as Record<string, any>;
    if (shape.address) base.address = a;
    if (shape.source) base.source = a;
 if(shape.source_address) {base.source_address=a;base.source_tx_hash=ih;}
 if(shape.addresses) continue;
    if (shape.token_contract) base.token_contract = b;
    if (shape.tx_hash) base.tx_hash = ih;
    if (shape.source_tx) base.source_tx = ih;
    if (shape.timestamp) base.timestamp = "2026-09-29T00:00:00Z";
    if (shape.start) base.start = "2026-09-28T00:00:00Z";
    if (shape.end) base.end = "2026-09-29T00:00:00Z";
    if (shape.function) base.function = "owner()";
    if (shape.position) base.position = "0x0";
    if (shape.call) base.call = { to: a, data: "0x" };
    if (shape.filter) base.filter = { fromBlock: "0x1", toBlock: "0x2" };
    if (shape.method) { base.method = "eth_blockNumber"; base.params = []; }
    samples[definition.name] = base;
  }
  samples.get_export_file = { job_id: job }; samples.download_export = { job_id: job };
  const single = toolDefinitions.filter(x => !x.batch && !x.name.startsWith("batch_") && (!x.name.endsWith("_batch") || x.name === "trace_fund_batch"));
  assert.deepEqual(Object.keys(samples).sort(), single.map(x => x.name).sort());
  for (let start = 0; start < single.length; start += 20) {
    const chunk = single.slice(start, start + 20);
    const out = data(await h.client.callTool({ name: "batch_call_tools", arguments: {
      requests: chunk.map(x => ({ tool: x.name, arguments: samples[x.name] })), max_concurrent: 1,
    } }));
    assert.equal(out.failed, 0);
    assert.deepEqual(out.results.map((x: any) => x.tool), chunk.map(x => x.name));
  }
  assert.deepEqual(h.received.map(x => [x.path, x.method]), single.map(x => [x.path, x.method ?? "GET"]));
  const exp = h.received.find(x => x.path === "/export/create")!;
  assert.deepEqual(exp.input.addresses, [a, b]);
});

test("generic batch retains POST error codes and caps expensive summary concurrency", async t => {
  const h = await harness(t);
  const out = data(await h.client.callTool({ name: "batch_call_tools", arguments: { max_concurrent: 8, requests: [
    { tool: "get_export_address_summary", arguments: { job_id: job, token: "USDT", address: a } },
    { tool: "create_large_export", arguments: { chain: "bsc", address: "bad", data_types: ["token_transfers"], format: "csv", compression: "zip" } },
    { tool: "get_export_address_summary", arguments: { job_id: job, token: "USDT", address: b } },
  ] } }));
  assert.equal(out.failed, 1);
  assert.equal(out.results[1].error.code, "INVALID_ADDRESS");
  assert.equal(out.results[0].response.data.total_amount, precise);
  assert.equal(out.results[2].success, true);
  assert.equal(h.peak(), 2);
});

test("multi-address export enforces one input form and still accepts the original single form", async t => {
  const h = await harness(t);
  const common = { chain: "bsc", data_types: ["token_transfers"], format: "csv", compression: "zip" };
  for (const args of [common, { ...common, address: a, addresses: [b] }, { ...common, addresses: [] }, { ...common, addresses: Array(101).fill(a) }]) {
    assert.equal((await h.client.callTool({ name: "create_large_export", arguments: args })).isError, true);
    assert.equal(h.received.length, 0);
  }
  data(await h.client.callTool({ name: "create_large_export", arguments: { ...common, address: a } }));
  data(await h.client.callTool({ name: "create_large_export", arguments: { ...common, addresses: [a, b] } }));
  assert.equal(h.received[0].input.address, a);
  assert.deepEqual(h.received[1].input.addresses, [a, b]);
});
