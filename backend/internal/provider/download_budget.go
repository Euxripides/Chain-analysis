package provider

import (
	"context"
	"errors"
	"math"
)

var ErrDownloadBudget = errors.New("download recovery request budget exhausted or unavailable")

const ClassDownloadBudget ErrorClass = "download_budget"

type downloadBudgetKey struct{}
type DownloadRequestBudget func(context.Context, int64, float64, float64) error

func WithDownloadBudget(ctx context.Context, reserve DownloadRequestBudget) context.Context {
	return context.WithValue(ctx, downloadBudgetKey{}, reserve)
}

// Called immediately before each real HTTP attempt, including retries and RPC
// batches. Cache hits and open-circuit rejections never reserve paid requests.
func ReserveDownloadRequest(ctx context.Context, calls int64, credits, cost float64) error {
	reserve, _ := ctx.Value(downloadBudgetKey{}).(DownloadRequestBudget)
	if reserve == nil {
		return nil
	}
	if calls < 1 || credits < 0 || cost < 0 || math.IsNaN(credits) || math.IsNaN(cost) || math.IsInf(credits, 0) || math.IsInf(cost, 0) {
		return &Error{Class: ClassDownloadBudget, Message: "download recovery pricing is unavailable", Cause: ErrDownloadBudget}
	}
	if err := reserve(ctx, calls, credits, cost); err != nil {
		return &Error{Class: ClassDownloadBudget, Message: "download recovery paused at its persisted request or cost limit", Cause: ErrDownloadBudget}
	}
	return nil
}

func (e *Error) Unwrap() error { return e.Cause }
