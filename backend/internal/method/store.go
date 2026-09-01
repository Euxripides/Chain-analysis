package method

import (
	"context"
	"time"
)

// SignatureRecord method_signatures 一行（§11）。
type SignatureRecord struct {
	Selector  string `json:"selector"`
	Name      string `json:"method_name"`
	Signature string `json:"signature"`
	Source    string `json:"source"`
	Verified  bool   `json:"verified"`
}

// ABIRecord contract_abis 一行（§13）。
type ABIRecord struct {
	Chain                 string    `json:"chain"`
	ContractAddress       string    `json:"contract_address"`
	ABIJSON               []byte    `json:"-"`
	Source                string    `json:"source"`
	Verified              bool      `json:"verified"`
	ImplementationAddress string    `json:"implementation_address,omitempty"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// EventSignatureRecord event_signatures 一行（§29）。
type EventSignatureRecord struct {
	Topic0    string `json:"topic0"`
	Name      string `json:"event_name"`
	Signature string `json:"signature"`
	Source    string `json:"source"`
	Verified  bool   `json:"verified"`
}

// ResolveJob 未知 Selector 补全任务（§18：不放完整 input，只放长度）。
type ResolveJob struct {
	ID              string `json:"id"`
	Chain           string `json:"chain"`
	ContractAddress string `json:"contract_address"`
	MethodID        string `json:"method_id"`
	SampleTxHash    string `json:"sample_tx_hash"`
	InputLength     int    `json:"input_length"`
	Attempts        int    `json:"attempts"`
}

// BackfillJob 历史数据回填任务（§35）。
type BackfillJob struct {
	ID                  string     `json:"id"`
	Chain               string     `json:"chain"`
	Status              string     `json:"status"`
	Priority            string     `json:"priority"`
	BatchSize           int        `json:"batch_size"`
	CheckpointTimestamp *time.Time `json:"checkpoint_timestamp"`
	CheckpointTxHash    string     `json:"checkpoint_tx_hash"`
	TotalRows           int64      `json:"total_rows"`
	ScannedRows         int64      `json:"scanned_rows"`
	RewrittenRows       int64      `json:"rewritten_rows"`
	ResolvedRows        int64      `json:"resolved_rows"`
	UnknownRows         int64      `json:"unknown_rows"`
	Error               string     `json:"error"`
	CreatedAt           time.Time  `json:"created_at"`
	StartedAt           *time.Time `json:"started_at"`
	FinishedAt          *time.Time `json:"finished_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// Backfill 任务状态机（§35：checkpoint / pause / resume / cancel / progress）。
const (
	BackfillPending   = "PENDING"
	BackfillRunning   = "RUNNING"
	BackfillPauseReq  = "PAUSE_REQUESTED"
	BackfillPaused    = "PAUSED"
	BackfillCancelReq = "CANCEL_REQUESTED"
	BackfillCancelled = "CANCELLED"
	BackfillCompleted = "COMPLETED"
	BackfillFailed    = "FAILED"
)

// Store 控制面持久化（PostgreSQL）。方法层只依赖接口，便于单测替换。
type Store interface {
	ListMethodSignatures(ctx context.Context) ([]SignatureRecord, error)
	CandidatesBySelector(ctx context.Context, selector string) ([]SignatureRecord, error)
	SearchMethodSignatures(ctx context.Context, query string, limit int) ([]SignatureRecord, error)
	UpsertMethodSignatures(ctx context.Context, records []SignatureRecord) (int64, error)
	BumpSignatureOccurrence(ctx context.Context, selector string, delta int64) error

	ListContractABIs(ctx context.Context, limit int) ([]ABIRecord, error)
	UpsertContractABI(ctx context.Context, record ABIRecord) error

	ListEventSignatures(ctx context.Context) ([]EventSignatureRecord, error)
	UpsertEventSignatures(ctx context.Context, records []EventSignatureRecord) (int64, error)

	EnqueueMethodResolve(ctx context.Context, jobs []ResolveJob) error
	LeaseMethodResolveJobs(ctx context.Context, limit int) ([]ResolveJob, error)
	FinishMethodResolveJob(ctx context.Context, id, status, message string) error
	MethodResolveQueueDepth(ctx context.Context) (int64, error)

	CreateBackfillJob(ctx context.Context, chain string, batchSize int) (*BackfillJob, error)
	GetBackfillJob(ctx context.Context, id string) (*BackfillJob, error)
	ListBackfillJobs(ctx context.Context, limit int) ([]BackfillJob, error)
	NextRunnableBackfillJob(ctx context.Context) (*BackfillJob, error)
	SetBackfillStatus(ctx context.Context, id, status, errMessage string) error
	CheckpointBackfillJob(ctx context.Context, id string, ts *time.Time, txHash string, scanned, rewritten, resolved, unknown int64) error
	SetBackfillTotal(ctx context.Context, id string, total int64) error
}
