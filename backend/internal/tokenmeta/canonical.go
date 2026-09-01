package tokenmeta

import (
	"sort"
	"strings"
)

type candidate[T comparable] struct {
	value  T
	source string
	rank   int
}

func sourceRank(source string) int {
	switch source {
	case "admin_override":
		return 600
	case "verified_explorer":
		return 500
	case "rpc", "rpc_symbol", "rpc_name", "rpc_decimals":
		return 400
	case "ankr_token_transfer", "ankr_account_balance":
		return 300
	case "raw_ankr":
		return 250
	case "clickhouse_history":
		return 200
	case "import":
		return 100
	default:
		return 0
	}
}

func Resolve(observations []Observation) Metadata {
	valid := make([]Observation, 0, len(observations))
	for _, obs := range observations {
		if obs.Valid {
			valid = append(valid, obs)
		}
	}
	// Deterministic tie-breaking independent of database order.
	sort.Slice(valid, func(i, j int) bool {
		if sourceRank(valid[i].Source) != sourceRank(valid[j].Source) {
			return sourceRank(valid[i].Source) > sourceRank(valid[j].Source)
		}
		if valid[i].Source != valid[j].Source {
			return valid[i].Source < valid[j].Source
		}
		return valid[i].RawHash < valid[j].RawHash
	})
	meta := Metadata{Confidence: "unknown", MetadataStatus: StatusUnknown}
	if len(valid) > 0 {
		meta.Chain = valid[0].Chain
		meta.ContractAddress = valid[0].ContractAddress
	}
	name, nameSources, nameConflict := chooseString(valid, func(o Observation) string { return o.Name })
	symbol, symbolSources, symbolConflict := chooseString(valid, func(o Observation) string { return o.Symbol })
	tokenType, typeSources, typeConflict := chooseString(valid, func(o Observation) string { return o.TokenType })
	decimals, decimalSources, decimalConflict := chooseInt(valid)
	logo, logoSources, _ := chooseString(valid, func(o Observation) string { return o.LogoURL })
	meta.Name, meta.Symbol, meta.TokenType, meta.Decimals, meta.LogoURL = name, symbol, tokenType, decimals, logo
	sources := append(append(append(append(nameSources, symbolSources...), typeSources...), decimalSources...), logoSources...)
	meta.MetadataSource = strings.Join(uniqueSorted(sources), "+")
	conflict := nameConflict || symbolConflict || typeConflict || decimalConflict
	fields := 0
	if name != "" {
		fields++
	}
	if symbol != "" {
		fields++
	}
	if decimals != nil {
		fields++
	}
	switch {
	case conflict:
		meta.MetadataStatus = StatusConflict
		meta.Confidence = "low"
	case fields == 3:
		meta.MetadataStatus = StatusComplete
		meta.Confidence = confidenceForSources(sources)
	case fields > 0:
		meta.MetadataStatus = StatusPartial
		meta.Confidence = confidenceForSources(sources)
	default:
		meta.MetadataStatus = StatusUnknown
	}
	for _, o := range valid {
		if sourceRank(o.Source) >= 500 {
			meta.Verified = true
			break
		}
	}
	return meta
}

func chooseString(obs []Observation, get func(Observation) string) (string, []string, bool) {
	values := map[string]bool{}
	chosen := ""
	rank := -1
	var sources []string
	for _, o := range obs {
		v := strings.TrimSpace(get(o))
		if v == "" {
			continue
		}
		values[v] = true
		r := sourceRank(o.Source)
		if r > rank || (r == rank && (chosen == "" || v < chosen)) {
			chosen = v
			rank = r
			sources = []string{o.Source}
		} else if r == rank && v == chosen {
			sources = append(sources, o.Source)
		}
	}
	return chosen, sources, len(values) > 1
}
func chooseInt(obs []Observation) (*int, []string, bool) {
	values := map[int]bool{}
	var chosen *int
	rank := -1
	var sources []string
	for _, o := range obs {
		if o.Decimals == nil {
			continue
		}
		v := *o.Decimals
		values[v] = true
		r := sourceRank(o.Source)
		if r > rank || (r == rank && (chosen == nil || v < *chosen)) {
			x := v
			chosen = &x
			rank = r
			sources = []string{o.Source}
		} else if r == rank && chosen != nil && v == *chosen {
			sources = append(sources, o.Source)
		}
	}
	return chosen, sources, len(values) > 1
}
func uniqueSorted(in []string) []string {
	m := map[string]bool{}
	for _, v := range in {
		if v != "" {
			m[v] = true
		}
	}
	out := make([]string, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
func confidenceForSources(s []string) string {
	best := 0
	for _, v := range s {
		if r := sourceRank(v); r > best {
			best = r
		}
	}
	if best >= 400 {
		return "high"
	}
	if best >= 250 {
		return "medium"
	}
	if best > 0 {
		return "low"
	}
	return "unknown"
}
