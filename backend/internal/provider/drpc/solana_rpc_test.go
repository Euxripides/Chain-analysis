package drpc

// 本文件覆盖 Solana JSON-RPC 通道：logs（getSignaturesForAddress + getTransaction）、
// balances（getSlot + getBalance + getTokenAccountsByOwner）、版本化交易错误与探针。
import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// ---- RPC 测试脚手架 ----

type rpcCall struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

func writeRPCResult(t *testing.T, w http.ResponseWriter, result json.RawMessage) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":`))
	_, _ = w.Write(result)
	_, _ = w.Write([]byte("}"))
}

func writeRPCError(t *testing.T, w http.ResponseWriter, code int, message string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","error":{"code":` + strconv.Itoa(code) + `,"message":"` + message + `"}}`))
}

func decodeRPCCall(t *testing.T, r *http.Request) rpcCall {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("read rpc body: %v", err)
	}
	var call rpcCall
	if err := json.Unmarshal(body, &call); err != nil {
		t.Errorf("decode rpc body: %v (%s)", err, body)
	}
	return call
}

func txResultFixture(slot uint64, blockTime int64, failed bool, logs []string) map[string]any {
	var errValue any
	if failed {
		errValue = map[string]any{"InstructionError": []any{0, "Custom"}}
	}
	return map[string]any{
		"slot":      slot,
		"blockTime": blockTime,
		"transaction": map[string]any{
			"signatures": []string{"sig"},
			"message":    map[string]any{"recentBlockhash": "hash", "accountKeys": []any{}},
		},
		"meta": map[string]any{"err": errValue, "fee": 5000, "logMessages": logs},
	}
}

func tokenAccountFixture(mint, amount string, decimals int) map[string]any {
	return map[string]any{
		"pubkey": "ata-" + mint,
		"account": map[string]any{
			"owner": testAddress,
			"data": map[string]any{"parsed": map[string]any{"info": map[string]any{
				"mint": mint, "owner": testAddress,
				"tokenAmount": map[string]any{"amount": amount, "decimals": decimals},
			}}},
		},
	}
}

// ---- logs ----

func TestRPCLogsPageContract(t *testing.T) {
	const (
		sigA = "SigA111111111111111111111111111111111111111111111111111"
		sigB = "SigB111111111111111111111111111111111111111111111111111"
	)
	systemProgram := "11111111111111111111111111111111"
	var mu sync.Mutex
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := decodeRPCCall(t, r)
		mu.Lock()
		methods = append(methods, call.Method)
		mu.Unlock()
		switch call.Method {
		case MethodSignatures:
			var address string
			_ = json.Unmarshal(call.Params[0], &address)
			if address != testAddress {
				t.Errorf("getSignaturesForAddress 的地址必须原样传递，得到 %s", address)
			}
			writeRPCResult(t, w, mustJSON(t, []map[string]any{
				{"signature": sigA, "slot": 100, "blockTime": 1700000000, "err": nil},
				{"signature": sigB, "slot": 99, "blockTime": 1699999000, "err": nil},
			}))
		case MethodTransaction:
			var signature string
			_ = json.Unmarshal(call.Params[0], &signature)
			logs := []string{
				"Program " + systemProgram + " invoke [1]",
				"Program log: Instruction: Transfer",
				"Program " + systemProgram + " success",
				"Program " + ProgramSPLToken + " invoke [1]",
				"Program log: outer",
				"Program " + ProgramSPLToken + " invoke [2]",
				"Program data: aGVsbG8=",
				"Program " + ProgramSPLToken + " success",
				"Program " + ProgramSPLToken + " success",
			}
			slot := uint64(100)
			if signature == sigB {
				slot = 99
			}
			writeRPCResult(t, w, mustJSON(t, txResultFixture(slot, 1700000000, false, logs)))
		default:
			t.Errorf("未预期的方法 %s", call.Method)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	result, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetLogs, Address: testAddress, PageSize: 2,
	})
	if err != nil {
		t.Fatalf("logs 取数失败: %v", err)
	}
	if len(result.Rows) != 6 {
		t.Fatalf("两笔交易各 3 条日志，得到 %d 行", len(result.Rows))
	}
	first := rowObject(t, result.Rows[0])
	// §3 logs 契约逐字段。
	if first["transactionHash"] != sigA {
		t.Fatalf("transactionHash 不符: %v", first["transactionHash"])
	}
	if first["blockNumber"] != "100" {
		t.Fatalf("blockNumber 应为 slot 字符串: %v", first["blockNumber"])
	}
	if first["timestamp"] != float64(1700000000) {
		t.Fatalf("timestamp 应为区块时间（秒）: %v", first["timestamp"])
	}
	if first["logIndex"] != float64(1) {
		t.Fatalf("logIndex 应从 1 开始: %v", first["logIndex"])
	}
	if first["address"] != systemProgram {
		t.Fatalf("address 应为当前程序 id: %v", first["address"])
	}
	if first["data"] != "Instruction: Transfer" {
		t.Fatalf("data 应去掉 \"Program log: \" 前缀: %v", first["data"])
	}
	if first["eventSource"] != "solana_program_log" {
		t.Fatalf("eventSource 不符: %v", first["eventSource"])
	}
	if first["programId"] != "" {
		t.Fatalf("programId 按契约留空: %v", first["programId"])
	}
	if first["invokeDepth"] != float64(1) {
		t.Fatalf("invokeDepth 应为 1: %v", first["invokeDepth"])
	}
	if first["success"] != true || first["removed"] != false {
		t.Fatalf("success/removed 不符: %v %v", first["success"], first["removed"])
	}
	if first["sourceApi"] != ChannelSolanaRPC {
		t.Fatalf("sourceApi 应为 solana_rpc: %v", first["sourceApi"])
	}
	if requestID, _ := first["sourceRequestId"].(string); requestID == "" {
		t.Fatalf("sourceRequestId 必须非空")
	}
	// 内层 CPI 的 depth=2 与 Program data 来源。
	third := rowObject(t, result.Rows[2])
	if third["invokeDepth"] != float64(2) || third["eventSource"] != "solana_program_data" {
		t.Fatalf("内层日志不符: %#v", third)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 3 || methods[0] != MethodSignatures {
		t.Fatalf("应先 getSignaturesForAddress 再逐签名 getTransaction: %#v", methods)
	}
}

func TestRPCLogsFailsOnNullTransaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := decodeRPCCall(t, r)
		if call.Method == MethodSignatures {
			writeRPCResult(t, w, mustJSON(t, []map[string]any{{"signature": "sig-null", "slot": 10}}))
			return
		}
		writeRPCResult(t, w, json.RawMessage("null"))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetLogs, Address: testAddress,
	})
	// result=null 不能写成成功空行：必须显式失败（§7.4）。
	perr := requireCode(t, err, CodeInvalidResponse)
	if perr.Class != provider.ClassUpstream {
		t.Fatalf("null 交易应归 upstream，得到 %s", perr.Class)
	}
	if !strings.Contains(err.Error(), "sig-null") {
		t.Fatalf("错误信息应包含签名以便排查: %v", err)
	}
}

func TestRPCTransactionVersionUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := decodeRPCCall(t, r)
		if call.Method == MethodSignatures {
			writeRPCResult(t, w, mustJSON(t, []map[string]any{{"signature": "sig-v0", "slot": 10}}))
			return
		}
		writeRPCError(t, w, -32015, "Transaction version (0) is not supported")
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetLogs, Address: testAddress,
	})
	perr := requireCode(t, err, CodeTxVersionUnsupported)
	if perr.Class != provider.ClassUnsupported || perr.Retryable {
		t.Fatalf("版本不支持必须不可重试且归 unsupported: %#v", perr)
	}
	if perr.RPCCode != -32015 {
		t.Fatalf("必须保留 JSON-RPC code，得到 %d", perr.RPCCode)
	}
}

func TestRPCInvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(jsonHandler(http.StatusOK, "<html>bad gateway</html>"))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetLogs, Address: testAddress,
	})
	perr := requireCode(t, err, CodeInvalidResponse)
	if perr.Class != provider.ClassInvalidJSON {
		t.Fatalf("非法 JSON 必须归 invalid_json，得到 %s", perr.Class)
	}
}

func TestRPCUnknownErrorCodeStaysUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := decodeRPCCall(t, r)
		if call.Method == MethodSignatures {
			writeRPCResult(t, w, mustJSON(t, []map[string]any{{"signature": "sig-x", "slot": 10}}))
			return
		}
		writeRPCError(t, w, -32099, "something new")
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetLogs, Address: testAddress,
	})
	perr := requireCode(t, err, CodeInvalidResponse)
	if perr.Class != provider.ClassUpstream || !perr.Retryable {
		t.Fatalf("未知 code 应归 upstream 且可重试: %#v", perr)
	}
	if perr.RPCCode != -32099 || !strings.Contains(perr.Message, "something new") {
		t.Fatalf("必须保留 code 与脱敏 message: %#v", perr)
	}
}

func TestRPCLogsSlotRangeTrimsAndPaginates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := decodeRPCCall(t, r)
		switch call.Method {
		case MethodSignatures:
			writeRPCResult(t, w, mustJSON(t, []map[string]any{
				{"signature": "sig-newest", "slot": 300},
				{"signature": "sig-mid", "slot": 150},
				{"signature": "sig-old", "slot": 50},
			}))
		case MethodTransaction:
			writeRPCResult(t, w, mustJSON(t, txResultFixture(150, 1700000000, false, []string{
				"Program " + ProgramSPLToken + " invoke [1]", "Program log: hello", "Program " + ProgramSPLToken + " success",
			})))
		default:
			t.Errorf("未预期方法 %s", call.Method)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	from, to := uint64(100), uint64(200)
	result, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetLogs, Address: testAddress,
		FromBlock: &from, ToBlock: &to, PageSize: 3,
	})
	if err != nil {
		t.Fatalf("logs 取数失败: %v", err)
	}
	// 只有 slot=150 落在区间内；看到比下界更旧的 slot 即停止翻页（区间已到下界）。
	if len(result.Rows) != 1 {
		t.Fatalf("区间过滤后应只剩 1 条日志，得到 %d", len(result.Rows))
	}
	if result.HasMore || result.NextPageToken != "" {
		t.Fatalf("到达区间下界后不应再翻页: token=%q hasMore=%v", result.NextPageToken, result.HasMore)
	}

	// 不设下界时，页满则用最旧签名继续翻页。
	result, err = env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetLogs, Address: testAddress,
		ToBlock: &to, PageSize: 3,
	})
	if err != nil {
		t.Fatalf("logs 取数失败: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("上界 200 内应剩 2 条日志，得到 %d", len(result.Rows))
	}
	if !result.HasMore || result.NextPageToken != "sig-old" {
		t.Fatalf("页满时应用最旧签名继续翻页: token=%q hasMore=%v", result.NextPageToken, result.HasMore)
	}
}

// ---- balances ----

func TestRPCBalancesSnapshot(t *testing.T) {
	const (
		mintA = "MintA1111111111111111111111111111111111111111111111111"
		mintB = "MintB1111111111111111111111111111111111111111111111111"
	)
	var methods []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := decodeRPCCall(t, r)
		mu.Lock()
		methods = append(methods, call.Method)
		mu.Unlock()
		switch call.Method {
		case MethodSlot:
			writeRPCResult(t, w, json.RawMessage("12345"))
		case MethodBalance:
			writeRPCResult(t, w, mustJSON(t, map[string]any{"context": map[string]any{"slot": 12345}, "value": 42}))
		case MethodTokenAccounts:
			var program map[string]any
			_ = json.Unmarshal(call.Params[1], &program)
			if program["programId"] == ProgramSPLToken {
				writeRPCResult(t, w, mustJSON(t, map[string]any{
					"context": map[string]any{"slot": 12345},
					"value": []any{
						tokenAccountFixture(mintA, "10", 6),
						tokenAccountFixture(mintA, "5", 6), // 同一 mint 的第二个账户：必须求和
						tokenAccountFixture(mintB, "7", 9),
					},
				}))
				return
			}
			writeRPCResult(t, w, mustJSON(t, map[string]any{"context": map[string]any{"slot": 12345}, "value": []any{}}))
		default:
			t.Errorf("未预期方法 %s", call.Method)
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	result, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetBalances, Address: testAddress,
	})
	if err != nil {
		t.Fatalf("balances 取数失败: %v", err)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("原生 + 两个 mint 共 3 行，得到 %d", len(result.Rows))
	}
	native := rowObject(t, result.Rows[0])
	if native["holderAddress"] != testAddress || native["contractAddress"] != "" ||
		native["tokenType"] != "native" || native["tokenDecimals"] != float64(9) ||
		native["balanceRawInteger"] != "42" || native["snapshotBlock"] != "12345" {
		t.Fatalf("原生余额行不符 §3 契约: %#v", native)
	}
	first := rowObject(t, result.Rows[1])
	if first["contractAddress"] != mintA || first["tokenType"] != "spl" ||
		first["tokenDecimals"] != float64(6) || first["balanceRawInteger"] != "15" {
		t.Fatalf("SPL 余额应按 mint 求和: %#v", first)
	}
	second := rowObject(t, result.Rows[2])
	if second["contractAddress"] != mintB || second["tokenType"] != "spl" {
		t.Fatalf("SPL 余额行不符: %#v", second)
	}
	if result.HasMore || result.NextPageToken != "" {
		t.Fatalf("余额快照是单页: %#v", result)
	}
}

func TestRPCBalancesRejectsOversizedSnapshot(t *testing.T) {
	// 通过把上限调小来验证"超限显式失败、绝不截断"。
	restore := maxBalanceAccounts
	maxBalanceAccounts = 1
	defer func() { maxBalanceAccounts = restore }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := decodeRPCCall(t, r)
		switch call.Method {
		case MethodSlot:
			writeRPCResult(t, w, json.RawMessage("7"))
		case MethodBalance:
			writeRPCResult(t, w, mustJSON(t, map[string]any{"context": map[string]any{"slot": 7}, "value": 1}))
		default:
			// 一个代币账户即可让总行数超过注入的上限。
			writeRPCResult(t, w, mustJSON(t, map[string]any{"context": map[string]any{"slot": 7}, "value": []any{
				tokenAccountFixture("MintCap111111111111111111111111111111111111111111111111", "3", 6),
			}}))
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetBalances, Address: testAddress,
	})
	perr := requireCode(t, err, CodeInvalidResponse)
	if perr.Class != provider.ClassResponseTooLarge {
		t.Fatalf("超限必须归 response_too_large，得到 %s", perr.Class)
	}
}

// ---- 探针 ----

func TestRPCProbeUsesGetHealthWithZeroCU(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(t, w, pageFixture(""))
			return
		}
		call := decodeRPCCall(t, r)
		mu.Lock()
		methods = append(methods, call.Method)
		mu.Unlock()
		writeRPCResult(t, w, json.RawMessage(`"ok"`))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	probe := env.client.ProbeChannels(context.Background())[ChannelSolanaRPC].(channelProbe)
	if !probe.OK || probe.ErrorClass != "" {
		t.Fatalf("getHealth 成功时探针应为 ok: %#v", probe)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 1 || methods[0] != MethodHealth {
		t.Fatalf("RPC 探针必须用 getHealth: %#v", methods)
	}
	for _, row := range env.usage.all() {
		if row.Method == MethodHealth {
			if row.Credits != 0 {
				t.Fatalf("getHealth 应为 0 CU: %v", row.Credits)
			}
			if row.UsageType != UsageProviderTest || row.APIType != APITypeRPC {
				t.Fatalf("探针用量行不符: %#v", row)
			}
		}
	}
}

func TestRPCProbeUnhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeRPCResult(t, w, json.RawMessage(`"behind"`))
			return
		}
		writeJSON(t, w, pageFixture(""))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	probe := env.client.ProbeChannels(context.Background())[ChannelSolanaRPC].(channelProbe)
	if probe.OK || probe.State != string(provider.HealthDegraded) {
		t.Fatalf("非 ok 的 getHealth 必须报 DEGRADED: %#v", probe)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return encoded
}
