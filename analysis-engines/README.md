# EVM contract analysis engines

The stdin JSON runner combines exact-version Solidity compilation, WhatsABI, sevm, offline Heimdall and Anvil witness replay. Custom rules preserve code/storage identity, source spans, instruction PCs, amount expressions, branches, state dependencies and evidence. See [capabilities](../docs/contract-analysis-capabilities.md) and [approved development plan](../docs/contract-analysis-development-plan.md).

Build the image only after acquiring the pinned upstream binaries and generating dependency notices. Production uses the resulting digest, never a mutable tag. Node and Debian base images are pinned in Dockerfile; npm dependencies and compiler aliases are pinned in package-lock.json. Upstream binaries are unmodified.

```sh
python3 scripts/contract-analysis/install_binaries.py
node scripts/contract-analysis/license_inventory.mjs
docker build -f analysis-engines/Dockerfile -t platform-contract-analysis:20261014 analysis-engines
docker image inspect platform-contract-analysis:20261014 --format '{{.Id}}'
```

The CLI performs no external network access. Platform gateway requests happen before the isolated run and retain provider usage records. Replay forks a local witness RPC, which rejects unknown accounts, storage or blocks. Optional capability probes return method-not-found and fall back to witnessed account calls. Replay output is simulation data for local fixtures, while original transaction replay is separately labeled.

`notices/inventory.json` records the installed production package tree, npm integrity, compiler identities and source commits. It was obtained from the actual runtime image. License texts for Heimdall, Foundry and Solidity accompany the image; package licenses remain in node_modules and system licenses in /usr/share/doc. solc-js wrapper and compiler-core licenses are recorded separately.

Tests: `npm test`; formal sandbox benchmark: `node scripts/contract-analysis/isolated_benchmark.mjs`; actual local EVM scenarios: `node scripts/contract-analysis/runtime_fixture_qa.mjs`. Run heavy scenarios sequentially with the isolated QA worker stopped. Private QA credentials stay outside source and exported artifacts.
