package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// P1-A/P1-B AUTH-007 observability.
//
// The commit gate serialises a session-scoped write against every scope
// mutation (workspace switch, logout, forced logout, membership revocation) by
// holding a row lock on the caller session row for the whole handler. That makes
// the mutation latency bounded below by the length of the critical section, so
// the length itself has to be a first-class measurement instead of an
// assumption. The histogram below is that measurement: it is recorded on every
// gated request, whether the request committed or was refused at the commit
// boundary.
//
// Buckets are chosen around the AUTH-007 budget: 5 s is the hard requirement, so
// the 500/1000/2000/4000/5000/8000/12000/30000 buckets make "how close is this
// deployment to the budget" readable from a single scrape.
//
// No label carries a user, a session, a route parameter or any secret: the
// permission key and the outcome are the only dimensions.
var (
	// ScopeCommitGateCriticalMs is the critical-section duration in milliseconds,
	// measured from the moment the gate holds the resolved scope to the moment it
	// releases the row lock.
	ScopeCommitGateCriticalMs = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cs_scope_commit_gate_critical_ms",
		Help:    "Commit-gate critical section duration in milliseconds (re-check to lock release)",
		Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2000, 4000, 5000, 8000, 12000, 30000},
	}, []string{"permission", "outcome"})

	// ScopeCommitGateCriticalSlow counts gated requests whose critical section
	// exceeded the warning threshold, i.e. the requests that can push a concurrent
	// scope mutation towards the AUTH-007 budget.
	ScopeCommitGateCriticalSlow = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cs_scope_commit_gate_critical_slow_total",
		Help: "Gated requests whose critical section exceeded the configured warning threshold",
	}, []string{"permission"})

	// ScopeCommitGateRequests counts gated requests by outcome: allowed (the
	// handler ran inside the gate) or refused_* (the commit boundary refused it).
	ScopeCommitGateRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cs_scope_commit_gate_requests_total",
		Help: "Requests handled by the commit gate by outcome",
	}, []string{"permission", "outcome"})

	// ScopeCommitGateWaitMs is how long a gated request waited for a gate
	// connection or for the session row itself before it could start.
	ScopeCommitGateWaitMs = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cs_scope_commit_gate_wait_ms",
		Help:    "Time a gated request spent acquiring the commit gate in milliseconds",
		Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2000, 4000, 5000, 8000},
	}, []string{"permission", "outcome"})

	// ScopeCommitGateSpooled counts gated responses that outgrew the in-memory
	// retractable buffer and continued in a private temporary file. Nothing was
	// sent to the client before the commit decision in either case; the counter
	// exists so that "how often does a gated route need disk" is measurable
	// instead of guessed, and so that a spool leak would show up as a rate.
	ScopeCommitGateSpooled = promauto.NewCounter(prometheus.CounterOpts{
		Name: "cs_scope_commit_gate_spooled_total",
		Help: "Gated responses spilled from memory to a private temporary file before the commit decision",
	})

	// ScopeCommitGateTooLarge counts gated handlers that tried to produce more
	// than the hard response bound. Those requests are cancelled and failed closed
	// instead of being streamed to a caller who would then be told the opposite of
	// what the database did.
	ScopeCommitGateTooLarge = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cs_scope_commit_gate_response_too_large_total",
		Help: "Gated handlers whose response exceeded the hard bound and were cancelled and rolled back",
	}, []string{"permission"})
)
