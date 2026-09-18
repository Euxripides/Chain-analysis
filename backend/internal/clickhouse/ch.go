// Package clickhouse：ClickHouse 数据面连接。
package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ErrScanTooLarge marks a scan the ClickHouse server refused because it would
// exceed the memory budget reserved for it (Code 241 MEMORY_LIMIT_EXCEEDED).
// It is a typed, user-visible outcome: the request was refused before it could
// exhaust the shared server, not silently truncated.
var ErrScanTooLarge = errors.New("export scan exceeded the ClickHouse memory budget")

// ErrScanTimeout marks an interactive (request-path) scan the ClickHouse server
// stopped because it exceeded the bounded execution time reserved for it (Code
// 159 TIMEOUT_EXCEEDED), or a scan whose client deadline expired first. Like
// ErrScanTooLarge it is a typed, user-visible outcome: the request was refused
// on its own budget instead of being killed by the transport with no
// diagnosable code.
var ErrScanTimeout = errors.New("interactive scan exceeded its bounded execution time")

// exportScanSettings bounds one export-range scan inside the ClickHouse server.
//
// Measured 2026-09-15 on the real instance: blockchain.token_transfers is
// ORDER BY (chain, tx_hash, log_index, token_address), so the export shape
// "SELECT * ... FINAL ... ORDER BY timestamp DESC LIMIT 2000000" cannot use the
// primary index and must merge + sort every matching row. For 0x...01 on bsc
// (11,957,608 rows) that needed 15.26 GiB and aborted with Code 241 against the
// 19.28 GiB server ceiling (OvercommitTracker), after which a legitimate
// multi-dataset ZIP produced no object at all.
//
// Measured budget of the same scan (FORMAT Null, all columns materialised): a
// 6.00 GiB cap aborts with Code 241 after 10 s, inside the FINAL merge read,
// while a 15.00 GiB cap completes in 25.3 s with a 7.19 GiB peak and 2,000,000
// rows. The real requirement is therefore ~7.2 GiB, and the sort spill is not
// the whole story: the floor comes from the FINAL merge read itself. 14 GiB is
// the smallest round cap with real headroom (~2x) that stays strictly below the
// 19.28 GiB server ceiling. A scan that still cannot fit now fails in ~10 s on
// its own budget and reports ErrScanTooLarge, instead of aborting the shared
// instance after 35-50 s with no object and no usable error.
var exportScanSettings = ch.Settings{
	"max_bytes_before_external_sort":     int(1) << 30,
	"max_bytes_before_external_group_by": int(1) << 30,
	"max_memory_usage":                   int(14) << 30,
	"max_threads":                        4,
}

// ExportScanContext returns a context whose queries carry exportScanSettings.
// clickhouse-go merges these with the connection defaults per query.
func ExportScanContext(ctx context.Context) context.Context {
	return ch.Context(ctx, ch.WithSettings(exportScanSettings))
}

// interactiveScanSettings bounds one interactive (request-path) aggregation.
//
// Measured 2026-09-15 on the real instance: the address flow-summary aggregate
// was the one request-path query that read a whole chain. With FINAL it read
// 85,074,805 rows and held 6.87-10.62 GiB (12 real calls in 30 minutes, 23.4 s
// to 52.3 s each), the bloom_filter skip indexes on from_address/to_address
// could not narrow it, and the server refused it with Code 241 against the
// 19.60 GiB ceiling. The explicitly deduplicated shape (see
// query.ActionFlowSummary) returns the same answer from 1.0-1.4 M rows in
// 0.3-3.3 s and 11-36 MiB, so this budget is a ceiling against a pathological
// caller, not a working limit: it keeps several interactive reads inside the
// export footprint (7x below exportScanSettings) and its execution time stays
// well inside the 60 s router middleware, so a scan that does not fit is
// refused by the server on its own budget instead of being cancelled by the
// transport.
// InteractiveScanTimeout is the wall-clock budget a request-path aggregation is
// allowed to spend. The query layer applies it as a context deadline, because
// clickhouse-go derives its own max_execution_time from a context deadline and
// that derived value WINS over an explicit one: measured 2026-09-15 with the
// live test, a request-shaped 60 s deadline made the server report
// max_execution_time=64 while the same call without a deadline reported the
// configured value. A bound that can be raised to 64 s by the transport is not a
// bound, so the interactive read carries both: this deadline and the matching
// per-query setting. 20 s is ~6x the worst measured latency of the fixed shape
// (3.3 s cold, 0.1-0.7 s warm) and leaves the refusal well inside the 60 s
// router middleware.
const InteractiveScanTimeout = 20 * time.Second

var interactiveScanSettings = ch.Settings{
	"max_execution_time":                 20,
	"max_memory_usage":                   int(2) << 30,
	"max_bytes_before_external_group_by": int(512) << 20,
	"max_threads":                        4,
}

// InteractiveScanContext returns a context whose queries carry
// interactiveScanSettings. clickhouse-go merges these with the connection
// defaults per query, and the request's own cancellation is preserved. Callers
// also apply InteractiveScanTimeout as a deadline; see it for why both are
// needed.
func InteractiveScanContext(ctx context.Context) context.Context {
	return ch.Context(ctx, ch.WithSettings(interactiveScanSettings))
}

// MaintenanceScanTimeout / maintenanceScanSettings bound the **background**
// aggregates (workspace address statistics, download verification counts).
//
// Measured 2026-09-17 on the real instance: the download finalize/benchmark path
// counted rows with "count() ... FINAL ... (from_address=? OR to_address=?)".
// FINAL on blockchain.token_transfers merges every part of the chain, so one
// count took 43.5 s and held ~7 GiB, while transactions took 3.2-4.9 s each.
// Those queries have no client waiting on them once the worker restarts (three
// orphaned ones were still running after 870 s), and while they run they pin
// ClickHouse at ~75% memory and 800% CPU, which is what pushed the explorer read
// endpoints' p95 from <1 s to 3-10 s.
//
// The counts themselves were rewritten to the equivalent non-FINAL shape (see
// addressmeta.dataCounts and ingest.Writer.CountFinal); this budget is the
// ceiling that keeps a future pathological caller from doing the same thing
// again: the request is refused on its own budget instead of holding the shared
// instance for minutes. Both callers treat a failure as best-effort (they log
// and continue), so a refusal degrades a statistic, never a user request.
const MaintenanceScanTimeout = 30 * time.Second

var maintenanceScanSettings = ch.Settings{
	"max_execution_time":                 30,
	"max_memory_usage":                   int(4) << 30,
	"max_bytes_before_external_group_by": int(512) << 20,
	"max_threads":                        4,
}

// MaintenanceScanContext returns a context whose queries carry
// maintenanceScanSettings. Callers also apply MaintenanceScanTimeout as a
// deadline for the same reason the interactive path does: clickhouse-go derives
// max_execution_time from a context deadline, so the two must agree.
func MaintenanceScanContext(ctx context.Context) context.Context {
	return ch.Context(ctx, ch.WithSettings(maintenanceScanSettings))
}

// DescribeScanError turns a server memory refusal into the typed
// ErrScanTooLarge so callers can surface it instead of a generic driver error.
// Every other error is returned unchanged.
func DescribeScanError(err error) error {
	if !IsMemoryLimitError(err) {
		return err
	}
	return fmt.Errorf("%w (Code 241 MEMORY_LIMIT_EXCEEDED); narrow the request with a block or time range: %v", ErrScanTooLarge, err)
}

// DescribeInteractiveScanError turns a bounded interactive scan refusal into
// the matching typed error so the request path can answer with a diagnosable
// code instead of a blanket 500: Code 241 becomes ErrScanTooLarge, Code 159 (or
// an expired client deadline) becomes ErrScanTimeout. Every other error is
// returned unchanged, so a real defect is still a 500.
func DescribeInteractiveScanError(err error) error {
	switch {
	case err == nil:
		return nil
	case IsMemoryLimitError(err):
		return DescribeScanError(err)
	case IsExecutionTimeoutError(err):
		return fmt.Errorf("%w (Code 159 TIMEOUT_EXCEEDED); narrow the request with start_time and end_time: %v", ErrScanTimeout, err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w (client deadline); narrow the request with start_time and end_time: %v", ErrScanTimeout, err)
	default:
		return err
	}
}

// IsExecutionTimeoutError reports whether err is a ClickHouse Code 159
// execution-time refusal, in either its numeric or its symbolic form.
func IsExecutionTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "code: 159") || strings.Contains(msg, "timeout_exceeded")
}

// IsMemoryLimitError reports whether err is a ClickHouse Code 241 memory
// refusal, in either its numeric or its symbolic form.
func IsMemoryLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "code: 241") || strings.Contains(msg, "memory_limit_exceeded")
}

type Conn struct {
	db driver.Conn
}

func New(ctx context.Context, dsn string) (*Conn, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse clickhouse url: %w", err)
	}
	var user, pass string
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	dbName := "blockchain"
	if p := u.Path; p != "" && p != "/" {
		dbName = p[1:]
	}
	opts := &ch.Options{
		Addr:            []string{u.Host},
		Auth:            ch.Auth{Database: dbName, Username: user, Password: pass},
		DialTimeout:     10 * time.Second,
		MaxOpenConns:    16,
		MaxIdleConns:    8,
		ConnMaxLifetime: time.Hour,
		Compression:     &ch.Compression{Method: ch.CompressionLZ4},
		Settings: ch.Settings{
			"async_insert":               1,
			"wait_for_async_insert":      1,
			"async_insert_max_data_size": "10000000",
		},
	}
	db, err := ch.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouse open: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		return nil, fmt.Errorf("clickhouse ping: %w", err)
	}
	return &Conn{db: db}, nil
}

func (c *Conn) DB() driver.Conn { return c.db }

func (c *Conn) Exec(ctx context.Context, query string) error {
	return c.db.Exec(ctx, query)
}

func (c *Conn) Close() error { return c.db.Close() }
