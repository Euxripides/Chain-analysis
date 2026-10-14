package contractanalysis

// EthereumHardfork uses Ethereum mainnet activation points. Other EVM chains
// require their own execution-rules adapter; Ethereum dates are never reused.
// Reference: go-ethereum params/chainspecs/mainnet.json, checked 2026-10-14.
func EthereumHardfork(chainID string, block, timestamp uint64) string {
	if chainID != "1" {
		return "chain_rules_unavailable"
	}
	switch {
	case timestamp >= 1764798551:
		return "osaka"
	case timestamp >= 1746612311:
		return "prague"
	case timestamp >= 1710338135:
		return "cancun"
	case timestamp >= 1681338455:
		return "shanghai"
	case block >= 15537394:
		return "paris"
	case block >= 12965000:
		return "london"
	case block >= 12244000:
		return "berlin"
	case block >= 9069000:
		return "istanbul"
	case block >= 7280000:
		return "petersburg"
	case block >= 4370000:
		return "byzantium"
	case block >= 2675000:
		return "spuriousDragon"
	case block >= 2463000:
		return "tangerine"
	case block >= 1150000:
		return "homestead"
	default:
		return "frontier"
	}
}
