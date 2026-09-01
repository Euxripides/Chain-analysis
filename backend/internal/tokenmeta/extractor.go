package tokenmeta

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var evmAddress = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

func normalizeAddress(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func validEVMAddress(value string) bool { return evmAddress.MatchString(normalizeAddress(value)) }

func IsValidContractAddress(value string) bool { return validEVMAddress(value) }

func ExtractPage(chain, source string, rows []json.RawMessage) []Observation {
	// A page can contain 10k transfers but should produce at most one observation
	// for each chain+contract. Merge only fields observed for the same contract.
	byContract := make(map[string]Observation)
	for _, raw := range rows {
		var item map[string]any
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		contract := normalizeAddress(stringValue(item, "contractAddress", "tokenAddress"))
		// Provider/raw rows sometimes contain truncated identifiers. They are not
		// contracts and must never enter the metadata catalog or RPC queue.
		if !validEVMAddress(contract) {
			continue
		}
		obs := byContract[contract]
		if obs.ContractAddress == "" {
			sum := sha256.Sum256(raw)
			obs = Observation{Chain: strings.ToLower(chain), ContractAddress: contract, Source: source,
				ObservedAt: time.Now().UTC(), RawHash: hex.EncodeToString(sum[:])}
		}
		fillString(&obs.Name, stringValue(item, "tokenName", "name"))
		fillString(&obs.Symbol, stringValue(item, "tokenSymbol", "symbol"))
		fillString(&obs.TokenType, stringValue(item, "tokenType", "type"))
		fillString(&obs.LogoURL, stringValue(item, "thumbnail", "logo", "logoUrl"))
		if obs.Decimals == nil {
			if n, ok := intValue(item, "tokenDecimals", "decimals"); ok {
				obs.Decimals = &n
			}
		}
		if obs.BlockNumber == nil {
			if n, ok := uintValue(item, "blockHeight", "blockNumber"); ok {
				obs.BlockNumber = &n
			}
		}
		byContract[contract] = obs
	}
	out := make([]Observation, 0, len(byContract))
	for _, obs := range byContract {
		Validate(&obs)
		out = append(out, obs)
	}
	return out
}

func Validate(obs *Observation) {
	obs.Valid = true
	var problems []string
	obs.Chain = strings.ToLower(strings.TrimSpace(obs.Chain))
	obs.ContractAddress = normalizeAddress(obs.ContractAddress)
	obs.Name = strings.TrimSpace(strings.TrimRight(obs.Name, "\x00"))
	obs.Symbol = strings.TrimSpace(strings.TrimRight(obs.Symbol, "\x00"))
	obs.TokenType = strings.TrimSpace(obs.TokenType)
	if obs.Chain == "" {
		problems = append(problems, "chain required")
	}
	if !evmAddress.MatchString(obs.ContractAddress) {
		problems = append(problems, "invalid contract address")
	}
	if len([]rune(obs.Name)) > 255 {
		problems = append(problems, "name exceeds 255 characters")
	}
	if len([]rune(obs.Symbol)) > 128 {
		problems = append(problems, "symbol exceeds 128 characters")
	}
	if len(obs.TokenType) > 32 {
		problems = append(problems, "token type exceeds 32 characters")
	}
	if len(obs.LogoURL) > 2048 {
		problems = append(problems, "logo url exceeds 2048 characters")
	}
	if obs.Decimals != nil && (*obs.Decimals < 0 || *obs.Decimals > 255) {
		problems = append(problems, "decimals outside 0..255")
	}
	if obs.Source == "" {
		problems = append(problems, "source required")
	}
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = time.Now().UTC()
	}
	if len(problems) > 0 {
		obs.Valid = false
		obs.ValidationError = strings.Join(problems, "; ")
	}
}

func fillString(target *string, value string) {
	if *target == "" && value != "" {
		*target = value
	}
}
func stringValue(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}
func intValue(m map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(v)))
			if err == nil {
				return n, true
			}
		}
	}
	return 0, false
}
func uintValue(m map[string]any, keys ...string) (uint64, bool) {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			s := strings.TrimSpace(fmt.Sprint(v))
			if strings.HasPrefix(s, "0x") {
				n, e := strconv.ParseUint(s[2:], 16, 64)
				if e == nil {
					return n, true
				}
			}
			n, e := strconv.ParseUint(s, 10, 64)
			if e == nil {
				return n, true
			}
		}
	}
	return 0, false
}
