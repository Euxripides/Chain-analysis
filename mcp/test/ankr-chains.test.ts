import test from "node:test";
import assert from "node:assert/strict";
import { CHAIN_VALUES, toolDefinitions } from "../src/tools.js";
import { z } from "zod";

test("all Advanced API mainnets are accepted by download and read tools", () => {
  const chains = ["fantom", "flare", "gnosis", "linea", "scroll", "somnia_mainnet", "story_mainnet", "taiko", "telos", "xlayer"];
  for (const chain of chains) {
    assert.ok((CHAIN_VALUES as readonly string[]).includes(chain));
    for (const name of ["ensure_chain_data", "get_transactions", "get_address_overview"]) {
      const tool = toolDefinitions.find((item) => item.name === name)!;
      const args = name === "ensure_chain_data"
        ? { chain, addresses: ["0x0000000000000000000000000000000000000001"], datasets: ["transactions"], range: { type: "full" } }
        : { chain, address: "0x0000000000000000000000000000000000000001" };
      assert.ok(z.object(tool.inputSchema).safeParse(args).success, `${name} rejected ${chain}`);
    }
  }
  assert.ok(!(CHAIN_VALUES as readonly string[]).includes("eth_sepolia"));
});
