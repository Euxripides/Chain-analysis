package api

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

func TestContractNamespaceProjectionRootAndIdentity(t *testing.T) {
	root, _ := new(big.Int).SetString("183a6125c38840424c4a85fa12bab2ab606c4b6d0e7cc73c0c06ba5300eab500", 16)
	storage := map[string]any{"layout_status": "erc7201_ast_layout_with_verified_source_pointer_assignment", "state_ast_id": 42, "namespace_id": "example.main", "namespace_root_slot": root.String()}
	projection := map[string]any{"namespace_id": "example.main", "base_slot": root.String()}
	if !contractNamespaceProjection(storage, projection) {
		t.Fatal("published namespace root rejected")
	}
	for _, change := range []map[string]any{{"namespace_id": "other"}, {"base_slot": "0"}, {"base_slot": new(big.Int).Add(root, big.NewInt(1)).String()}} {
		candidate := rowMap(ca.Row(projection))
		for k, v := range change {
			candidate[k] = v
		}
		if contractNamespaceProjection(storage, candidate) {
			t.Fatalf("mismatching namespace identity accepted: %+v", candidate)
		}
	}
	storage["layout_status"] = "erc7201_annotation_only"
	if contractNamespaceProjection(storage, projection) {
		t.Fatal("annotation without actual pointer assignment accepted")
	}
}

func TestContractNamespaceBalanceProjectionActualRuntime(t *testing.T) {
	base := os.Getenv("CONTRACT_NAMESPACE_RUNTIME")
	if base == "" {
		t.Skip("actual frozen namespace EVM corpus required")
	}
	load := func(name string, into any) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(base, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatal(err)
		}
	}
	holder := "0x" + strings.Repeat("3", 40)
	root, _ := new(big.Int).SetString("183a6125c38840424c4a85fa12bab2ab606c4b6d0e7cc73c0c06ba5300eab500", 16)
	expected, _ := contractMappingSlot(holder, new(big.Int).Add(root, big.NewInt(1)).String())
	for _, optimized := range []bool{false, true} {
		for _, viaIR := range []bool{false, true} {
			for _, group := range []string{"good", "aliases", "assembly", "coupled", "inflation", "bypass", "pollution", "bad-root", "override"} {
				t.Run(fmt.Sprintf("%s-%t-ir-%t", group, optimized, viaIR), func(t *testing.T) {
					label := fmt.Sprintf("%s-%t-ir-%t", group, optimized, viaIR)
					var input ca.Input
					var result ca.Result
					load(label+"-input.json", &input)
					load(label+"-analysis.json", &result)
					asset := input.Snapshot.StorageAddress
					pre := map[string]ca.StateAccount{asset: {Code: input.Bytecode}}
					projection := contractBalanceProjection(&result, asset, pre)
					if group == "bad-root" || group == "override" {
						if projection != nil {
							t.Fatal("unverified runtime accessor acquired a balance projection")
						}
						return
					}
					if projection == nil {
						t.Fatal("verified original source namespace projection missing")
					}
					key, err := projection.key(holder)
					if err != nil || key != expected {
						t.Fatalf("physical mapping slot mismatch %s %s %v", key, expected, err)
					}
					var witness struct {
						StateDiff ca.StateDiff `json:"state_diff"`
					}
					load(label+"-mint-witness.json", &witness)
					value, err := projection.value(witness.StateDiff.Post[asset].Storage[key])
					expectedValue := "17"
					if group == "coupled" {
						expectedValue = "459"
					}
					if err != nil || value != expectedValue {
						t.Fatal("real transaction balance delta was not decoded", value, err)
					}
					pre[asset] = ca.StateAccount{Code: "0x6000"}
					if contractBalanceProjection(&result, asset, pre) != nil {
						t.Fatal("changed transaction runtime accepted")
					}
				})
			}
		}
	}
}
