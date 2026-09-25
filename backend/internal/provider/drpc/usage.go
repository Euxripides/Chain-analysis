package drpc

// 本文件实现用量与成本记录（规范 §1.4 + 开发文档 §15.1）：
// **每次 HTTP 请求**写一条 model.UsageRow，包含通道/方法/用量类型/行数/方法调用数/
// CU/CostUSD/状态/HTTP 状态/RPC code/错误分类/request_id/时长/TTFB/Body/wire/decoded 字节。
import (
	"context"
	"errors"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// UsageType 取值（§15.1）。
const (
	UsageDatasetDownload = "dataset_download"
	UsageTxDetail        = "tx_detail"
	UsageBalanceSnapshot = "balance_snapshot"
	UsageProviderTest    = "provider_test"
	// UsageChainHead 是取链头（getSlot）的用量类型：全量历史任务创建时固化目标 Slot（§5.3）。
	UsageChainHead = "chain_head"
	UsageQuotaSync = "quota_sync"
)

// APIType 取值（§15.1：api_type=data_api|rpc）。
const (
	APITypeDataAPI = "data_api"
	APITypeRPC     = "rpc"
)

// usageMeta 是一次 HTTP 请求的用量归因。
type usageMeta struct {
	// ctx 携带调度器注入的任务/分片归属（与 ankr/chainbase 相同的 "job_id"/"shard_id"
	// 字符串键约定）。缺了它，后台"任务详情 → Provider 运行"的已用请求/已用 CU 永远是 0，
	// 计划 §15.1 要求的 job_id/shard_id 归因也就落不了库。
	ctx      context.Context
	dataset  string
	apiType  string
	channel  string
	method   string
	usageTyp string

	rows         int
	methodCalls  int
	httpRequests int
	cu           int64

	status     string
	httpStatus int
	rpcCode    int
	err        error

	requestID string
	started   time.Time
	ttfb      time.Duration
	body      time.Duration

	wireBytes    int64
	decodedBytes int64
	pageSize     int
	retryAttempt int
	retryReason  string
	priority     string
	txHash       string
}

// recordUsage 写一条用量行；未注入 hook 时静默跳过（不阻塞数据面）。
func (c *Client) recordUsage(meta usageMeta) {
	hook := c.hook
	if hook == nil {
		return
	}
	now := c.cfg.now()
	if meta.started.IsZero() {
		meta.started = now
	}
	if meta.status == "" {
		meta.status = "ok"
	}
	if meta.httpRequests <= 0 {
		meta.httpRequests = 1
	}
	if meta.methodCalls <= 0 {
		meta.methodCalls = 1
	}
	jobID, shardID, traceID, tenantID := usageAttribution(meta.ctx)
	row := model.UsageRow{
		Provider: ProviderKey,
		APIType:  meta.apiType,
		// APIChannel 让后台能按通道核对额度：data_api 与 solana_rpc 的并发、速率与 CU 口径不同。
		APIChannel:   meta.channel,
		Chain:        ChainSolana,
		Method:       meta.method,
		JobID:        jobID,
		ShardID:      shardID,
		TraceID:      traceID,
		TenantID:     tenantID,
		UsageType:    meta.usageTyp,
		Dataset:      meta.dataset,
		Rows:         meta.rows,
		ReturnedRows: meta.rows,
		MethodCalls:  meta.methodCalls,
		HTTPRequests: meta.httpRequests,
		// Credits 存 CU；CostUSD 是本地估算（正式计费以 provider_pricing + Statistics API 对账）。
		Credits:    float64(meta.cu),
		CostUSD:    float64(meta.cu) * USDPerCU,
		Status:     meta.status,
		HTTPStatus: meta.httpStatus,
		RPCCode:    meta.rpcCode,
		RequestID:  meta.requestID,
		TxHash:     meta.txHash,
		PageSize:   meta.pageSize,

		RequestTime:   meta.started,
		DurationMs:    int(now.Sub(meta.started).Milliseconds()),
		TTFBMs:        int(meta.ttfb.Milliseconds()),
		BodyMs:        int(meta.body.Milliseconds()),
		WireBytes:     meta.wireBytes,
		DecodedBytes:  meta.decodedBytes,
		ResponseBytes: int(meta.decodedBytes),

		RetryAttempt: meta.retryAttempt,
		RetryReason:  meta.retryReason,
		Priority:     meta.priority,
	}
	if meta.err != nil {
		if row.Status == "ok" {
			row.Status = "error"
		}
		row.APIError = meta.err.Error()
		// 结构化错误字段优先取错误自身的值，避免调用点漏填。
		// 本包只产生 *provider.Error，但按接口断言以免将来包装后丢字段。
		var perr *provider.Error
		if errors.As(meta.err, &perr) {
			if row.ErrorClass == "" {
				row.ErrorClass = string(perr.Class)
			}
			if row.HTTPStatus == 0 {
				row.HTTPStatus = perr.HTTPStatus
			}
			if row.RPCCode == 0 {
				row.RPCCode = perr.RPCCode
			}
		}
	}
	c.hook(row)
}

// usageAttribution 从 ctx 读出任务/分片归属。
//
// 键名沿用仓库既有约定（ankr/client.go、chainbase/client.go 同样读 "job_id"）：
// 调度器在 runShard 里用字符串键注入，Provider 不该另立一套键。
func usageAttribution(ctx context.Context) (jobID, shardID, traceID, tenantID string) {
	if ctx == nil {
		return "", "", "", ""
	}
	text := func(key string) string {
		if v, ok := ctx.Value(key).(string); ok {
			return v
		}
		return ""
	}
	return text("job_id"), text("shard_id"), text("trace_id"), text("tenant_id")
}
