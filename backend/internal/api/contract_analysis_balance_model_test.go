package api

import (
	"encoding/json"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"math/big"
	"os"
	"strings"
	"testing"
)

func TestCompiledRebaseBalanceUsesObservedIndexAndExactShares(t *testing.T) {
	raw, err := os.ReadFile("testdata/contract_balance_rebase.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Bytecode string      `json:"bytecode"`
		Snapshot ca.Snapshot `json:"snapshot"`
		Result   ca.Result   `json:"result"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	asset := fixture.Snapshot.StorageAddress
	holder := "0x" + strings.Repeat("1", 40)
	key, _ := contractMappingSlot(holder, "0")
	quantity := "900719925474099312345678901234567890"
	n, _ := new(big.Int).SetString(quantity, 10)
	pre := map[string]ca.StateAccount{asset: {Code: fixture.Bytecode, Storage: map[string]string{key: "0x" + n.Text(16), "0x1": "0x1bc16d674ec80000"}}}
	model := contractComputedBalance(&fixture.Result, asset, pre)
	if model == nil {
		t.Fatal("compiled balance expression was not recognized")
	}
	diff := ca.StateDiff{Pre: map[string]ca.StateAccount{asset: {Storage: map[string]string{"0x1": "0x1bc16d674ec80000"}}}, Post: map[string]ca.StateAccount{asset: {Storage: map[string]string{"0x1": "0x29a2241af62c0000"}}}}
	before, err := model.evaluate(holder, asset, pre, diff, false)
	if err != nil || before != new(big.Int).Mul(n, big.NewInt(2)).String() {
		t.Fatal(before, err)
	}
	after, err := model.evaluate(holder, asset, pre, diff, true)
	if err != nil || after != new(big.Int).Mul(n, big.NewInt(3)).String() {
		t.Fatal(after, err)
	}
	diff.Post[asset] = ca.StateAccount{Code: "0x6000"}
	if _, err = model.evaluate(holder, asset, pre, diff, true); err == nil {
		t.Fatal("changed runtime was accepted")
	}
	diff = ca.StateDiff{Pre: map[string]ca.StateAccount{}, Post: map[string]ca.StateAccount{}}
	missing := pre[asset]
	missing.Storage = map[string]string{key: "0x1"}
	pre[asset] = missing
	if _, err = model.evaluate(holder, asset, pre, diff, false); err == nil {
		t.Fatal("unobserved index was assumed")
	}
	missing.Storage["0x1"] = "0x2"
	missing.Storage[key] = "0x" + strings.Repeat("f", 64)
	pre[asset] = missing
	if _, err = model.evaluate(holder, asset, pre, diff, false); err == nil {
		t.Fatal("checked uint overflow was silently wrapped")
	}
	missing.Code = "0x6000"
	pre[asset] = missing
	if contractComputedBalance(&fixture.Result, asset, pre) != nil {
		t.Fatal("unrelated transaction code accepted")
	}
}

func TestContractPinnedImagesSurviveOnlyExplicitCompatibleUpgrade(t *testing.T) {
	old := "sha256:" + strings.Repeat("a", 64)
	current := "sha256:" + strings.Repeat("b", 64)
	t.Setenv("CONTRACT_ENGINE_IMAGE", current)
	t.Setenv("CONTRACT_ENGINE_PREVIOUS_IMAGES", old)
	if !contractEngineAllowed(current) || !contractEngineAllowed(old) {
		t.Fatal("pinned compatible job image rejected")
	}
	if contractEngineAllowed("sha256:"+strings.Repeat("c", 64)) || contractEngineAllowed("sha256:"+strings.Repeat("x", 64)) {
		t.Fatal("unconfigured or malformed image accepted")
	}
}
