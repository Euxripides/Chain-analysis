package api

// P1-1（性能）：explorer 的交互式读路径此前没有任何扫描预算 —— activity 与 flow-profile 在数万行
// 活动量级上是 8-11 秒级，且没有上限，超限只会"一直转"。这里统一给 explorer 读加预算，并把超限
// 翻译成**可操作**的类型化错误，而不是 500 或者静默长耗时。
//
// 为什么只用截止时间、不套 clickhouse 的交互 SETTINGS（2026-09-15 同一时刻 A/B 实测）：
//   flow-profile 无预算 10.80s / HTTP 200；套上 InteractiveScanContext（max_threads=4）后
//   20.02s 才失败 —— 把并行度压到 4 反而把一条本来能完成的聚合拖过了 20 秒预算。
// 预算是"不再无限等待并给出可操作回复"，不是"把能跑完的查询变慢"，所以这里只加截止时间；
// 内存与线程仍由服务器与连接默认值决定（重内存扫描走 /api/v1/address/flow-summary 那套交互设置）。

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

// explorerReadBudget 是单次 explorer 交互读的墙钟预算：40s 明显高于实测最慢的 10.8s，
// 又留足余量给 60s 的路由中间件（超限时由我们自己回答，而不是被传输层掐断）。
const explorerReadBudget = 40 * time.Second

// explorerReadContext 给 explorer 读请求套上截止时间。调用方必须 defer 返回的 cancel。
func explorerReadContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, explorerReadBudget)
}

// explorerScanRefusal 处理被预算拒绝的请求：命中则写出 503 与可操作提示并返回 true，
// 未命中返回 false，由调用方按原有语义处理。
func explorerScanRefusal(w http.ResponseWriter, r *http.Request, readCtx context.Context, err error, what string) bool {
	if errors.Is(err, context.DeadlineExceeded) || (readCtx != nil && errors.Is(readCtx.Err(), context.DeadlineExceeded)) {
		respondError(w, r, http.StatusServiceUnavailable, "SCAN_TIMEOUT",
			what+"超出交互查询预算（"+explorerReadBudget.String()+"），请收窄时间窗或减小 limit 后重试", nil)
		return true
	}
	if errors.Is(err, clickhouse.ErrScanTooLarge) {
		respondError(w, r, http.StatusServiceUnavailable, "SCAN_TOO_LARGE",
			what+"扫描量超出交互查询预算，请收窄时间窗，或改用导出任务", nil)
		return true
	}
	return false
}
