import test from "node:test";
import assert from "node:assert/strict";
import { z } from "zod";
import { toolDefinitions } from "../src/tools.js";
import { EVM_CHAIN_VALUES } from "../src/chains.js";

const address = "0x0000000000000000000000000000000000000001";
function parse(name:string,args:Record<string,unknown>) {
  const tool=toolDefinitions.find(t=>t.name===name);
  assert.ok(tool, `missing ${name}`);
  return z.object(tool.inputSchema).safeParse(args);
}
test("all Ankr mainnets can query NFT/log/snapshot and actual balances",()=>{
  for(const chain of EVM_CHAIN_VALUES) {
    for(const name of ["get_nft_transfers","get_address_logs","get_balance_snapshots","get_native_balance","get_token_balance","get_all_token_balances"]) {
      assert.ok(parse(name,{chain,address,token_contract:address}).success,`${name}: ${chain}`);
    }
    assert.ok(parse("batch_get_nft_transfers",{chain,addresses:[address]}).success);
    assert.ok(parse("ensure_chain_data",{chain,addresses:[address],datasets:["nft_transfers"],range:{type:"block",from_block:1,to_block:2}}).success);
    assert.ok(parse("ensure_chain_data_batch",{requests:[{chain,addresses:[address],datasets:["nft_transfers"],range:{type:"block",from_block:1,to_block:2}},{chain,addresses:[address],datasets:["balances"],range:{type:"full"}}]}).success);
    for(const format of ["csv","parquet"]) assert.ok(parse("create_large_export",{chain,address,data_types:["nft_transfers","balances"],format,compression:format==="csv"?"zip":"zstd"}).success);
  }
  assert.equal(parse("get_native_balance",{chain:"eth_sepolia",address}).success,false);
});
