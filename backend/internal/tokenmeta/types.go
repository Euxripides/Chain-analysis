// Package tokenmeta implements V1.4 token metadata enrichment. Chain facts remain
// in ClickHouse; every metadata assertion is retained as a PostgreSQL observation.
package tokenmeta

import (
	"context"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

const (
	StatusUnknown  = "unknown"
	StatusPartial  = "partial"
	StatusComplete = "complete"
	StatusConflict = "conflict"
	StatusFailed   = "failed"
)

type Observation struct {
	Chain           string    `json:"chain"`
	ContractAddress string    `json:"contract_address"`
	Name            string    `json:"name,omitempty"`
	Symbol          string    `json:"symbol,omitempty"`
	Decimals        *int      `json:"decimals,omitempty"`
	TokenType       string    `json:"token_type,omitempty"`
	LogoURL         string    `json:"logo_url,omitempty"`
	Source          string    `json:"source"`
	SourceRef       string    `json:"source_ref,omitempty"`
	BlockNumber     *uint64   `json:"block_number,omitempty"`
	ObservedAt      time.Time `json:"observed_at"`
	RawHash         string    `json:"raw_hash,omitempty"`
	Valid           bool      `json:"valid"`
	ValidationError string    `json:"validation_error,omitempty"`
}

type Metadata struct {
	Chain           string     `json:"chain"`
	ContractAddress string     `json:"contract_address"`
	Name            string     `json:"name,omitempty"`
	Symbol          string     `json:"symbol,omitempty"`
	Decimals        *int       `json:"decimals"`
	TokenType       string     `json:"token_type,omitempty"`
	LogoURL         string     `json:"logo_url,omitempty"`
	LogoObjectKey   string     `json:"logo_object_key,omitempty"`
	LogoMimeType    string     `json:"logo_mime_type,omitempty"`
	Verified        bool       `json:"verified"`
	MetadataSource  string     `json:"metadata_source,omitempty"`
	Confidence      string     `json:"confidence"`
	MetadataStatus  string     `json:"metadata_status"`
	FirstSeenBlock  *int64     `json:"first_seen_block,omitempty"`
	LastSeenBlock   *int64     `json:"last_seen_block,omitempty"`
	FirstSeenAt     *time.Time `json:"first_seen_at,omitempty"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
	LastResolvedAt  *time.Time `json:"last_resolved_at,omitempty"`
	LastVerifiedAt  *time.Time `json:"last_verified_at,omitempty"`
}

type CatalogItem struct {
	Chain           string
	ContractAddress string
	FirstBlock      uint64
	LastBlock       uint64
	FirstSeen       time.Time
	LastSeen        time.Time
	TransferCount   uint64
	Symbol          string
	Decimals        *int
}

type Summary struct {
	Contracts  int64 `json:"token_contracts"`
	Complete   int64 `json:"complete"`
	Partial    int64 `json:"partial"`
	Unknown    int64 `json:"unknown"`
	Conflict   int64 `json:"conflict"`
	Failed     int64 `json:"failed"`
	WithLogo   int64 `json:"logo_available"`
	QueueDepth int64 `json:"queue_depth"`
}

type RPCResolver interface {
	ResolveTokenMetadata(ctx context.Context, chain, contract string) (provider.TokenMetadataResult, error)
}
