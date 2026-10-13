// Package transactionanalysis derives transaction-local movement evidence without
// equating the executor with the owner or transfer deltas with balance snapshots.
package transactionanalysis

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
	"golang.org/x/crypto/sha3"
)

type Asset struct {
	Kind           string `json:"kind"`
	Contract       string `json:"contract"`
	Symbol         string `json:"symbol"`
	Decimals       *int   `json:"decimals"`
	TokenID        string `json:"token_id,omitempty"`
	MetadataSource string `json:"metadata_source,omitempty"`
}
type Evidence struct {
	Kind   string         `json:"kind"`
	Index  string         `json:"index"`
	Source string         `json:"source"`
	Raw    map[string]any `json:"raw"`
}
type Movement struct {
	ID        string   `json:"id"`
	From      string   `json:"from"`
	To        string   `json:"to"`
	Asset     Asset    `json:"asset"`
	AmountRaw string   `json:"amount_raw"`
	Amount    string   `json:"amount"`
	Evidence  Evidence `json:"evidence"`
}
type Approval struct {
	Owner     string   `json:"owner"`
	Spender   string   `json:"spender"`
	Asset     Asset    `json:"asset"`
	AmountRaw string   `json:"amount_raw"`
	Amount    string   `json:"amount"`
	Operation string   `json:"operation"`
	Evidence  Evidence `json:"evidence"`
}
type Delta struct {
	Address string `json:"address"`
	Asset   Asset  `json:"asset"`
	InRaw   string `json:"in_raw"`
	OutRaw  string `json:"out_raw"`
	NetRaw  string `json:"net_raw"`
	In      string `json:"in"`
	Out     string `json:"out"`
	Net     string `json:"net"`
}
type Label struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}
type Participant struct {
	Address string   `json:"address"`
	Roles   []string `json:"roles"`
	Labels  []Label  `json:"labels"`
}
type Fee struct {
	Payer         string `json:"payer"`
	GasUsed       string `json:"gas_used"`
	GasLimit      string `json:"gas_limit"`
	UnitPriceRaw  string `json:"unit_price_raw"`
	UnitPriceGwei string `json:"unit_price_gwei"`
	AmountRaw     string `json:"amount_raw"`
	Amount        string `json:"amount"`
	Source        string `json:"source"`
	Scope         string `json:"scope"`
}
type Result struct {
	Chain            string         `json:"chain"`
	Hash             string         `json:"hash"`
	Outcome          string         `json:"outcome"`
	Behavior         string         `json:"behavior"`
	BehaviorEvidence string         `json:"behavior_evidence"`
	Native           Asset          `json:"native"`
	Transaction      map[string]any `json:"transaction"`
	Movements        []Movement     `json:"movements"`
	Approvals        []Approval     `json:"approvals"`
	Deltas           []Delta        `json:"deltas"`
	Participants     []Participant  `json:"participants"`
	Fee              Fee            `json:"fee"`
	Coverage         map[string]any `json:"coverage"`
	Issues           []string       `json:"issues"`
	BalanceSnapshots any            `json:"balance_snapshots"`
	Protocol         *Protocol      `json:"protocol,omitempty"`
}
type Input struct {
	Chain, Hash                   string
	Native                        Asset
	Transaction                   map[string]any
	Tokens, NFTs, Logs, Internals []map[string]any
	Trace                         *chaintrace.TraceResult
	Metadata                      map[string]Asset
	MethodName                    string
	MethodVerified                bool
	ReceiptPresent                bool
	Issues                        []string
}

func Text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return string(x)
	case nil:
		return ""
	case uint64:
		return strconv.FormatUint(x, 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint16:
		return strconv.FormatUint(uint64(x), 10)
	case uint8:
		return strconv.FormatUint(uint64(x), 10)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case *uint64:
		if x != nil {
			return strconv.FormatUint(*x, 10)
		}
	case *uint8:
		if x != nil {
			return strconv.FormatUint(uint64(*x), 10)
		}
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}
func Integer(v any) (*big.Int, bool) {
	s := strings.TrimSpace(Text(v))
	base := 10
	if strings.HasPrefix(s, "0x") {
		s = s[2:]
		base = 16
	}
	if s == "" || len(s) > 160 {
		return nil, false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9') && !(base == 16 && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F')) {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(s, base)
	return n, ok
}
func Decimal(raw string, d *int) string {
	if d == nil || *d < 0 || *d > 77 || raw == "" {
		return ""
	}
	n, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return ""
	}
	sign := ""
	if n.Sign() < 0 {
		sign = "-"
		n.Abs(n)
	}
	s := n.String()
	if *d == 0 {
		return sign + s
	}
	if len(s) <= *d {
		s = strings.Repeat("0", *d+1-len(s)) + s
	}
	fraction := strings.TrimRight(s[len(s)-*d:], "0")
	whole := s[:len(s)-*d]
	if fraction == "" {
		return sign + whole
	}
	return sign + whole + "." + fraction
}
func key(a Asset) string { return a.Kind + ":" + a.Contract + ":" + a.TokenID }
func event(sig string) string {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(sig))
	return "0x" + fmt.Sprintf("%x", h.Sum(nil))
}

var transfer = event("Transfer(address,address,uint256)")
var approval = event("Approval(address,address,uint256)")
var approvalAll = event("ApprovalForAll(address,address,bool)")
var single = event("TransferSingle(address,address,address,uint256,uint256)")
var batch = event("TransferBatch(address,address,address,uint256[],uint256[])")

func address(s string) string {
	s = strings.TrimPrefix(strings.ToLower(s), "0x")
	if len(s) != 64 || s[:24] != strings.Repeat("0", 24) {
		return ""
	}
	if _, ok := Integer("0x" + s); !ok {
		return ""
	}
	return "0x" + s[24:]
}
func words(s string) []string {
	s = strings.TrimPrefix(s, "0x")
	if len(s)%64 != 0 || len(s) > 131072 {
		return nil
	}
	out := []string{}
	for i := 0; i < len(s); i += 64 {
		if _, ok := Integer("0x" + s[i:i+64]); !ok {
			return nil
		}
		out = append(out, s[i:i+64])
	}
	return out
}
func topics(v any) []string {
	if a, ok := v.([]string); ok {
		return a
	}
	out := []string{}
	if a, ok := v.([]any); ok {
		for _, x := range a {
			out = append(out, Text(x))
		}
	}
	return out
}
func number(word string) string {
	n, ok := Integer("0x" + word)
	if !ok {
		return ""
	}
	return n.String()
}
func array(w []string, offset string) []string {
	o, ok := Integer("0x" + offset)
	if !ok || !o.IsInt64() || o.Int64()%32 != 0 {
		return nil
	}
	i := o.Int64() / 32
	if i < 2 || i >= int64(len(w)) {
		return nil
	}
	n, ok := Integer("0x" + w[i])
	if !ok || !n.IsInt64() || n.Int64() > 1000 || n.Int64() < 0 || i+1+n.Int64() > int64(len(w)) {
		return nil
	}
	return w[i+1 : i+1+n.Int64()]
}

func Build(in Input) Result {
	out := Result{Chain: in.Chain, Hash: in.Hash, Native: in.Native, Transaction: in.Transaction, Movements: []Movement{}, Approvals: []Approval{}, Deltas: []Delta{}, Participants: []Participant{}, Issues: append([]string{}, in.Issues...), Coverage: map[string]any{}, Outcome: "unknown", Behavior: "合约交互", BehaviorEvidence: "未解析的方法"}
	status := Text(in.Transaction["status"])
	if status == "1" || status == "0x1" {
		out.Outcome = "success"
	}
	if status == "0" || status == "0x0" {
		out.Outcome = "failed"
	}
	if Text(in.Transaction["block_number"]) == "" && in.Transaction["source_provider"] == "rpc" {
		out.Outcome = "pending"
	}
	normalize := func(s string) string {
		if in.Chain == "solana" || in.Chain == "tron" {
			return s
		}
		return strings.ToLower(s)
	}
	meta := func(c string) Asset {
		c = normalize(c)
		if a, ok := in.Metadata[c]; ok {
			return a
		}
		return Asset{Kind: "token", Contract: c}
	}
	seen := map[string]bool{}
	add := func(id, from, to, raw string, a Asset, e Evidence) {
		n, ok := Integer(raw)
		if !ok || n.Sign() <= 0 || from == "" || to == "" || seen[id] || out.Outcome == "failed" || out.Outcome == "pending" {
			return
		}
		seen[id] = true
		out.Movements = append(out.Movements, Movement{id, normalize(from), normalize(to), a, n.String(), Decimal(n.String(), a.Decimals), e})
	}
	if out.Outcome == "success" && in.Chain != "solana" {
		add("tx:value", Text(in.Transaction["from_address"]), Text(in.Transaction["to_address"]), Text(in.Transaction["value_raw"]), in.Native, Evidence{"transaction", "value", Text(in.Transaction["source_provider"]), in.Transaction})
	}
	// Receipt logs are canonical when available: an indexed row for the same log is
	// a fallback, never an additional transfer. NFT Transfer has four topics.
	covered := map[string]bool{}
	if out.Outcome != "failed" && out.Outcome != "pending" {
		for _, l := range in.Logs {
			if Text(l["removed"]) == "true" || Text(l["removed"]) == "1" {
				continue
			}
			ts := topics(l["topics"])
			if len(ts) == 0 {
				continue
			}
			w := words(Text(l["data"]))
			c := Text(l["address"])
			if c == "" {
				c = Text(l["contract_address"])
			}
			c = normalize(c)
			index := Text(l["log_index"])
			if index == "" {
				out.Issues = append(out.Issues, "日志下标缺失")
				continue
			}
			id := c + ":" + index
			e := Evidence{"log", index, Text(l["source_provider"]), l}
			a := meta(c)
			switch strings.ToLower(ts[0]) {
			case transfer:
				if len(ts) == 3 && len(w) == 1 {
					add("log:"+id, address(ts[1]), address(ts[2]), number(w[0]), a, e)
					covered[id] = true
				}
				if len(ts) == 4 && Text(l["data"]) == "0x" {
					a.Kind = "nft"
					a.TokenID = number(strings.TrimPrefix(ts[3], "0x"))
					d := 0
					a.Decimals = &d
					if a.TokenID != "" {
						add("log:"+id, address(ts[1]), address(ts[2]), "1", a, e)
						covered[id] = true
					}
				}
			case single, batch:
				if len(ts) != 4 {
					continue
				}
				a.Kind = "nft"
				d := 0
				a.Decimals = &d
				ids, amounts := []string{}, []string{}
				if ts[0] == single && len(w) == 2 {
					ids = w[:1]
					amounts = w[1:]
				}
				if ts[0] == batch && len(w) >= 2 {
					ids = array(w, w[0])
					amounts = array(w, w[1])
				}
				if len(ids) != len(amounts) || len(ids) == 0 {
					out.Issues = append(out.Issues, "ERC-1155 日志无法解析")
					continue
				}
				for i, v := range ids {
					a.TokenID = number(v)
					add(fmt.Sprintf("log:%s:%d", id, i), address(ts[2]), address(ts[3]), number(amounts[i]), a, e)
				}
				covered[id] = true
			case approval, approvalAll:
				raw := ""
				operation := "设置授权"
				if len(ts) == 3 && len(w) == 1 {
					raw = number(w[0])
					if raw == "0" {
						operation = "撤销授权"
					}
					if raw == new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).String() {
						operation = "无限额授权"
					}
				}
				if len(ts) == 4 && Text(l["data"]) == "0x" {
					a.Kind = "nft"
					a.TokenID = number(strings.TrimPrefix(ts[3], "0x"))
					operation = "NFT 授权"
					raw = "1"
					if address(ts[2]) == "0x0000000000000000000000000000000000000000" {
						operation = "撤销 NFT 授权"
						raw = "0"
					}
					d := 0
					a.Decimals = &d
				}
				if ts[0] == approvalAll && len(ts) == 3 && len(w) == 1 {
					if raw != "0" && raw != "1" {
						continue
					}
					a.Kind = "nft"
					d := 0
					a.Decimals = &d
					operation = "全部 NFT 授权"
					if raw == "0" {
						operation = "撤销全部 NFT 授权"
					}
				}
				if raw != "" && address(ts[1]) != "" && address(ts[2]) != "" {
					out.Approvals = append(out.Approvals, Approval{address(ts[1]), address(ts[2]), a, raw, Decimal(raw, a.Decimals), operation, e})
				}
			}
		}
	}
	for _, l := range in.Tokens {
		if in.ReceiptPresent && in.Chain != "solana" && in.Chain != "tron" {
			continue
		}
		c := normalize(Text(l["token_address"]))
		idx := Text(l["log_index"])
		if in.Chain == "solana" {
			idx = Text(l["instruction_index"]) + ":" + Text(l["inner_instruction_index"])
		}
		if covered[c+":"+idx] {
			continue
		}
		a := meta(c)
		if a.Decimals == nil && Text(l["token_decimals"]) != "" {
			d, err := strconv.Atoi(Text(l["token_decimals"]))
			if err == nil && d >= 0 && d <= 77 && Text(l["token_symbol"]) != "" {
				a.Decimals = &d
				a.Symbol = Text(l["token_symbol"])
				a.MetadataSource = "indexed_transfer"
			}
		}
		add("log:"+c+":"+idx, Text(l["from_address"]), Text(l["to_address"]), Text(l["amount_raw"]), a, Evidence{"token_transfer", idx, Text(l["source_provider"]), l})
	}
	for _, l := range in.NFTs {
		if in.ReceiptPresent && in.Chain != "solana" && in.Chain != "tron" {
			continue
		}
		c := normalize(Text(l["contract_address"]))
		idx := Text(l["log_index"])
		if covered[c+":"+idx] {
			continue
		}
		a := meta(c)
		a.Kind = "nft"
		a.TokenID = Text(l["token_id"])
		d := 0
		a.Decimals = &d
		raw := Text(l["amount_raw"])
		if raw == "" && strings.EqualFold(Text(l["token_standard"]), "erc721") {
			raw = "1"
		}
		add("nft:"+c+":"+idx+":"+a.TokenID, Text(l["from_address"]), Text(l["to_address"]), raw, a, Evidence{"nft_transfer", idx, Text(l["source_provider"]), l})
	}
	internalSource := "unavailable"
	if in.Trace != nil && in.Trace.Root != nil {
		internalSource = "cached_trace"
		var walk func(*chaintrace.CallFrame, string, bool, bool)
		walk = func(f *chaintrace.CallFrame, path string, root, ancestorFailed bool) {
			failed := ancestorFailed || f.Error != ""
			kind := strings.ToUpper(f.Type)
			to := ""
			if f.To != nil {
				to = string(*f.To)
			}
			if !root && !failed && (kind == "CALL" || kind == "CREATE" || kind == "CREATE2" || kind == "SELFDESTRUCT") {
				raw := map[string]any{"trace_path": path, "type": kind, "from": string(f.From), "to": to, "value": f.Value}
				add("trace:"+path, string(f.From), to, f.Value, in.Native, Evidence{"trace", path, in.Trace.Source, raw})
			}
			for i := range f.Children {
				walk(&f.Children[i], path+"."+strconv.Itoa(i), false, failed)
			}
		}
		walk(in.Trace.Root, "0", true, false)
	} else if len(in.Internals) > 0 {
		internalSource = "indexed_internal"
		failedPaths := []string{}
		for _, l := range in.Internals {
			if Text(l["success"]) == "0" || Text(l["error"]) != "" {
				failedPaths = append(failedPaths, Text(l["trace_path"]))
			}
		}
		for _, l := range in.Internals {
			p := Text(l["trace_path"])
			kind := strings.ToUpper(Text(l["call_type"]))
			blocked := Text(l["success"]) != "1" || Text(l["error"]) != ""
			for _, f := range failedPaths {
				if p == f || f == "" || strings.HasPrefix(p, f+".") {
					blocked = true
				}
			}
			internalValue, iok := Integer(l["value_raw"])
			outerValue, ook := Integer(in.Transaction["value_raw"])
			sameValue := iok && ook && internalValue.Cmp(outerValue) == 0
			root := in.Chain != "solana" && (p == "root" || (Text(l["depth"]) == "0" && normalize(Text(l["from_address"])) == normalize(Text(in.Transaction["from_address"])) && normalize(Text(l["to_address"])) == normalize(Text(in.Transaction["to_address"])) && sameValue))
			solTransfer := in.Chain == "solana" && (kind == "SYSTEM_TRANSFER" || kind == "SYSTEM_CREATE_ACCOUNT")
			if blocked || root || !(kind == "CALL" || kind == "CREATE" || kind == "CREATE2" || kind == "SELFDESTRUCT" || kind == "TRANSFER" || solTransfer) {
				continue
			}
			add("internal:"+Text(l["internal_index"]), Text(l["from_address"]), Text(l["to_address"]), Text(l["value_raw"]), in.Native, Evidence{"internal", p, Text(l["source_provider"]), l})
		}
	}
	parts := map[string]*Participant{}
	role := func(addr, r string) {
		addr = normalize(addr)
		if addr == "" {
			return
		}
		p := parts[addr]
		if p == nil {
			p = &Participant{Address: addr, Roles: []string{}, Labels: []Label{}}
			parts[addr] = p
		}
		for _, s := range p.Roles {
			if s == r {
				return
			}
		}
		p.Roles = append(p.Roles, r)
	}
	role(Text(in.Transaction["from_address"]), "交易发起人")
	role(Text(in.Transaction["from_address"]), "手续费支付方")
	role(Text(in.Transaction["to_address"]), "交易交互对象")
	type accumulator struct {
		address string
		asset   Asset
		in, out *big.Int
	}
	acc := map[string]*accumulator{}
	for _, m := range out.Movements {
		if m.From == "0x0000000000000000000000000000000000000000" {
			role(m.From, "资产铸造来源")
		} else {
			role(m.From, "实际转出方")
		}
		if m.To == "0x0000000000000000000000000000000000000000" {
			role(m.To, "资产销毁地址")
		} else {
			role(m.To, "实际接收方")
		}
		n, _ := new(big.Int).SetString(m.AmountRaw, 10)
		for i, a := range []string{m.From, m.To} {
			k := a + "|" + key(m.Asset)
			x := acc[k]
			if x == nil {
				x = &accumulator{a, m.Asset, new(big.Int), new(big.Int)}
				acc[k] = x
			}
			if i == 0 {
				x.out.Add(x.out, n)
			} else {
				x.in.Add(x.in, n)
			}
		}
	}
	for _, a := range out.Approvals {
		role(a.Owner, "授权方")
		role(a.Spender, "被授权方")
	}
	keys := []string{}
	for k := range parts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.Participants = append(out.Participants, *parts[k])
	}
	keys = nil
	for k := range acc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		x := acc[k]
		net := new(big.Int).Sub(x.in, x.out).String()
		out.Deltas = append(out.Deltas, Delta{x.address, x.asset, x.in.String(), x.out.String(), net, Decimal(x.in.String(), x.asset.Decimals), Decimal(x.out.String(), x.asset.Decimals), Decimal(net, x.asset.Decimals)})
	}
	out.Fee = Fee{Payer: Text(in.Transaction["from_address"]), GasLimit: Text(in.Transaction["gas"]), GasUsed: Text(in.Transaction["gas_used"]), Scope: "execution_gas"}
	price := Text(in.Transaction["effective_gas_price"])
	source := "receipt.effectiveGasPrice"
	if price == "" {
		typ := Text(in.Transaction["tx_type"])
		if typ == "0" || typ == "0x0" || typ == "legacy" {
			price = Text(in.Transaction["gas_price"])
			source = "legacy_transaction.gasPrice"
		}
	}
	used, uok := Integer(out.Fee.GasUsed)
	p, pok := Integer(price)
	if out.Outcome != "pending" && out.Outcome != "unknown" && uok && pok {
		out.Fee.UnitPriceRaw = p.String()
		d := 9
		out.Fee.UnitPriceGwei = Decimal(p.String(), &d)
		out.Fee.AmountRaw = new(big.Int).Mul(used, p).String()
		out.Fee.Amount = Decimal(out.Fee.AmountRaw, in.Native.Decimals)
		out.Fee.Source = source
	}
	if len(out.Movements) > 0 {
		out.Behavior = "资产转移"
		out.BehaviorEvidence = "逐笔资金流"
		if len(out.Movements) == 1 && out.Movements[0].Asset.Kind == "native" && Text(in.Transaction["input"]) == "0x" {
			out.Behavior = "原生币转账"
		}
		if in.MethodVerified {
			name := strings.ToLower(in.MethodName)
			for _, v := range []struct{ word, label string }{{"swap", "资产兑换"}, {"deposit", "资产存入"}, {"withdraw", "资产提取"}, {"liquidity", "流动性操作"}} {
				if strings.Contains(name, v.word) {
					out.Behavior = v.label
					out.BehaviorEvidence = "已验证合约方法 + 逐笔资金流"
					break
				}
			}
		}
	}
	if len(out.Approvals) > 0 && len(out.Movements) == 0 {
		out.Behavior = "资产授权"
		out.BehaviorEvidence = "Approval 事件"
	}
	if out.Outcome == "failed" {
		out.Behavior = "执行失败"
		out.BehaviorEvidence = "交易回执"
	}
	if out.Outcome == "pending" {
		out.Behavior = "待确认"
		out.BehaviorEvidence = "交易尚未入块"
	}
	out.Coverage = map[string]any{"receipt": in.ReceiptPresent, "internal_source": internalSource, "transfer_delta_scope": "observed_transaction_movements", "balance_snapshots": false, "historical_usd_price": false, "complete": in.ReceiptPresent && in.Trace != nil && in.Trace.Root != nil && in.Chain != "solana" && in.Chain != "tron" && len(out.Issues) == 0}
	identifyProtocol(&out)
	return out
}
