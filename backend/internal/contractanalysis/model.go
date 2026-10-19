// Package contractanalysis contains the isolated contract processor protocol.
package contractanalysis

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

const Version = "contract-analysis-v1"
const Operation = "analyze_contract"
const EngineOutputLimit = 64 << 20
const EngineExpandedLimit = 128 << 20
const ArtifactLimit = 32 << 20

type Budget struct {
	MaxContracts    int   `json:"max_contracts"`
	MaxTransactions int   `json:"max_transactions"`
	MaxStateReads   int   `json:"max_state_reads"`
	MaxRecords      int   `json:"max_records"`
	MaxRPCRequests  int64 `json:"max_rpc_requests"`
	MaxCredits      int64 `json:"max_credits"`
	TimeoutSeconds  int   `json:"timeout_seconds"`
}
type Request struct {
	Input             *SubmittedCode  `json:"input,omitempty"`
	Program           *ProgramRequest `json:"program,omitempty"`
	Chain             string          `json:"chain,omitempty"`
	Address           string          `json:"address,omitempty"`
	Block             json.RawMessage `json:"block,omitempty"`
	Timestamp         string          `json:"timestamp,omitempty"`
	FromBlock         *uint64         `json:"from_block,omitempty"`
	ToBlock           *uint64         `json:"to_block,omitempty"`
	TxHashes          []string        `json:"tx_hashes,omitempty"`
	TxHash            string          `json:"tx_hash,omitempty"`
	IncludeSource     *bool           `json:"include_source,omitempty"`
	Engines           []string        `json:"engines,omitempty"`
	Budget            Budget          `json:"budget"`
	IdempotencyKey    string          `json:"idempotency_key,omitempty"`
	AnalysisID        string          `json:"analysis_id,omitempty"`
	CompareAnalysisID string          `json:"compare_analysis_id,omitempty"`
	MethodID          string          `json:"method_id,omitempty"`
	EvidenceID        string          `json:"evidence_id,omitempty"`
	ArtifactID        string          `json:"artifact_id,omitempty"`
	Section           string          `json:"section,omitempty"`
	Search            string          `json:"search,omitempty"`
	Behavior          string          `json:"behavior,omitempty"`
	Cursor            string          `json:"cursor,omitempty"`
	PageSize          int             `json:"page_size,omitempty"`
	Offset            int64           `json:"offset,omitempty"`
	ChunkSize         int             `json:"chunk_size,omitempty"`
}

func Decode(raw []byte) (Request, error) {
	var q Request
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if e := d.Decode(&q); e != nil {
		return q, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return q, fmt.Errorf("expected one request object")
	}
	return q, nil
}
func (q *Request) Defaults() {
	if q.PageSize == 0 {
		q.PageSize = 100
	}
	if q.ChunkSize == 0 {
		q.ChunkSize = 1 << 20
	}
	b := &q.Budget
	if b.MaxContracts == 0 {
		b.MaxContracts = 20
	}
	if b.MaxTransactions == 0 {
		b.MaxTransactions = 20
	}
	if b.MaxStateReads == 0 {
		b.MaxStateReads = 128
	}
	if b.MaxRecords == 0 {
		b.MaxRecords = 5000
	}
	if b.MaxRPCRequests == 0 {
		b.MaxRPCRequests = 400
	}
	if b.MaxCredits == 0 {
		b.MaxCredits = 100000
	}
	if b.TimeoutSeconds == 0 {
		b.TimeoutSeconds = 1800
	}
	if q.IncludeSource == nil {
		v := true
		q.IncludeSource = &v
	}
	if len(q.Engines) == 0 {
		q.Engines = []string{"whatsabi", "sevm", "solc-ast", "heimdall", "slither", "gigahorse"}
	}
	sort.Strings(q.Engines)
}

var CompleteAddress = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
var CompleteHash = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
var Identifier = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (q Request) Validate() error {
	if q.Input != nil {
		if err := q.ValidateCodeInput(); err != nil {
			return err
		}
	}
	if q.PageSize < 1 || q.PageSize > 200 || len(q.Cursor) > 2048 {
		return fmt.Errorf("page_size must be 1-200")
	}
	if len(q.Search) > 200 || len(q.Behavior) > 100 || len(q.IdempotencyKey) > 128 {
		return fmt.Errorf("filter or idempotency key exceeds limit")
	}
	if q.Offset < 0 || q.ChunkSize < 1 || q.ChunkSize > 4<<20 {
		return fmt.Errorf("invalid artifact chunk")
	}
	for _, v := range []string{q.MethodID, q.EvidenceID, q.ArtifactID} {
		if v != "" && !Identifier.MatchString(v) {
			return fmt.Errorf("invalid analysis item ID")
		}
	}
	if q.Timestamp != "" && len(q.Block) > 0 {
		return fmt.Errorf("block and timestamp are mutually exclusive")
	}
	if q.Timestamp != "" {
		if _, e := time.Parse(time.RFC3339, q.Timestamp); e != nil {
			return fmt.Errorf("invalid timestamp")
		}
	}
	if (q.FromBlock == nil) != (q.ToBlock == nil) {
		return fmt.Errorf("from_block and to_block must be supplied together")
	}
	if q.FromBlock != nil && (*q.ToBlock < *q.FromBlock || *q.ToBlock-*q.FromBlock > 100000) {
		return fmt.Errorf("history range must span at most 100000 blocks")
	}
	b := q.Budget
	if b.MaxContracts < 1 || b.MaxContracts > 50 || b.MaxTransactions < 1 || b.MaxTransactions > 50 || b.MaxStateReads < 1 || b.MaxStateReads > 512 || b.MaxRecords < 1 || b.MaxRecords > 20000 || b.MaxRPCRequests < 10 || b.MaxRPCRequests > 2000 || b.MaxCredits < 1 || b.MaxCredits > 1000000 || b.TimeoutSeconds < 30 || b.TimeoutSeconds > 1800 {
		return fmt.Errorf("analysis budget exceeds supported bounds")
	}
	if len(q.TxHashes) > b.MaxTransactions {
		return fmt.Errorf("selected transactions exceed max_transactions")
	}
	for _, v := range append(append([]string{}, q.TxHashes...), q.TxHash) {
		if q.Program != nil {
			continue
		}
		if v != "" && !CompleteHash.MatchString(v) {
			return fmt.Errorf("complete EVM transaction hash required")
		}
	}
	seenTransactions := map[string]bool{}
	for _, tx := range q.TxHashes {
		key := strings.ToLower(tx)
		if seenTransactions[key] {
			return fmt.Errorf("duplicate selected transaction hash")
		}
		seenTransactions[key] = true
	}
	engines := StructuralEngines
	seen := map[string]bool{}
	for _, e := range q.Engines {
		if !engines[e] || seen[e] {
			return fmt.Errorf("unsupported or duplicate engine")
		}
		seen[e] = true
	}
	if !seen["sevm"] || !seen["whatsabi"] {
		return fmt.Errorf("sevm and whatsabi are required structural engines")
	}
	return nil
}

type Snapshot struct {
	IdentityKind        string               `json:"identity_kind,omitempty"`
	InputKind           string               `json:"input_kind,omitempty"`
	InputSHA256         string               `json:"input_sha256,omitempty"`
	CodePhase           string               `json:"code_phase,omitempty"`
	CodeIdentity        map[string]any       `json:"code_identity,omitempty"`
	DeclaredEnvironment *DeclaredEnvironment `json:"declared_environment,omitempty"`
	VMFamily            string               `json:"vm_family,omitempty"`
	Slot                *uint64              `json:"slot,omitempty"`
	ProgramLoader       string               `json:"program_loader,omitempty"`
	Chain               string               `json:"chain"`
	ChainID             string               `json:"chain_id"`
	BlockNumber         uint64               `json:"block_number"`
	BlockHash           string               `json:"block_hash"`
	BlockTime           time.Time            `json:"block_time"`
	EntryAddress        string               `json:"entry_address"`
	CodeAddress         string               `json:"code_address"`
	StorageAddress      string               `json:"storage_address"`
	CodeHash            string               `json:"code_hash"`
	CodeSHA256          string               `json:"code_sha256"`
	EVMRules            string               `json:"evm_rules"`
	EngineDigest        string               `json:"engine_digest"`
	StateGranularity    string               `json:"state_granularity"`
	FetchedAt           time.Time            `json:"fetched_at"`
	Proxy               map[string]any       `json:"proxy,omitempty"`
}
type Input struct {
	SubmittedInputJSON string          `json:"submitted_input_json,omitempty"`
	Program            json.RawMessage `json:"program,omitempty"`
	Operation          string          `json:"operation,omitempty"`
	Witness            json.RawMessage `json:"witness,omitempty"`
	Snapshot           Snapshot        `json:"snapshot"`
	Bytecode           string          `json:"bytecode"`
	Source             json.RawMessage `json:"source,omitempty"`
	Engines            []string        `json:"engines"`
}
type FrozenInput struct {
	Version       string            `json:"version"`
	Request       Request           `json:"request"`
	Snapshot      Snapshot          `json:"snapshot"`
	Contracts     []Input           `json:"contracts"`
	SourceObjects map[string]Object `json:"source_objects"`
	Frontier      []map[string]any  `json:"frontier"`
	Transactions  []json.RawMessage `json:"transactions"`
	Coverage      []map[string]any  `json:"coverage"`
}
type Object struct {
	Key    string `json:"object_key"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"byte_size"`
}
type Artifact struct {
	ContentBase64 string `json:"content_base64,omitempty"`
	ArtifactID    string `json:"artifact_id"`
	Kind          string `json:"kind"`
	Filename      string `json:"filename"`
	MediaType     string `json:"media_type"`
	SHA256        string `json:"sha256"`
	Bytes         int64  `json:"byte_size"`
	ObjectKey     string `json:"object_key,omitempty"`
	MethodID      string `json:"method_id,omitempty"`
	Text          string `json:"text,omitempty"`
}
type Result struct {
	Version     string                       `json:"version"`
	RuleVersion string                       `json:"rule_version"`
	Snapshot    Snapshot                     `json:"snapshot"`
	Sections    map[string][]json.RawMessage `json:"sections"`
	Metrics     map[string]any               `json:"metrics"`
}

func SHA256(b []byte) string    { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func ID(parts ...string) string { return SHA256([]byte(strings.Join(parts, "\x00")))[:40] }
func Row(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func ParseResult(raw []byte) (Result, error) {
	var r Result
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	e := d.Decode(&r)
	if e == nil && (r.Version != Version || r.Sections == nil) {
		e = fmt.Errorf("invalid engine protocol")
	}
	if rows, ok := r.Sections["engine_transport"]; e == nil && ok {
		if len(rows) != 1 {
			return r, fmt.Errorf("invalid engine transport")
		}
		var envelope struct {
			Encoding      string `json:"encoding"`
			Content       string `json:"content_base64"`
			Bytes         int64  `json:"byte_size"`
			SHA           string `json:"sha256"`
			ExpandedBytes int64  `json:"expanded_bytes"`
			ExpandedSHA   string `json:"expanded_sha256"`
		}
		if json.Unmarshal(rows[0], &envelope) != nil || envelope.Encoding != "engine-transport-gzip-v1" || envelope.Bytes < 1 || envelope.Bytes > ArtifactLimit || envelope.ExpandedBytes < 1 || envelope.ExpandedBytes > EngineExpandedLimit {
			return r, fmt.Errorf("engine transport exceeds supported bounds")
		}
		compressed, err := base64.StdEncoding.DecodeString(envelope.Content)
		if err != nil || int64(len(compressed)) != envelope.Bytes || SHA256(compressed) != envelope.SHA {
			return r, fmt.Errorf("engine transport compressed integrity failed")
		}
		reader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return r, err
		}
		expanded, err := io.ReadAll(io.LimitReader(reader, EngineExpandedLimit+1))
		closeErr := reader.Close()
		if err != nil || closeErr != nil || int64(len(expanded)) != envelope.ExpandedBytes || SHA256(expanded) != envelope.ExpandedSHA {
			return r, fmt.Errorf("engine transport expanded integrity failed")
		}
		var decoded Result
		decoder := json.NewDecoder(bytes.NewReader(expanded))
		decoder.UseNumber()
		if decoder.Decode(&decoded) != nil || decoded.Sections == nil || decoded.Version != r.Version || decoded.RuleVersion != r.RuleVersion || !bytes.Equal(Row(decoded.Snapshot), Row(r.Snapshot)) {
			return r, fmt.Errorf("engine transport identity mismatch")
		}
		if _, nested := decoded.Sections["engine_transport"]; nested {
			return r, fmt.Errorf("nested engine transport rejected")
		}
		return decoded, nil
	}
	return r, e
}
