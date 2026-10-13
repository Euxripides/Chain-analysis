package crosschain

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Service) lifi(ctx context.Context, r QueryRequest) ([]Transfer, error) {
	headers := map[string]string{}
	if s.config.LifiKey != "" {
		headers["x-lifi-api-key"] = s.config.LifiKey
	}
	v, ev, e := s.fetch(ctx, "lifi", "https://li.quest", "/v1/status", url.Values{"txHash": {externalHash(r.Chain, r.TxHash)}}, nil, headers)
	if e != nil {
		return nil, e
	}
	status := str(get(v, "status"))
	if status == "NOT_FOUND" || status == "INVALID" {
		return nil, &upstreamError{"not_found", "LI.FI has no indexed record for this hash"}
	}
	if status == "" {
		return nil, fmt.Errorf("missing LI.FI status")
	}
	t := Transfer{OrderID: str(get(v, "transactionId")), RawStatus: status, Status: normalizeStatus(status), UnderlyingBridge: str(get(v, "tool")), Evidence: ev}
	if str(get(v, "substatus")) == "REFUNDED" {
		t.Status = "refunded"
	}
	if leg := lifiLeg(get(v, "sending"), str(get(v, "fromAddress"))); leg.TxHash != "" {
		t.Source = []Leg{leg}
	}
	if leg := lifiLeg(get(v, "receiving"), str(get(v, "toAddress"))); leg.TxHash != "" {
		if t.Status == "refunded" {
			t.Refund = []Leg{leg}
		} else {
			t.Destination = []Leg{leg}
		}
	}
	return []Transfer{t}, nil
}

func lifiLeg(v any, address string) Leg {
	c := chainKey(get(v, "chainId"))
	if c == "" {
		c = chainKey(get(v, "token", "chainId"))
	}
	leg := emptyLeg(c, str(get(v, "txHash")))
	leg.ChainID = str(get(v, "chainId"))
	leg.Address = address
	leg.TokenContract = str(get(v, "token", "address"))
	leg.Token = str(get(v, "token", "symbol"))
	leg.AmountRaw = str(get(v, "amount"))
	leg.Decimals = integerPointer(get(v, "token", "decimals"))
	leg.AmountsScope = "protocol_reported"
	if leg.Decimals != nil {
		leg.Amount = scaled(leg.AmountRaw, *leg.Decimals)
	}
	if seconds, e := strconv.ParseInt(str(get(v, "timestamp")), 10, 64); e == nil && seconds > 0 {
		leg.Timestamp = time.Unix(seconds, 0).UTC().Format(time.RFC3339)
	}
	return leg
}

func integerPointer(v any) *int {
	n, e := strconv.Atoi(str(v))
	if e != nil || n < 0 || n > 38 {
		return nil
	}
	return &n
}
func scaled(raw string, decimals int) string {
	n, e := Amount(raw)
	if e != nil || strings.Contains(raw, ".") || decimals < 0 || decimals > 38 {
		return ""
	}
	return n.Shift(int32(-decimals)).String()
}

func (s *Service) near(ctx context.Context, r QueryRequest) ([]Transfer, error) {
	if s.config.NearJWT == "" {
		return nil, &upstreamError{"not_configured", "CROSSCHAIN_NEAR_JWT is not configured"}
	}
	search := externalHash(r.Chain, r.TxHash)
	if r.DepositAddress != "" {
		search = r.DepositAddress
	}
	q := url.Values{"search": {search}, "numberOfTransactions": {strconv.Itoa(r.MaxResults + 1)}, "statuses": {"FAILED,INCOMPLETE_DEPOSIT,PENDING_DEPOSIT,PROCESSING,REFUNDED,SUCCESS"}}
	if r.DepositMemo != "" {
		q.Set("depositMemo", r.DepositMemo)
	}
	v, ev, e := s.fetch(ctx, "near_intents", "https://explorer.near-intents.org", "/api/v0/transactions", q, nil, map[string]string{"Authorization": "Bearer " + s.config.NearJWT})
	if e != nil {
		return nil, e
	}
	records := arr(v)
	if records == nil {
		return nil, fmt.Errorf("NEAR Explorer did not return an array")
	}
	if len(records) == 0 {
		return []Transfer{}, nil
	}
	ev.PageTruncated = len(records) > r.MaxResults
	// Exact asset metadata handles hashed NEP-141 IDs without guessing the source chain or mint.
	metadata := map[string]map[string]any{}
	if tokens, _, err := s.fetch(ctx, "near_tokens", "https://1click.chaindefuser.com", "/v0/tokens", nil, nil, nil); err == nil {
		for _, item := range arr(tokens) {
			metadata[str(get(item, "assetId"))] = obj(item)
		}
	}
	out := []Transfer{}
	for _, item := range records {
		t := Transfer{OrderID: str(get(item, "depositAddressAndMemo")), RawStatus: str(get(item, "status")), Evidence: ev}
		t.Status = normalizeStatus(t.RawStatus)
		if r.DepositAddress != "" {
			if str(get(item, "depositAddress")) != r.DepositAddress || r.DepositMemo != "" && str(get(item, "depositMemo")) != r.DepositMemo {
				continue
			}
			t.Evidence.MatchedIdentifiers = []string{"deposit_address"}
		}
		from := nearAsset(str(get(item, "originAsset")), metadata)
		to := nearAsset(str(get(item, "destinationAsset")), metadata)
		from.Address = ""
		senders := arr(get(item, "senders"))
		if len(senders) == 1 {
			from.Address = str(senders[0])
		}
		to.Address = str(get(item, "recipient"))
		from.AmountRaw = str(get(item, "amountIn"))
		from.Amount = str(get(item, "amountInFormatted"))
		to.AmountRaw = str(get(item, "amountOut"))
		to.Amount = str(get(item, "amountOutFormatted"))
		from.AmountsScope = "order_total"
		to.AmountsScope = "order_total"
		// An order total is not duplicated as a per-transaction amount in multi-leg orders.
		for _, h := range arr(get(item, "originChainTxHashes")) {
			leg := from
			leg.TxHash = emptyLeg(leg.Chain, hashValue(h)).TxHash
			if len(arr(get(item, "originChainTxHashes"))) > 1 {
				leg.AmountsScope = "order_total_not_per_transaction"
			}
			t.Source = append(t.Source, leg)
		}
		for _, h := range arr(get(item, "destinationChainTxHashes")) {
			leg := to
			if len(arr(get(item, "destinationChainTxHashes"))) > 1 {
				leg.AmountsScope = "order_total_not_per_transaction"
			}
			if t.Status == "refunded" {
				switch str(get(item, "refundType")) {
				case "ORIGIN_CHAIN":
					leg = from
				case "DESTINATION_CHAIN":
					leg = to
				default:
					leg = Leg{}
				}
				leg.Address = str(get(item, "refundTo"))
				leg.Amount = ""
				leg.AmountRaw = ""
				leg.AmountsScope = "refund_amount_unavailable"
				leg.TxHash = emptyLeg(leg.Chain, hashValue(h)).TxHash
				t.Refund = append(t.Refund, leg)
			} else {
				leg.TxHash = emptyLeg(leg.Chain, hashValue(h)).TxHash
				t.Destination = append(t.Destination, leg)
			}
		}
		out = append(out, t)
	}
	return out, nil
}

func nearAsset(id string, metadata map[string]map[string]any) Leg {
	m := metadata[id]
	return Leg{Chain: chainKey(m["blockchain"]), TokenContract: str(m["address"]), Token: str(m["symbol"]), Decimals: integerPointer(m["decimals"])}
}
func hashValue(v any) string {
	if h := str(v); h != "" {
		return h
	}
	return str(get(v, "hash"))
}

func externalHash(c, hash string) string {
	if c == "tron" {
		return strings.TrimPrefix(hash, "0x")
	}
	return hash
}

func (s *Service) bridgers(ctx context.Context, r QueryRequest) ([]Transfer, error) {
	v, ev, e := s.fetch(ctx, "bridgers", "https://api.bridgers.xyz", "/api/exchangeRecord/getTransDataById", nil, map[string]string{"orderId": r.OrderID}, nil)
	if e != nil {
		return nil, e
	}
	if str(get(v, "resCode")) != "100" {
		return nil, &upstreamError{"upstream_error", "BRIDGERS rejected the order query"}
	}
	d := get(v, "data")
	if str(get(d, "orderId")) == "" {
		return []Transfer{}, nil
	}
	t := Transfer{OrderID: str(get(d, "orderId")), RawStatus: str(get(d, "status")), Evidence: ev}
	t.Status = normalizeStatus(t.RawStatus)
	a := emptyLeg(chainKey(get(d, "fromChain")), str(get(d, "hash")))
	a.Address = str(get(d, "fromAddress"))
	a.TokenContract = str(get(d, "fromTokenAddress"))
	a.Token = str(get(d, "fromCoinCode"))
	a.Amount = str(get(d, "fromTokenAmount"))
	a.AmountsScope = "protocol_reported"
	a.Timestamp = str(get(d, "createTime"))
	b := emptyLeg(chainKey(get(d, "toChain")), str(get(d, "toHash")))
	b.Address = str(get(d, "toAddress"))
	b.TokenContract = str(get(d, "toTokenAddress"))
	b.Token = str(get(d, "toCoinCode"))
	b.Amount = str(get(d, "toTokenAmount"))
	b.AmountsScope = "quoted_expected"
	if a.TxHash != "" {
		t.Source = []Leg{a}
	}
	if b.TxHash != "" && t.Status != "refunded" {
		t.Destination = []Leg{b}
	}
	if h := str(get(d, "refundHash")); h != "" {
		refund := a
		refund.TxHash = h
		refund.Amount = str(get(d, "refundCoinAmt"))
		refund.AmountsScope = "protocol_reported_refund"
		t.Refund = []Leg{refund}
	}
	return []Transfer{t}, nil
}

func (s *Service) layerzero(ctx context.Context, r QueryRequest) ([]Transfer, error) {
	v, ev, e := s.fetch(ctx, "layerzero", "https://scan.layerzero-api.com", "/v1/messages/tx/"+url.PathEscape(r.TxHash), nil, nil, nil)
	if e != nil {
		return nil, e
	}
	items := arr(get(v, "data"))
	if items == nil {
		return nil, fmt.Errorf("LayerZero messages array missing")
	}
	out := []Transfer{}
	for _, item := range items {
		from := emptyLeg(chainKey(get(item, "pathway", "sender", "chain")), str(get(item, "source", "tx", "txHash")))
		to := emptyLeg(chainKey(get(item, "pathway", "receiver", "chain")), str(get(item, "destination", "tx", "txHash")))
		// Pathway sender/receiver are messaging contracts, not the token beneficiary.
		from.AmountsScope = "message_only"
		to.AmountsScope = "message_only"
		t := Transfer{OrderID: str(get(item, "guid")), RawStatus: str(get(item, "status", "name")), Source: []Leg{from}, Evidence: ev}
		t.Status = normalizeStatus(t.RawStatus)
		if strings.EqualFold(t.RawStatus, "FAILED") {
			t.Status = "failed"
		}
		if to.TxHash != "" {
			t.Destination = []Leg{to}
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Service) cbridge(ctx context.Context, r QueryRequest) ([]Transfer, error) {
	v, ev, e := s.fetch(ctx, "cbridge", "https://cbridge-prod2.celer.app", "/v2/getTransferStatus", nil, map[string]string{"transfer_id": strings.TrimPrefix(r.TransferID, "0x")}, nil)
	if e != nil {
		return nil, e
	}
	if get(v, "err") != nil {
		return nil, &upstreamError{"upstream_error", "cBridge rejected the transfer query"}
	}
	status := str(get(v, "status"))
	if status == "0" {
		return []Transfer{}, nil
	}
	if status == "" {
		return nil, fmt.Errorf("cBridge status missing")
	}
	t := Transfer{OrderID: r.TransferID, RawStatus: status, Status: "pending", Evidence: ev}
	switch status {
	case "5":
		t.Status = "completed"
	case "2":
		t.Status = "failed"
	case "10":
		t.Status = "refunded"
	}
	if leg := explorerLeg(str(get(v, "src_block_tx_link"))); leg.TxHash != "" {
		t.Source = []Leg{leg}
	}
	if leg := explorerLeg(str(get(v, "dst_block_tx_link"))); leg.TxHash != "" {
		if t.Status == "refunded" {
			t.Refund = []Leg{leg}
		} else {
			t.Destination = []Leg{leg}
		}
	}
	return []Transfer{t}, nil
}

func explorerLeg(link string) Leg {
	u, e := url.Parse(link)
	if e != nil {
		return Leg{}
	}
	hosts := map[string]string{"etherscan.io": "ethereum", "bscscan.com": "bsc", "polygonscan.com": "polygon", "arbiscan.io": "arbitrum", "optimistic.etherscan.io": "optimism", "basescan.org": "base", "snowtrace.io": "avalanche", "solscan.io": "solana", "tronscan.org": "tron"}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return Leg{}
	}
	return emptyLeg(hosts[u.Hostname()], parts[len(parts)-1])
}

func (s *Service) across(ctx context.Context, r QueryRequest) ([]Transfer, error) {
	if r.TxHash == "" {
		return nil, &upstreamError{"query_identifier_required", "Across requires the deposit transaction hash"}
	}
	headers := map[string]string{}
	if s.config.AcrossKey != "" {
		headers["Authorization"] = "Bearer " + s.config.AcrossKey
	}
	v, ev, e := s.fetch(ctx, "across", "https://across.to", "/api/deposit", url.Values{"depositTxnRef": {externalHash(r.Chain, r.TxHash)}}, nil, headers)
	if e != nil {
		return nil, e
	}
	d := get(v, "deposit")
	if d == nil {
		return nil, fmt.Errorf("Across deposit missing")
	}
	currentIndex, _ := strconv.Atoi(str(get(v, "pagination", "currentIndex")))
	maxIndex, _ := strconv.Atoi(str(get(v, "pagination", "maxIndex")))
	ev.PageTruncated = maxIndex > currentIndex
	deposits := []any{d}
	if ev.PageTruncated {
		// The singular API exposes one deposit. Enumerate a bounded page for a
		// multi-transfer transaction and retain only exact hash/chain matches.
		q := url.Values{"limit": {strconv.Itoa(r.MaxResults + 1)}, "skip": {"0"}}
		if sweep := str(get(d, "sweepTxnRef")); sweep != "" && Same(r.Chain, sweep, r.TxHash) {
			q.Set("sweepTxnRef", sweep)
		} else {
			q.Set("depositor", str(get(d, "depositor")))
			q.Set("originChainId", str(get(d, "originChainId")))
		}
		if page, pageEvidence, err := s.fetch(ctx, "across", "https://across.to", "/api/deposits", q, nil, headers); err == nil {
			for _, item := range arr(page) {
				a := emptyLeg(chainKey(get(item, "originChainId")), first(str(get(item, "depositTxnRef")), str(get(item, "depositTxHash"))))
				if a.Chain != r.Chain || !Same(r.Chain, a.TxHash, r.TxHash) || str(get(item, "depositId")) == str(get(d, "depositId")) {
					continue
				}
				deposits = append(deposits, item)
			}
			// The original pagination says more records may exist; a bounded page
			// does not assert complete historical coverage.
			pageEvidence.PageTruncated = true
			ev = pageEvidence
		}
	}
	out := []Transfer{}
	for _, d := range deposits {
		out = append(out, acrossDeposit(d, ev))
	}
	return out, nil
}

func acrossDeposit(d any, ev Evidence) Transfer {
	t := Transfer{OrderID: str(get(d, "depositId")), RawStatus: str(get(d, "status")), Evidence: ev}
	t.Status = normalizeStatus(t.RawStatus)
	a := emptyLeg(chainKey(get(d, "originChainId")), first(str(get(d, "depositTxnRef")), str(get(d, "depositTxHash"))))
	a.Address = str(get(d, "depositor"))
	a.TokenContract = str(get(d, "inputToken"))
	a.AmountRaw = str(get(d, "inputAmount"))
	a.Timestamp = str(get(d, "depositBlockTimestamp"))
	a.AmountsScope = "protocol_reported"
	b := emptyLeg(chainKey(get(d, "destinationChainId")), first(str(get(d, "fillTxnRef")), str(get(d, "fillTx"))))
	b.Address = str(get(d, "recipient"))
	b.TokenContract = str(get(d, "outputToken"))
	b.AmountRaw = str(get(d, "outputAmount"))
	b.Timestamp = str(get(d, "fillBlockTimestamp"))
	b.AmountsScope = "protocol_reported"
	t.Source = []Leg{a}
	if b.TxHash != "" {
		t.Destination = []Leg{b}
	}
	if h := first(str(get(d, "depositRefundTxnRef")), str(get(d, "depositRefundTxHash"))); h != "" {
		refund := a
		refund.TxHash = h
		refund.AmountRaw = ""
		refund.AmountsScope = "refund_amount_unavailable"
		t.Refund = []Leg{refund}
	}
	return t
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Service) relay(ctx context.Context, r QueryRequest) ([]Transfer, error) {
	if s.config.RelayKey == "" {
		return nil, &upstreamError{"not_configured", "CROSSCHAIN_RELAY_API_KEY is required for Requests v3"}
	}
	q := url.Values{"limit": {strconv.Itoa(r.MaxResults + 1)}}
	if r.TxHash != "" {
		q.Set("term", r.TxHash)
	} else {
		q.Set("orderId", r.OrderID)
	}
	v, ev, e := s.fetch(ctx, "relay", "https://api.relay.link", "/requests/v3", q, nil, map[string]string{"x-api-key": s.config.RelayKey})
	if e != nil {
		return nil, e
	}
	items := arr(get(v, "requests"))
	if items == nil {
		return nil, fmt.Errorf("Relay Requests v3 array missing")
	}
	ev.PageTruncated = str(get(v, "continuation")) != "" || len(items) > r.MaxResults
	out := []Transfer{}
	for _, d := range items {
		t := Transfer{OrderID: first(str(get(d, "protocol", "orderId")), str(get(d, "id"))), RawStatus: str(get(d, "status")), Evidence: ev}
		t.Status = normalizeStatus(t.RawStatus)
		origin := get(d, "protocol", "deposit", "origin")
		a := emptyLeg(chainKey(get(origin, "chainId")), str(get(origin, "transactionId")))
		a.Address = str(get(origin, "depositor"))
		a.TokenContract = str(get(origin, "currency"))
		a.AmountRaw = str(get(origin, "amount"))
		a.AmountsScope = "protocol_reported"
		if a.TxHash != "" {
			t.Source = []Leg{a}
		}
		for _, fill := range arr(get(d, "protocol", "settlement", "destination", "fills")) {
			leg := emptyLeg(chainKey(get(fill, "chainId")), str(get(fill, "transactionId")))
			leg.AmountsScope = "amount_unavailable"
			t.Destination = append(t.Destination, leg)
		}
		for _, refund := range arr(get(d, "protocol", "settlement", "destination", "refunds")) {
			leg := emptyLeg(chainKey(get(refund, "chainId")), str(get(refund, "transactionId")))
			leg.AmountsScope = "refund_amount_unavailable"
			t.Refund = append(t.Refund, leg)
		}
		out = append(out, t)
	}
	return out, nil
}
