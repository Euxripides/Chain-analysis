package tron

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// dailyQuota is an in-process guard for the TronGrid daily account budget.
// The provider_usage table remains the durable audit source; this guard stops
// a worker before it sends a request and resets at the UTC account day.
type dailyQuota struct {
	mu     sync.Mutex
	day    time.Time
	used   int64
	budget int64
	warned map[int]bool
}

func newDailyQuota(budget int64) *dailyQuota {
	return &dailyQuota{budget: budget, day: utcDay(time.Now()), warned: map[int]bool{}}
}

func utcDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func (q *dailyQuota) allow(ctx context.Context, priority string) (string, error) {
	if q == nil || q.budget <= 0 {
		return "", nil
	}
	select {
	case <-ctx.Done():
		return "", provider.NewCancelled(ctx.Err().Error())
	default:
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	day := utcDay(time.Now())
	if !day.Equal(q.day) {
		q.day, q.used, q.warned = day, 0, map[int]bool{}
	}
	if q.used >= q.budget {
		return "", &provider.Error{Provider: "trongrid", Class: provider.ClassQuota, Message: "TronGrid daily request budget exhausted", Retryable: false}
	}
	pct := int((q.used * 100) / q.budget)
	level := "normal"
	switch {
	case pct >= 95:
		level = "manual_only"
		if !quotaPriorityAllowed(priority, true) {
			return level, &provider.Error{Provider: "trongrid", Class: provider.ClassQuota, Message: "TronGrid daily budget is above 95%; only manual or investigation requests are allowed", Retryable: false}
		}
	case pct >= 90:
		level = "pause_background"
		if !quotaPriorityAllowed(priority, false) {
			return level, &provider.Error{Provider: "trongrid", Class: provider.ClassQuota, Message: "TronGrid daily budget is above 90%; background requests are paused", Retryable: false}
		}
	case pct >= 80:
		level = "warn"
	}
	q.used++
	return level, nil
}

func quotaPriorityAllowed(priority string, manualOnly bool) bool {
	p := strings.ToLower(strings.TrimSpace(priority))
	if manualOnly {
		return p == "manual" || p == "investigation" || p == "interactive" || p == "realtime"
	}
	return p != "background" && p != "maintenance" && p != "backfill"
}

func (q *dailyQuota) snapshot() (used, budget int64, level string) {
	if q == nil {
		return 0, 0, "disabled"
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !utcDay(time.Now()).Equal(q.day) {
		q.day, q.used, q.warned = utcDay(time.Now()), 0, map[int]bool{}
	}
	used, budget = q.used, q.budget
	if budget <= 0 {
		return used, budget, "disabled"
	}
	pct := (used * 100) / budget
	switch {
	case pct >= 100:
		level = "exhausted"
	case pct >= 95:
		level = "manual_only"
	case pct >= 90:
		level = "pause_background"
	case pct >= 80:
		level = "warn"
	default:
		level = "normal"
	}
	return used, budget, level
}

func quotaMessage(level string, used, budget int64) string {
	if budget <= 0 || level == "normal" || level == "disabled" {
		return ""
	}
	return fmt.Sprintf("TronGrid daily quota level=%s used=%d budget=%d", level, used, budget)
}
