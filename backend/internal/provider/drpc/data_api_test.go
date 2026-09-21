package drpc

// 本文件覆盖 Data API 通道：分页（两页、边界重复、重复/循环 Token、完全相同页）、
// 错误分类（401/402/429/5xx/截断/非法 JSON）、§3 Row 契约逐字段、用量行与非支持数据集。
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
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// ---- fixtures ----

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encode fixture: %v", err)
	}
}

func transferFixture(direction, sender, recipient, mint, amount string, decimals int) map[string]any {
	return map[string]any{
		"direction": direction, "sender_address": sender, "recipient_address": recipient,
		"token":    map[string]any{"name": "Token", "symbol": "TKN", "address": mint},
		"decimals": decimals, "amount_string": amount,
	}
}

// txFixture 构造一条 Data API 交易；fee 与字段名按官方文档示例（timestamp 毫秒）。
func txFixture(signature string, slot uint64, timestampMS int64, transfers ...map[string]any) map[string]any {
	if transfers == nil {
		transfers = []map[string]any{}
	}
	return map[string]any{
		"type": "transfer", "hash": signature, "block": slot, "timestamp": timestampMS,
		"sender_address": testAddress, "recipient_address": "So11111111111111111111111111111111111111112",
		"status": "success",
		"fee": map[string]any{
			"token_symbol": "SOL", "token_address": "", "token_decimals": 9,
			"token_amount_string": "5000",
		},
		"transfers":  transfers,
		"attributes": map[string]any{"is_trash": false},
	}
}

func pageFixture(nextToken string, txs ...map[string]any) map[string]any {
	if txs == nil {
		txs = []map[string]any{}
	}
	return map[string]any{"next_page_token": nextToken, "data": txs}
}

// ---- 分页 ----

func TestDataAPIHistoryTwoPages(t *testing.T) {
	page1 := pageFixture("tok-2",
		txFixture("SigOne111111111111111111111111111111111111111111111111", 100, 1700000000000),
		txFixture("SigTwo222222222222222222222222222222222222222222222222", 99, 1699999000000),
	)
	page2 := pageFixture("",
		txFixture("SigThree3333333333333333333333333333333333333333333333", 98, 1699998000000),
	)
	var mu sync.Mutex
	var tokens, paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokens = append(tokens, r.URL.Query().Get("page_token"))
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Query().Get("page_token") == "" {
			writeJSON(t, w, page1)
			return
		}
		writeJSON(t, w, page2)
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	first, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress, PageSize: 100,
	})
	if err != nil {
		t.Fatalf("第一页失败: %v", err)
	}
	if len(first.Rows) != 2 || first.NextPageToken != "tok-2" || !first.HasMore {
		t.Fatalf("第一页结果不符: rows=%d token=%q hasMore=%v", len(first.Rows), first.NextPageToken, first.HasMore)
	}
	if first.RequestPageSize != 100 {
		t.Fatalf("RequestPageSize 应为 100，得到 %d", first.RequestPageSize)
	}
	second, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress, PageSize: 100, PageToken: "tok-2",
	})
	if err != nil {
		t.Fatalf("第二页失败: %v", err)
	}
	if len(second.Rows) != 1 || second.NextPageToken != "" || second.HasMore {
		t.Fatalf("第二页应结束: rows=%d token=%q hasMore=%v", len(second.Rows), second.NextPageToken, second.HasMore)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tokens) != 2 || tokens[0] != "" || tokens[1] != "tok-2" {
		t.Fatalf("page_token 必须原样传递: %#v", tokens)
	}
	for _, path := range paths {
		if !strings.Contains(path, testAddress) {
			t.Fatalf("地址必须原样出现在路径里（base58 大小写敏感）: %s", path)
		}
	}
}

func TestDataAPIPaginationRepeatToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 无条件返回同一个 token：Provider 没有推进游标。
		writeJSON(t, w, pageFixture("tok-loop",
			txFixture("SigLoop111111111111111111111111111111111111111111111111", 10, 1700000000000)))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress, PageToken: "tok-loop",
	})
	perr := requireCode(t, err, CodePaginationInvalid)
	if perr.Class != provider.ClassProviderInvalid {
		t.Fatalf("分页异常必须归 provider_invalid_response，得到 %s", perr.Class)
	}
	if perr.Retryable {
		t.Fatalf("分页异常不应重试")
	}
}

func TestDataAPIPaginationLoopToken(t *testing.T) {
	var mu sync.Mutex
	call := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		call++
		current := call
		mu.Unlock()
		switch current {
		case 1:
			writeJSON(t, w, pageFixture("tok-a", txFixture("SigA11111111111111111111111111111111111111111111111111", 10, 1700000000000)))
		case 2:
			writeJSON(t, w, pageFixture("tok-b", txFixture("SigB11111111111111111111111111111111111111111111111111", 9, 1699999000000)))
		default:
			// 回到 tok-a：循环。
			writeJSON(t, w, pageFixture("tok-a", txFixture("SigC11111111111111111111111111111111111111111111111111", 8, 1699998000000)))
		}
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	ctx := context.Background()
	req := provider.FetchRequest{Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress}
	if _, err := env.client.Fetch(ctx, req); err != nil {
		t.Fatalf("第一页应成功: %v", err)
	}
	req.PageToken = "tok-a"
	if _, err := env.client.Fetch(ctx, req); err != nil {
		t.Fatalf("第二页应成功: %v", err)
	}
	req.PageToken = "tok-b"
	_, err := env.client.Fetch(ctx, req)
	requireCode(t, err, CodePaginationInvalid)
	if !strings.Contains(err.Error(), "loop") {
		t.Fatalf("应报告 token 循环: %v", err)
	}
}

func TestDataAPIPaginationIdenticalPages(t *testing.T) {
	var mu sync.Mutex
	call := 0
	// token 每页都推进，但内容完全相同 => 仍然是分页异常。
	page := func(token string) map[string]any {
		return pageFixture(token, txFixture("SigSame111111111111111111111111111111111111111111111111", 10, 1700000000000))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		call++
		current := call
		mu.Unlock()
		writeJSON(t, w, page("tok-"+strconv.Itoa(current)))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	ctx := context.Background()
	req := provider.FetchRequest{Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress}
	if _, err := env.client.Fetch(ctx, req); err != nil {
		t.Fatalf("第一页应成功: %v", err)
	}
	// 第二页内容与第一页完全相同（但 token 变了）=> 分页异常。
	req.PageToken = "tok-1"
	_, err := env.client.Fetch(ctx, req)
	requireCode(t, err, CodePaginationInvalid)
	if !strings.Contains(err.Error(), "identical") {
		t.Fatalf("应报告连续两页完全相同: %v", err)
	}
}

func TestDataAPIPageBoundaryOverlapIsVisibleNotFatal(t *testing.T) {
	boundary := txFixture("SigBoundary11111111111111111111111111111111111111111111", 100, 1700000000000)
	var mu sync.Mutex
	call := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		call++
		current := call
		mu.Unlock()
		if current == 1 {
			writeJSON(t, w, pageFixture("tok-2", boundary,
				txFixture("SigOnlyOne11111111111111111111111111111111111111111111", 101, 1700000100000)))
			return
		}
		// 第二页重复页边界的同一条交易（包含式分页），并带一条新交易。
		writeJSON(t, w, pageFixture("", boundary,
			txFixture("SigOnlyTwo11111111111111111111111111111111111111111111", 99, 1699999000000)))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	ctx := context.Background()
	req := provider.FetchRequest{Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress}
	if _, err := env.client.Fetch(ctx, req); err != nil {
		t.Fatalf("第一页应成功: %v", err)
	}
	req.PageToken = "tok-2"
	second, err := env.client.Fetch(ctx, req)
	if err != nil {
		t.Fatalf("页边界重复行不应让整页失败（去重由下载层幂等键负责）: %v", err)
	}
	if len(second.Rows) != 2 {
		t.Fatalf("Provider 返回 2 行就应原样返回，得到 %d", len(second.Rows))
	}
}

// ---- 错误分类 ----

func TestDataAPIErrorClassification(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		header     map[string]string
		body       string
		wantCode   string
		wantClass  provider.ErrorClass
		wantRetry  bool
		wantReduce bool
	}{
		{"401", http.StatusUnauthorized, nil, `{"error":"unauthorized"}`, CodeAuthFailed, provider.ClassAuth, false, false},
		{"403", http.StatusForbidden, nil, "", CodeAuthFailed, provider.ClassAuth, false, false},
		{"402", http.StatusPaymentRequired, nil, "", CodeQuotaExhausted, provider.ClassQuota, false, false},
		{"429", http.StatusTooManyRequests, map[string]string{"Retry-After": "2"}, "", CodeRateLimited, provider.ClassRateLimit, true, true},
		{"502", http.StatusBadGateway, nil, "", CodeInvalidResponse, provider.ClassUpstream, true, false},
		{"400", http.StatusBadRequest, nil, "", CodeInvalidResponse, provider.ClassInvalidRequest, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for key, value := range tc.header {
					w.Header().Set(key, value)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			env := newTestEnv(t, server, nil)

			_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
				Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
			})
			perr := requireCode(t, err, tc.wantCode)
			if perr.Class != tc.wantClass {
				t.Fatalf("class 应为 %s，得到 %s", tc.wantClass, perr.Class)
			}
			if perr.Retryable != tc.wantRetry {
				t.Fatalf("retryable 应为 %v，得到 %v", tc.wantRetry, perr.Retryable)
			}
			if perr.HTTPStatus != tc.status {
				t.Fatalf("必须保留 HTTP 状态码，得到 %d", perr.HTTPStatus)
			}
			if tc.wantReduce {
				if !perr.ReduceConcurrency || !perr.ReduceRate {
					t.Fatalf("429 必须降并发/降速: %#v", perr)
				}
				if perr.RetryAfter != 2*time.Second {
					t.Fatalf("必须遵守 Retry-After: %v", perr.RetryAfter)
				}
			}
			rows := env.usage.all()
			if len(rows) != 1 || rows[0].HTTPStatus != tc.status || rows[0].Status != "error" {
				t.Fatalf("失败请求也要有用量行: %#v", rows)
			}
			if rows[0].Credits != 0 {
				t.Fatalf("HTTP 错误不计 CU（无法确认上游是否扣费）: %v", rows[0].Credits)
			}
		})
	}
}

func TestDataAPIBodyTruncated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"next_page_token":"","data":[{"hash":"Sig"`)
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
	})
	perr := requireCode(t, err, CodeInvalidResponse)
	if perr.Class != provider.ClassBodyTruncated {
		t.Fatalf("截断必须归 body_truncated，得到 %s (%v)", perr.Class, err)
	}
	if !perr.Retryable {
		t.Fatalf("截断必须可重试")
	}
}

func TestDataAPIInvalidJSON(t *testing.T) {
	server := httptest.NewServer(jsonHandler(http.StatusOK, "<html>upstream gateway error</html>"))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
	})
	perr := requireCode(t, err, CodeInvalidResponse)
	if perr.Class != provider.ClassInvalidJSON {
		t.Fatalf("非法 JSON 必须归 invalid_json，得到 %s", perr.Class)
	}
	// 已经拿到 200 响应：CU 必须计入（上游确实做了工作）。
	rows := env.usage.all()
	if len(rows) != 1 || rows[0].Credits != float64(CUDataAPIHistory) {
		t.Fatalf("200 但解码失败也要计 CU: %#v", rows)
	}
}

// ---- 非支持数据集与参数校验 ----

func TestFetchInternalTransactionsUnsupported(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetInternalTransactions, Address: testAddress,
	})
	perr := requireCode(t, err, CodeDatasetUnsupported)
	if perr.Class != provider.ClassUnsupported {
		t.Fatalf("必须是 ClassUnsupported，得到 %s", perr.Class)
	}
	if calls != 0 {
		t.Fatalf("不支持的数据集不得发出请求，实际 %d", calls)
	}
}

func TestFetchRejectsNonSolanaChain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: "ethereum", Dataset: DatasetTransactions, Address: testAddress,
	})
	perr := requireCode(t, err, CodeInvalidResponse)
	if perr.Class != provider.ClassInvalidRequest {
		t.Fatalf("跨链请求必须被拒（invalid_request），得到 %s", perr.Class)
	}
}

func TestFetchRejectsInvalidAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: "0xnotasolanaaddress",
	})
	perr := requireCode(t, err, CodeInvalidResponse)
	if perr.Class != provider.ClassInvalidRequest {
		t.Fatalf("非法地址必须归 invalid_request，得到 %s", perr.Class)
	}
}

// ---- 时间范围 ----

func TestDataAPISendsTimeRangeInMillis(t *testing.T) {
	var gotStart, gotEnd, gotLimit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotStart = r.URL.Query().Get("start")
		gotEnd = r.URL.Query().Get("end")
		gotLimit = r.URL.Query().Get("limit")
		writeJSON(t, w, pageFixture(""))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	from := time.Unix(1700000000, 0).UTC()
	to := time.Unix(1700086400, 0).UTC()
	if _, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
		FromTime: &from, ToTime: &to, PageSize: 42,
	}); err != nil {
		t.Fatalf("取数失败: %v", err)
	}
	if gotStart != "1700000000000" || gotEnd != "1700086400000" {
		t.Fatalf("start/end 必须是毫秒: %s %s", gotStart, gotEnd)
	}
	if gotLimit != "42" {
		t.Fatalf("limit 应使用请求的页大小 42，得到 %s", gotLimit)
	}
}

func TestDataAPISlotRangeWithoutBlockTimeFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// 所有 getBlockTime 都返回 null：节点没有该 slot 的时间。
			writeRPCResult(t, w, json.RawMessage("null"))
			return
		}
		writeJSON(t, w, pageFixture(""))
	}))
	defer server.Close()
	env := newTestEnv(t, server, func(o *Options) { o.SlotTimeSearchLimit = 4 })

	from, to := uint64(100), uint64(200)
	_, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
		FromBlock: &from, ToBlock: &to,
	})
	requireCode(t, err, CodeSlotTimeUnavailable)
}

// ---- §3 Row 契约逐字段断言 ----

const testMint = "MintContract1111111111111111111111111111111111111111111"

func TestDataAPITransactionRowContract(t *testing.T) {
	const recipient = "RecipientAddr11111111111111111111111111111111111111"
	const signature = "SigContract111111111111111111111111111111111111111111111"
	tx := txFixture(signature, 250, 1700000000000,
		transferFixture("out", testAddress, recipient, "", "1500000000", 9),
		transferFixture("in", recipient, testAddress, testMint, "42", 6),
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, pageFixture("", tx))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	result, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress, RequestID: "req-contract",
	})
	if err != nil {
		t.Fatalf("取数失败: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("应输出 1 行交易，得到 %d", len(result.Rows))
	}
	row := rowObject(t, result.Rows[0])
	expectStrings := map[string]string{
		"blockNumber": "250", "blockHash": "", "hash": signature,
		"from": testAddress, "to": recipient, "value": "1500000000",
		"nonce": "0", "gasLimit": "0", "gasUsed": "0", "gasPrice": "0",
		"type": "transfer", "status": "1", "input": "", "feeRaw": "5000",
		"sourceApi": "data_api", "sourceMethod": "get_transactions_history",
		"sourceRequestId": "req-contract",
	}
	for field, want := range expectStrings {
		if got, _ := row[field].(string); got != want {
			t.Fatalf("字段 %s 应为 %q，得到 %#v", field, want, row[field])
		}
	}
	if row["timestamp"] != float64(1700000000) {
		t.Fatalf("timestamp 必须换算成秒: %#v", row["timestamp"])
	}
	for _, field := range []string{"signers", "accountKeys", "programIds"} {
		list, ok := row[field].([]any)
		if !ok || len(list) != 0 {
			t.Fatalf("Data API 不返回 %s，必须给空数组而不是伪造: %#v", field, row[field])
		}
	}
	payload := rowObject(t, json.RawMessage(row["chainPayloadJson"].(string)))
	if payload["chain"] != "solana" || payload["providerStatus"] != "success" {
		t.Fatalf("chainPayloadJson 不符: %#v", payload)
	}
	if payload["transferCount"] != float64(2) || payload["nativeTransferCount"] != float64(1) {
		t.Fatalf("chainPayloadJson 的转账计数不符: %#v", payload)
	}
}

func TestDataAPITransactionRowMultipleRecipientsLeavesToEmpty(t *testing.T) {
	const other = "OtherRecipient111111111111111111111111111111111111111"
	tx := txFixture("SigMulti11111111111111111111111111111111111111111111111", 251, 1700000100000,
		transferFixture("out", testAddress, other, "", "10", 9),
		transferFixture("out", testAddress, other+"x", "", "20", 9),
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, pageFixture("", tx))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	result, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
	})
	if err != nil {
		t.Fatalf("取数失败: %v", err)
	}
	row := rowObject(t, result.Rows[0])
	// 多笔原生转账：不压缩到单字段（§8.2）。
	if row["to"] != "" || row["value"] != "" {
		t.Fatalf("多接收方时 to/value 必须留空: %#v", row)
	}
}

func TestDataAPITokenTransferRowContract(t *testing.T) {
	const recipient = "RecipientAddr11111111111111111111111111111111111111"
	tx := txFixture("SigToken11111111111111111111111111111111111111111111111", 300, 1700000200000,
		transferFixture("out", testAddress, recipient, "", "1000", 9),
		transferFixture("in", recipient, testAddress, testMint, "42", 6),
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, pageFixture("", tx))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	result, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTokenTransfers, Address: testAddress, RequestID: "req-token",
	})
	if err != nil {
		t.Fatalf("取数失败: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("原生 SOL 转账不进 token_transfers（§8.3），应只剩 1 行，得到 %d", len(result.Rows))
	}
	row := rowObject(t, result.Rows[0])
	expectStrings := map[string]string{
		"blockHeight": "300", "transactionHash": "SigToken11111111111111111111111111111111111111111111111",
		"contractAddress": testMint, "tokenSymbol": "",
		"fromAddress": recipient, "toAddress": testAddress,
		"valueRawInteger": "42", "value": "42",
		"fromTokenAccount": "", "toTokenAccount": "",
		"sourceApi": "data_api", "sourceRequestId": "req-token",
	}
	for field, want := range expectStrings {
		if got, _ := row[field].(string); got != want {
			t.Fatalf("字段 %s 应为 %q，得到 %#v", field, want, row[field])
		}
	}
	if row["timestamp"] != float64(1700000200) || row["tokenDecimals"] != float64(6) {
		t.Fatalf("timestamp/decimals 不符: %#v", row)
	}
	// 本地稳定序号：transfers 数组下标（原生那笔是 0，被跳过），不是 EVM log index。
	if row["logIndex"] != float64(1) {
		t.Fatalf("logIndex 应为 transfers 数组下标: %#v", row["logIndex"])
	}
	// Data API 不返回真实指令位置：0 / -1（-1 = 非内层指令）。
	if row["instructionIndex"] != float64(0) || row["innerInstructionIndex"] != float64(-1) {
		t.Fatalf("指令位置字段不符: %#v", row)
	}
}

func TestDataAPINFTTransferRowContract(t *testing.T) {
	const recipient = "RecipientAddr11111111111111111111111111111111111111"
	nftTransfer := transferFixture("out", testAddress, recipient, testMint, "1", 0)
	nftTransfer["token"].(map[string]any)["nft"] = map[string]any{"id": "nft-1"}
	tx := txFixture("SigNFT1111111111111111111111111111111111111111111111111", 400, 1700000300000, nftTransfer)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, pageFixture("", tx))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	// NFT 不进 token_transfers。
	tokens, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTokenTransfers, Address: testAddress,
	})
	if err != nil {
		t.Fatalf("取数失败: %v", err)
	}
	if len(tokens.Rows) != 0 {
		t.Fatalf("NFT 转账不应出现在 token_transfers: %#v", tokens.Rows)
	}

	result, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetNFTTransfers, Address: testAddress, RequestID: "req-nft",
	})
	if err != nil {
		t.Fatalf("取数失败: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("应输出 1 行 NFT 转账，得到 %d", len(result.Rows))
	}
	row := rowObject(t, result.Rows[0])
	expectStrings := map[string]string{
		"blockNumber": "400", "transactionHash": "SigNFT1111111111111111111111111111111111111111111111111",
		"contractAddress": testMint, "fromAddress": testAddress, "toAddress": recipient,
		"value": "1", "tokenStandard": "SPL",
		"sourceApi": "data_api", "sourceRequestId": "req-nft",
	}
	for field, want := range expectStrings {
		if got, _ := row[field].(string); got != want {
			t.Fatalf("字段 %s 应为 %q，得到 %#v", field, want, row[field])
		}
	}
	if row["timestamp"] != float64(1700000300) || row["logIndex"] != float64(0) {
		t.Fatalf("timestamp/logIndex 不符: %#v", row)
	}
}

func TestDataAPIFailedTransactionStatusIsZero(t *testing.T) {
	tx := txFixture("SigFail111111111111111111111111111111111111111111111111", 401, 1700000400000)
	tx["status"] = "failed"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, pageFixture("", tx))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	result, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress,
	})
	if err != nil {
		t.Fatalf("取数失败: %v", err)
	}
	row := rowObject(t, result.Rows[0])
	if row["status"] != "0" {
		t.Fatalf("失败交易必须写 status=0: %#v", row["status"])
	}
	payload := rowObject(t, json.RawMessage(row["chainPayloadJson"].(string)))
	if payload["providerStatus"] != "failed" {
		t.Fatalf("原始 status 必须保留在 chainPayloadJson: %#v", payload)
	}
}

func TestDataAPIUsageRowFields(t *testing.T) {
	tx := txFixture("SigUsage111111111111111111111111111111111111111111111111", 500, 1700000500000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, pageFixture("", tx))
	}))
	defer server.Close()
	env := newTestEnv(t, server, nil)

	if _, err := env.client.Fetch(context.Background(), provider.FetchRequest{
		Chain: ChainSolana, Dataset: DatasetTransactions, Address: testAddress, RequestID: "req-usage", Priority: "high",
	}); err != nil {
		t.Fatalf("取数失败: %v", err)
	}
	rows := env.usage.all()
	if len(rows) != 1 {
		t.Fatalf("每个 HTTP 请求一条用量行，得到 %d", len(rows))
	}
	row := rows[0]
	if row.Provider != ProviderKey || row.APIType != APITypeDataAPI || row.Chain != ChainSolana {
		t.Fatalf("用量行基础字段不符: %#v", row)
	}
	if row.Method != MethodDataAPIHistory || row.UsageType != UsageDatasetDownload {
		t.Fatalf("方法/用量类型不符: %#v", row)
	}
	if row.Dataset != DatasetTransactions || row.Rows != 1 || row.MethodCalls < 1 || row.HTTPRequests != 1 {
		t.Fatalf("行数/方法调用数不符: %#v", row)
	}
	if row.Credits != float64(CUDataAPIHistory) {
		t.Fatalf("Credits 必须存 CU: %v", row.Credits)
	}
	wantCost := float64(CUDataAPIHistory) * USDPerCU
	if diff := row.CostUSD - wantCost; diff > 1e-12 || diff < -1e-12 {
		t.Fatalf("CostUSD 应为 %v，得到 %v", wantCost, row.CostUSD)
	}
	if row.Status != "ok" || row.HTTPStatus != http.StatusOK || row.ErrorClass != "" {
		t.Fatalf("状态字段不符: %#v", row)
	}
	if row.RequestID != "req-usage" || row.Priority != "high" {
		t.Fatalf("请求关联字段不符: %#v", row)
	}
	if row.PageSize != DefaultHistoryPageSize {
		t.Fatalf("用量行应记录有效页大小: %#v", row)
	}
}
