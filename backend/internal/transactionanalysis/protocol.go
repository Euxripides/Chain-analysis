package transactionanalysis

import "strings"

type Protocol struct {
	Name           string `json:"name"`
	Contract       string `json:"contract"`
	Method         string `json:"method"`
	Nonce          string `json:"nonce"`
	Receiver       string `json:"receiver"`
	AssetContract  string `json:"asset_contract"`
	AmountRaw      string `json:"amount_raw"`
	PayoutMatched  bool   `json:"payout_matched"`
	RegistrySource string `json:"registry_source"`
	ABISource      string `json:"abi_source"`
	SourceChain    string `json:"source_chain"`
	SourceHash     string `json:"source_hash"`
}

const hotTreasury = "0x233c5370ccfb3cd7409d9a3fb98ab94de94cb4cd"
const hotRepo = "https://github.com/hot-dao/omni-sdk/blob/3728a06a4266b4ab0f75a0f7bffcf90242e3995e/src/bridge-evm/"

// This adapter identifies the BSC withdrawal instruction and verifies its actual
// payout. It deliberately does not infer a Solana/NEAR origin from the treasury.
func identifyProtocol(out *Result) {
	if out.Chain != "bsc" || strings.ToLower(Text(out.Transaction["to_address"])) != hotTreasury {
		return
	}
	input := strings.ToLower(Text(out.Transaction["input"]))
	selector := event("withdraw(uint128,address,address,uint128,bytes)")[:10]
	if len(input) < 10 || input[:10] != selector {
		return
	}
	w := words("0x" + input[10:])
	if len(w) < 6 {
		return
	}
	nonce, nok := Integer("0x" + w[0])
	amount, aok := Integer("0x" + w[3])
	if !nok || !aok || nonce.BitLen() > 128 || amount.BitLen() > 128 {
		return
	}
	token, receiver := address("0x"+w[1]), address("0x"+w[2])
	if token == "" || receiver == "" || number(w[4]) != "160" {
		return
	}
	signatureLength := number(w[5])
	if signatureLength != "65" || len(w) < 9 {
		return
	}
	p := &Protocol{Name: "HOT Bridge", Contract: hotTreasury, Method: "withdraw(uint128,address,address,uint128,bytes)", Nonce: nonce.String(), Receiver: receiver, AssetContract: token, AmountRaw: amount.String(), RegistrySource: hotRepo + "index.ts#L36", ABISource: hotRepo + "constants.ts#L17"}
	for _, m := range out.Movements {
		native := token == "0x0000000000000000000000000000000000000000" && m.Asset.Kind == "native"
		if m.From == hotTreasury && m.To == receiver && m.AmountRaw == p.AmountRaw && (native || m.Asset.Contract == token) {
			p.PayoutMatched = true
		}
	}
	out.Protocol = p
	if out.Outcome == "success" && p.PayoutMatched {
		out.Behavior = "HOT Bridge 提取到账"
		out.BehaviorEvidence = "协议 ABI + 成功的链上资金转移"
	}
	for i := range out.Participants {
		if out.Participants[i].Address == hotTreasury {
			out.Participants[i].Labels = append(out.Participants[i].Labels, Label{Name: "HOT Bridge Treasury", Source: p.RegistrySource})
		}
	}
}
