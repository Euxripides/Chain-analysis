package api

import (
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
)

// activityPaths includes every preceding edge and keeps history separate from attribution.
func activityPaths(c, source string, edges []map[string]any) []map[string]any {
	type prefix struct {
		addresses []string
		hops      []any
	}
	prefixes := map[string]prefix{addressnorm.NormalizeKey(c, source): {addresses: []string{source}, hops: []any{}}}
	paths := []map[string]any{}
	for _, edge := range edges {
		from, to := fmt.Sprint(edge["from"]), fmt.Sprint(edge["to"])
		base, ok := prefixes[addressnorm.NormalizeKey(c, from)]
		if !ok {
			continue
		}
		addresses := append(append([]string{}, base.addresses...), to)
		hop := map[string]any{}
		for k, v := range edge {
			hop[k] = v
		}
		hop["depth"] = len(base.hops) + 1
		hop["attribution_type"] = "address_activity"
		hops := append(append([]any{}, base.hops...), hop)
		if _, exists := prefixes[addressnorm.NormalizeKey(c, to)]; !exists {
			prefixes[addressnorm.NormalizeKey(c, to)] = prefix{addresses, hops}
		}
		paths = append(paths, map[string]any{"path_id": fmt.Sprintf("path-%d", len(paths)+1), "addresses": addresses, "hops": hops, "hop_count": len(hops), "hop": hop, "total_amount": edge["amount"], "amount_interpretation": "Last hop historical aggregate; not an attributed source amount", "is_source_fund_attribution": false})
	}
	return paths
}
