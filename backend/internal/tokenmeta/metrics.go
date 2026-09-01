package tokenmeta

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ObservationsTotal   = promauto.NewCounterVec(prometheus.CounterOpts{Name: "token_metadata_observations_total", Help: "Token metadata observations retained"}, []string{"source", "valid"})
	ResolvedTotal       = promauto.NewCounterVec(prometheus.CounterOpts{Name: "token_metadata_resolved_total", Help: "Canonical token metadata resolutions"}, []string{"status"})
	RPCRequestsTotal    = promauto.NewCounter(prometheus.CounterOpts{Name: "token_metadata_rpc_requests_total", Help: "Token metadata RPC resolve attempts"})
	RPCErrorsTotal      = promauto.NewCounter(prometheus.CounterOpts{Name: "token_metadata_rpc_errors_total", Help: "Token metadata RPC resolve failures"})
	BackfillTokensTotal = promauto.NewCounter(prometheus.CounterOpts{Name: "token_metadata_backfill_tokens_total", Help: "Token contracts scanned by metadata backfill"})
	BackfillActive      = promauto.NewGauge(prometheus.GaugeOpts{Name: "token_metadata_backfill_active", Help: "Whether token metadata backfill is active"})
	QueueDepth          = promauto.NewGauge(prometheus.GaugeOpts{Name: "token_metadata_queue_depth", Help: "Runnable token metadata queue depth"})
	LogoFetchTotal      = promauto.NewCounterVec(prometheus.CounterOpts{Name: "token_logo_fetch_total", Help: "Token logo localization attempts"}, []string{"status"})
)
