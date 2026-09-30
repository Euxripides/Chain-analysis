package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/secret"
)

const defaultDRPCSolanaRPCEndpoint = "https://lb.drpc.live/solana/{key}"

type solanaEndpoint struct {
	url    string
	source string
}

// These read-only endpoints deliberately do not claim that Solana is supported
// by the EVM/TRON downloader, ClickHouse analysis or coverage pipeline.
var solanaReadSlots = make(chan struct{}, 4)

type solanaReadError struct {
	status int
	code   string
	text   string
}

func (e *solanaReadError) Error() string { return e.text }

func solanaReadFailure(w http.ResponseWriter, err error) {
	var typed *solanaReadError
	if errors.As(err, &typed) {
		actionFail(w, typed.status, typed.code, typed.text)
		return
	}
	actionFail(w, http.StatusBadGateway, "SOLANA_RPC_ERROR", "Solana RPC could not return a readable record")
}

func validSolanaBase58(value string, size int) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	n := new(big.Int)
	for i := 0; i < len(value); i++ {
		digit := strings.IndexByte(alphabet, value[i])
		if digit < 0 {
			return false
		}
		n.Mul(n, big.NewInt(58))
		n.Add(n, big.NewInt(int64(digit)))
	}
	leading := 0
	for leading < len(value) && value[leading] == '1' {
		leading++
	}
	return leading+len(n.Bytes()) == size
}

func validateSolanaRPCURL(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("invalid Solana RPC URL")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return errors.New("Solana RPC URL must use HTTPS")
		}
	}
	return nil
}

func validateDRPCSolanaRPCURL(endpoint string) error {
	if err := validateSolanaRPCURL(endpoint); err != nil {
		return err
	}
	u, _ := url.Parse(endpoint)
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if host == "localhost" || (ip != nil && ip.IsLoopback()) || host == "drpc.live" || strings.HasSuffix(host, ".drpc.live") || host == "drpc.org" || strings.HasSuffix(host, ".drpc.org") {
		return nil
	}
	return errors.New("dRPC Solana RPC host is not allowed")
}

func solanaRPCEndpoints() ([]solanaEndpoint, error) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("DRPC_ENABLED")), "false") || (os.Getenv("SOLANA_PROVIDER") != "" && !strings.EqualFold(strings.TrimSpace(os.Getenv("SOLANA_PROVIDER")), "drpc")) {
		return nil, errors.New("dRPC Solana provider is disabled")
	}
	key := strings.TrimSpace(os.Getenv("DRPC_API_KEY"))
	if key == "" {
		ref := strings.TrimSpace(os.Getenv("DRPC_RUNTIME_KEY_REF"))
		if strings.HasPrefix(ref, "env:") && !strings.HasPrefix(ref, "env://") {
			key = strings.TrimSpace(os.Getenv(strings.TrimPrefix(ref, "env:")))
		} else if ref != "" {
			manager := secret.NewManager(secret.ManagerOptions{FilePath: os.Getenv("PROVIDER_SECRETS_FILE")})
			resolved, err := manager.Resolve(context.Background(), ref)
			if err == nil {
				key = strings.TrimSpace(resolved)
			}
		}
	}
	template := strings.TrimSpace(os.Getenv("DRPC_SOLANA_RPC_ENDPOINT"))
	if template == "" {
		base := strings.TrimRight(strings.TrimSpace(os.Getenv("DRPC_BASE_URL")), "/")
		if base == "" {
			template = defaultDRPCSolanaRPCEndpoint
		} else if strings.HasSuffix(base, "/solana") {
			template = base + "/{key}"
		} else {
			template = base + "/solana/{key}"
		}
	}
	if key == "" && strings.Contains(template, "{key}") {
		return nil, errors.New("dRPC runtime key is unavailable")
	}
	primary := strings.ReplaceAll(template, "{key}", url.PathEscape(key))
	if err := validateDRPCSolanaRPCURL(primary); err != nil {
		return nil, err
	}
	endpoints := []solanaEndpoint{{primary, "drpc_solana_rpc"}}
	if archive := strings.TrimSpace(os.Getenv("DRPC_SOLANA_ARCHIVE_RPC_ENDPOINT")); archive != "" {
		if key == "" && strings.Contains(archive, "{key}") {
			return nil, errors.New("dRPC runtime key is unavailable")
		}
		archive = strings.ReplaceAll(archive, "{key}", url.PathEscape(key))
		if err := validateDRPCSolanaRPCURL(archive); err != nil {
			return nil, err
		}
		if archive != primary {
			endpoints = append(endpoints, solanaEndpoint{archive, "drpc_solana_archive_rpc"})
		}
	}
	return endpoints, nil
}

func solanaRPCCall(ctx context.Context, methodName string, params []any) (json.RawMessage, string, error) {
	endpoints, err := solanaRPCEndpoints()
	if err != nil {
		return nil, "", &solanaReadError{http.StatusServiceUnavailable, "DRPC_SOLANA_NOT_CONFIGURED", "dRPC Solana RPC or its runtime credential is unavailable"}
	}
	var lastErr error
	for index, endpoint := range endpoints {
		result, err := solanaRPCOnce(ctx, endpoint.url, methodName, params)
		if err == nil && string(result) != "null" && !(methodName == "getSignaturesForAddress" && string(result) == "[]" && index+1 < len(endpoints)) {
			return result, endpoint.source, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = nil
		}
	}
	if lastErr != nil {
		return nil, "", lastErr
	}
	return json.RawMessage("null"), endpoints[len(endpoints)-1].source, nil
}

func solanaRPCOnce(ctx context.Context, endpoint, methodName string, params []any) (json.RawMessage, error) {
	select {
	case solanaReadSlots <- struct{}{}:
		defer func() { <-solanaReadSlots }()
	case <-ctx.Done():
		return nil, &solanaReadError{http.StatusServiceUnavailable, "SOLANA_RPC_TIMEOUT", "Solana RPC read timed out"}
	}
	requestBody, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": methodName, "params": params})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	request.Header.Set("content-type", "application/json")
	timeoutSeconds := 30
	if configured, err := strconv.Atoi(strings.TrimSpace(os.Getenv("DRPC_SOLANA_RPC_REQUEST_TIMEOUT_SECONDS"))); err == nil && configured >= 1 && configured <= 60 {
		timeoutSeconds = configured
	}
	client := &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, &solanaReadError{http.StatusBadGateway, "SOLANA_RPC_UNAVAILABLE", "Solana RPC request failed or timed out"}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, &solanaReadError{http.StatusServiceUnavailable, "SOLANA_RPC_RATE_LIMITED", "dRPC Solana RPC rate limit reached; retry later"}
	}
	if response.StatusCode != http.StatusOK {
		return nil, &solanaReadError{http.StatusBadGateway, "SOLANA_RPC_UPSTREAM_ERROR", "Solana RPC returned a non-success status"}
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&envelope); err != nil || envelope.Error != nil || len(envelope.Result) == 0 {
		return nil, &solanaReadError{http.StatusBadGateway, "SOLANA_RPC_INVALID_RESPONSE", "Solana RPC did not return a readable result"}
	}
	return envelope.Result, nil
}

func solanaEvidenceMeta(source string) map[string]any {
	return map[string]any{
		"source": source, "cached": false,
		"dataset_version": "solana-rpc-live-v1", "normalization_version": "none",
	}
}

func solanaTransaction(ctx context.Context, signature string) (map[string]any, error) {
	if !validSolanaBase58(signature, 64) {
		return nil, &solanaReadError{http.StatusBadRequest, "INVALID_SIGNATURE", "A complete 64-byte Solana Base58 transaction signature is required"}
	}
	maxTxVersion := 1
	if configured, err := strconv.Atoi(strings.TrimSpace(os.Getenv("DRPC_SOLANA_MAX_TX_VERSION"))); err == nil && configured > maxTxVersion {
		maxTxVersion = configured
	}
	raw, source, err := solanaRPCCall(ctx, "getTransaction", []any{signature, map[string]any{"encoding": "jsonParsed", "commitment": "finalized", "maxSupportedTransactionVersion": maxTxVersion}})
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, &solanaReadError{http.StatusNotFound, "SOURCE_RECORD_UNAVAILABLE", "The configured Solana RPC has no readable transaction for this signature; this does not establish that the transaction never existed"}
	}
	var details map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&details); err != nil || details == nil || details["transaction"] == nil {
		return nil, &solanaReadError{http.StatusBadGateway, "SOLANA_RPC_INVALID_RESPONSE", "Solana RPC returned an incomplete transaction record"}
	}
	return map[string]any{"chain": "solana", "signature": signature, "slot": details["slot"], "block_time": details["blockTime"], "version": details["version"], "transaction": details["transaction"], "meta": details["meta"], "source_record_status": "readable", "rpc_source": source}, nil
}

func (s *Server) handleActionSolanaTransaction(w http.ResponseWriter, r *http.Request) {
	signature := strings.TrimSpace(r.URL.Query().Get("signature"))
	data, err := solanaTransaction(r.Context(), signature)
	if err != nil {
		solanaReadFailure(w, err)
		return
	}
	actionOK(w, data, solanaEvidenceMeta(data["rpc_source"].(string)))
}

func (s *Server) handleActionSolanaSignatures(w http.ResponseWriter, r *http.Request) {
	address := strings.TrimSpace(r.URL.Query().Get("address"))
	if !validSolanaBase58(address, 32) {
		actionFail(w, http.StatusBadRequest, "INVALID_ADDRESS", "A complete 32-byte Solana Base58 address is required")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			actionFail(w, http.StatusBadRequest, "INVALID_LIMIT", "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	before := strings.TrimSpace(r.URL.Query().Get("before"))
	if before != "" && !validSolanaBase58(before, 64) {
		actionFail(w, http.StatusBadRequest, "INVALID_SIGNATURE", "before must be a complete Solana transaction signature")
		return
	}
	options := map[string]any{"limit": limit, "commitment": "finalized"}
	if before != "" {
		options["before"] = before
	}
	raw, source, err := solanaRPCCall(r.Context(), "getSignaturesForAddress", []any{address, options})
	if err != nil {
		solanaReadFailure(w, err)
		return
	}
	var signatures []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&signatures); err != nil || signatures == nil {
		actionFail(w, http.StatusBadGateway, "SOLANA_RPC_INVALID_RESPONSE", "Solana RPC did not return a readable signature list")
		return
	}
	var nextBefore any
	if len(signatures) == limit {
		if last, ok := signatures[len(signatures)-1]["signature"].(string); ok {
			nextBefore = last
		}
	}
	meta := solanaEvidenceMeta(source)
	meta["pagination"] = map[string]any{"returned": len(signatures), "limit": limit, "next_before": nextBefore, "has_more": nextBefore != nil}
	actionOK(w, map[string]any{"chain": "solana", "address": address, "signatures": signatures}, meta)
}

func (s *Server) handleActionBridgeSourceTransaction(w http.ResponseWriter, r *http.Request) {
	sourceChain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("source_chain")))
	hash := strings.TrimSpace(r.URL.Query().Get("source_tx_hash"))
	orderID := strings.TrimSpace(r.URL.Query().Get("bridge_order_id"))
	if len(orderID) > 256 {
		actionFail(w, http.StatusBadRequest, "INVALID_ORDER_ID", "bridge_order_id is too long")
		return
	}
	if hash == "" {
		actionFail(w, http.StatusBadRequest, "SOURCE_TX_REQUIRED", "The source transaction hash is required; an order ID alone cannot identify a verified source-chain record")
		return
	}
	var record map[string]any
	var err error
	meta := map[string]any{}
	sourceRecordStatus := "readable"
	if sourceChain == "solana" {
		record, err = solanaTransaction(r.Context(), hash)
		if err == nil {
			meta = solanaEvidenceMeta(record["rpc_source"].(string))
		}
	} else {
		canonical, chainErr := canonicalActionChain(sourceChain)
		if chainErr != nil {
			actionFail(w, http.StatusBadRequest, "INVALID_CHAIN", "Unsupported source chain")
			return
		}
		parsed, hashErr := chain.ParseHash(hash)
		if hashErr != nil {
			actionFail(w, http.StatusBadRequest, "INVALID_TX_HASH", "A complete source-chain transaction hash is required")
			return
		}
		sourceChain, hash = publicActionChain(canonical), parsed.String()
		var detailMeta map[string]any
		record, detailMeta, err = s.transactionActionData(r.Context(), canonical, hash)
		if detailMeta["transaction_row_present"] == false {
			sourceRecordStatus = "partial"
			meta["source_record_scope"] = detailMeta
		}
		if errors.Is(err, method.ErrTransactionNotFound) {
			actionFail(w, http.StatusNotFound, "SOURCE_RECORD_UNAVAILABLE", "No readable source-chain transaction is available from the platform")
			return
		}
	}
	if err != nil {
		if sourceChain == "solana" {
			solanaReadFailure(w, err)
		} else {
			actionFail(w, http.StatusBadGateway, "SOURCE_RECORD_UNAVAILABLE", "The platform could not read the source-chain transaction")
		}
		return
	}
	actionOK(w, map[string]any{
		"bridge_order_id": orderID, "source_chain": sourceChain, "source_tx_hash": hash,
		"source_record_status": sourceRecordStatus, "source_record": record,
		"bridge_order_association_verified": false,
		"association_reason":                "The source transaction was read by hash; bridge order matching requires independent protocol/order evidence",
	}, meta)
}
