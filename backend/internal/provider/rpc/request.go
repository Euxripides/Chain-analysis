package rpc

import (
	"fmt"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
)

// Priority 调度优先级（§163/§164）。
type Priority string

const (
	PriorityInteractive Priority = "interactive"
	PriorityRealtime    Priority = "realtime"
	PriorityNormal      Priority = "normal"
	PriorityBackground  Priority = "background"
	PriorityMaintenance Priority = "maintenance"
)

// Weight 数值越大越优先（用于公平调度与背压判断，§165/§166）。
func (p Priority) Weight() int {
	switch p {
	case PriorityInteractive:
		return 100
	case PriorityRealtime:
		return 80
	case PriorityNormal:
		return 50
	case PriorityBackground:
		return 20
	case PriorityMaintenance:
		return 10
	default:
		return 50
	}
}

func (p Priority) OrDefault() Priority {
	if p == "" {
		return PriorityNormal
	}
	return p
}

// Consistency 跨模块统一的缓存语义（§202-§206）。
type Consistency string

const (
	ConsistencyLocalOnly    Consistency = "local_only"
	ConsistencyAuto         Consistency = "auto"
	ConsistencyForceRefresh Consistency = "force_refresh"
)

// ParseConsistency 解析 ?decode=/?mode= 参数，默认 auto。
func ParseConsistency(value string) (Consistency, error) {
	switch Consistency(strings.ToLower(strings.TrimSpace(value))) {
	case "", ConsistencyAuto:
		return ConsistencyAuto, nil
	case ConsistencyLocalOnly:
		return ConsistencyLocalOnly, nil
	case ConsistencyForceRefresh:
		return ConsistencyForceRefresh, nil
	default:
		return "", fmt.Errorf("mode must be local_only, auto or force_refresh")
	}
}

func (c Consistency) OrDefault() Consistency {
	if c == "" {
		return ConsistencyAuto
	}
	return c
}

// Purpose 固定用途枚举（§38）：用于用量归属与预算维度。
const (
	PurposeChainHead              = "chain_head"
	PurposeTokenMetadata          = "token_metadata"
	PurposeBalance                = "balance"
	PurposeContractClassification = "contract_classification"
	PurposeTransactionVerify      = "transaction_verify"
	PurposeReceipt                = "receipt"
	PurposeLogsRepair             = "logs_repair"
	PurposeTrace                  = "trace"
	PurposeDebugTrace             = "debug_trace"
	PurposeInvestigation          = "investigation"
	PurposeAdmin                  = "admin"
	PurposeCanary                 = "canary"
)

var validPurposes = map[string]bool{
	PurposeChainHead: true, PurposeTokenMetadata: true, PurposeBalance: true,
	PurposeContractClassification: true, PurposeTransactionVerify: true, PurposeReceipt: true,
	PurposeLogsRepair: true, PurposeTrace: true, PurposeDebugTrace: true,
	PurposeInvestigation: true, PurposeAdmin: true, PurposeCanary: true,
}

// ValidPurpose 未登记的 Purpose 不允许进入用量统计（§38）。
func ValidPurpose(purpose string) bool { return validPurposes[purpose] }

// BudgetContext 预算上下文（§37）。
type BudgetContext struct {
	OrganizationID  string `json:"organization_id,omitempty"`
	WorkspaceID     string `json:"workspace_id,omitempty"`
	JobID           string `json:"job_id,omitempty"`
	InvestigationID string `json:"investigation_id,omitempty"`

	Purpose string `json:"purpose"`

	MaxRequests int64 `json:"max_requests,omitempty"`
	MaxCredits  int64 `json:"max_credits,omitempty"`

	Priority Priority `json:"priority,omitempty"`
}

// Key 预算计数维度：调查 > 任务 > 工作区 > 组织 > 用途（§169）。
func (b BudgetContext) Key() string {
	switch {
	case b.InvestigationID != "":
		return "investigation:" + b.InvestigationID
	case b.JobID != "":
		return "job:" + b.JobID
	case b.WorkspaceID != "":
		return "workspace:" + b.WorkspaceID
	case b.OrganizationID != "":
		return "org:" + b.OrganizationID
	default:
		return "purpose:" + b.Purpose
	}
}

// Limited 是否声明了显式上限。
func (b BudgetContext) Limited() bool { return b.MaxRequests > 0 || b.MaxCredits > 0 }

// CachePolicy 单请求缓存覆盖（默认取 MethodSpec，§47）。
type CachePolicy struct {
	Disabled bool
	// TTLOverride > 0 时覆盖方法默认 TTL
	TTLOverride int64 // 纳秒；避免与 time 依赖交叉，构造时用 CacheTTL()
}

// Request 统一 RPC 请求（§12）。
type Request struct {
	Chain string

	Method string
	Params []any

	// Class 可留空：由 Method Registry 推断（§15）
	Class MethodClass

	Priority    Priority
	Budget      BudgetContext
	CachePolicy CachePolicy
	Consistency Consistency

	// BlockRef 决定 archive 需求与缓存策略（§52/§53/§152）
	BlockRef chain.BlockRef

	TraceID string
}

// Normalize 补齐默认值。
func (r Request) Normalize() Request {
	r.Chain = strings.ToLower(strings.TrimSpace(r.Chain))
	r.Priority = r.Priority.OrDefault()
	r.Consistency = r.Consistency.OrDefault()
	if r.Budget.Priority == "" {
		r.Budget.Priority = r.Priority
	}
	if r.Budget.Purpose == "" {
		r.Budget.Purpose = PurposeAdmin
	}
	return r
}

// RequiresArchive 固定历史区块必须走 Archive Provider（§88/§152/§247-15）。
func (r Request) RequiresArchive() bool { return r.BlockRef.IsFixed() }
