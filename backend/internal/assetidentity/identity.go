// Package assetidentity keeps display symbols separate from chain/contract identity.
package assetidentity

import (
	"fmt"
	"strings"
)

type Record struct{ Chain, Asset, Contract, Source string }

var records = []Record{
	{"bsc", "USDT", "0x55d398326f99059ff775485246999027b3197955", "https://github.com/trustwallet/assets/blob/master/blockchains/smartchain/assets/0x55d398326f99059fF775485246999027B3197955/info.json"},
	{"bsc", "USDC", "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d", "https://github.com/trustwallet/assets/blob/master/blockchains/smartchain/assets/0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d/info.json"},
	{"eth", "USDT", "0xdac17f958d2ee523a2206206994597c13d831ec7", "https://tether.to/en/supported-protocols/"},
	{"eth", "USDC", "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", "https://developers.circle.com/stablecoins/usdc-contract-addresses"},
}

func canonicalChain(c string) string {
	if c == "ethereum" {
		return "eth"
	}
	return c
}
func Resolve(c, token, contract string) (string, string, error) {
	c = canonicalChain(c)
	token = strings.TrimSpace(token)
	contract = strings.TrimSpace(contract)
	if contract != "" {
		return "", contract, nil
	} // Contract always takes precedence over a display symbol.
	if token == "" {
		return "", "", nil
	}
	if strings.HasPrefix(strings.ToLower(token), "0x") || (c == "solana" && len(token) >= 32) || (c == "tron" && strings.HasPrefix(token, "T") && len(token) == 34) {
		return "", token, nil
	}
	if c == "solana" && strings.EqualFold(token, "SOL") {
		return "SOL", "", nil
	}
	for _, r := range records {
		if r.Chain == c && strings.EqualFold(token, r.Asset) {
			return "", r.Contract, nil
		}
	}
	return "", "", fmt.Errorf("token symbol is display-only; provide the exact token_contract for this chain")
}
func skeleton(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	var out strings.Builder
	for _, r := range s {
		if r >= 0xff01 && r <= 0xff5e {
			r -= 0xfee0
		}
		switch r {
		case '5', 'Ⴝ', 'Ѕ':
			r = 'S'
		case 'Τ', 'Т':
			r = 'T'
		case 'υ', 'Υ', 'Ս':
			r = 'U'
		case 'Ｄ':
			r = 'D'
		}
		out.WriteRune(r)
	}
	if out.String() == "BSC-USD" {
		return "USDT"
	}
	return out.String()
}
func Describe(c, contract, symbol string) map[string]any {
	c = canonicalChain(c)
	out := map[string]any{"canonical_asset": nil, "canonical_contract": nil, "verified_token": false, "symbol_confusable": false, "spam_suspected": false, "identity_source": nil, "identity_registry_version": "2026-10-01", "identity_basis": "Chain and exact contract; verified_token means curated-registry match, not ABI or issuer solvency verification"}
	for _, r := range records {
		if r.Chain == c && strings.EqualFold(r.Contract, contract) {
			out["canonical_asset"] = r.Asset
			out["canonical_contract"] = r.Contract
			out["verified_token"] = true
			out["identity_source"] = r.Source
			return out
		}
	}
	for _, r := range records {
		if r.Chain == c && skeleton(symbol) == r.Asset {
			out["symbol_confusable"] = true
			out["spam_suspected"] = true
			out["identity_note"] = "Symbol resembles a registry asset but contract differs; heuristic suspicion is not proof of fraud"
			break
		}
	}
	return out
}
