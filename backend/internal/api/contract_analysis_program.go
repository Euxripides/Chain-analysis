package api

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/solana"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
)

const upgradeableLoader = "BPFLoaderUpgradeab1e11111111111111111111111"

func (s *Server) handleAnalyzeProgram(w http.ResponseWriter, r *http.Request, session bool) {
	ctx, scope, err := s.contractRequestContext(r, session)
	if err != nil {
		investigationError(w, err)
		return
	}
	if os.Getenv("CONTRACT_PROGRAM_ANALYSIS_ENABLED") != "1" {
		investigationError(w, summaryError(503, "PROGRAM_ANALYSIS_DISABLED", "Program analysis is not enabled in this EVM release"))
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		investigationError(w, fmt.Errorf("invalid program request"))
		return
	}
	p, err := ca.DecodeProgram(raw)
	if err == nil {
		err = p.Normalize()
	}
	q := ca.Request{Program: &p, Address: p.Address, TxHashes: p.TxHashes, Budget: p.Budget, IdempotencyKey: p.IdempotencyKey}
	if p.VMFamily == "tron_tvm" {
		q.Chain = "tron"
	} else {
		q.Chain = "solana"
	}
	q.Defaults()
	if err == nil {
		err = q.Validate()
	}
	if err != nil {
		investigationError(w, err)
		return
	}
	s.handleContractPrepared(w, r, "analyze_program", session, ctx, scope, q)
}

type programAccount struct {
	Data       []json.RawMessage `json:"data"`
	Executable bool              `json:"executable"`
	Owner      string            `json:"owner"`
	Lamports   json.Number       `json:"lamports"`
}

func accountBytes(a programAccount) ([]byte, error) {
	if len(a.Data) != 2 {
		return nil, fmt.Errorf("program account encoding unavailable")
	}
	var encoded, encoding string
	if json.Unmarshal(a.Data[0], &encoded) != nil || json.Unmarshal(a.Data[1], &encoding) != nil || encoding != "base64" {
		return nil, fmt.Errorf("program account must use base64")
	}
	b, err := base64.StdEncoding.DecodeString(encoded)
	if err == nil && len(b) > 16<<20 {
		err = fmt.Errorf("program exceeds byte limit")
	}
	return b, err
}

func (s *Server) programBlockGuard(ctx context.Context, q ca.Request, point chainread.StatePoint) error {
	if q.Program.VMFamily == "tron_tvm" {
		return s.contractBlockGuard(ctx, q, point, true)
	}
	ctx = context.WithValue(ctx, contractFinalGuardKey{}, true)
	raw, err := s.deps.DRPC.ReadProgramRPC(ctx, "getBlock", []any{point.Block, map[string]any{"commitment": "finalized", "transactionDetails": "none", "rewards": false, "maxSupportedTransactionVersion": 1}})
	if err != nil {
		return err
	}
	var block struct {
		Hash string `json:"blockhash"`
	}
	if nextDecode(raw, &block) != nil || block.Hash != point.Hash {
		return summaryError(409, "SNAPSHOT_REORG", "Observed program slot identity changed")
	}
	return nil
}

func (s *Server) pinProgram(ctx context.Context, q ca.Request, engine string) (chainread.StatePoint, ca.Object, error) {
	var point chainread.StatePoint
	var input ca.Input
	var err error
	if q.Program.VMFamily == "tron_tvm" {
		block, e := s.deps.ChainRead.Block(ctx, "tron", chain.Latest(), false, contractOptions(ctx, q))
		if e != nil || block == nil {
			if e == nil {
				e = fmt.Errorf("TRON snapshot block unavailable")
			}
			err = e
			return point, ca.Object{}, err
		}
		point = chainread.StatePoint{Block: block.Number, Hash: block.Hash, Time: time.Unix(int64(block.Timestamp), 0).UTC(), Ref: chain.AtBlock(block.Number)}
		rpcAddress, e := tron.Base58TToEVMHex20(q.Address)
		if e != nil {
			return point, ca.Object{}, e
		}
		// java-tron code reads support latest only. The block is an observed
		// reference; it is never presented as historical code/state proof.
		opt := contractOptions(ctx, q)
		code, e := s.deps.ChainRead.Code(ctx, "tron", chain.Address(rpcAddress), chain.Latest(), opt)
		if e != nil {
			return point, ca.Object{}, e
		}
		if len(code) == 0 || len(code) > 49152 {
			return point, ca.Object{}, fmt.Errorf("TRON runtime code unavailable or over budget")
		}
		input = ca.Input{Snapshot: ca.Snapshot{VMFamily: "tron_tvm", Chain: "tron", ChainID: fmt.Sprint(mustChain(s, "tron").ChainID), BlockNumber: point.Block, BlockHash: point.Hash, BlockTime: point.Time,
			EntryAddress: q.Address, CodeAddress: q.Address, StorageAddress: q.Address, CodeHash: contractKeccak(code), CodeSHA256: ca.SHA256(code), EngineDigest: engine,
			EVMRules: "tvm_rules_not_verified", StateGranularity: "observed_latest_code_reference_block_not_atomic_state", FetchedAt: time.Now().UTC()}, Bytecode: "0x" + hex.EncodeToString(code)}
		input.Operation = "program"
		transactions := []json.RawMessage{}
		reader, available := s.deps.Prov.(bridgeTronReader)
		if len(q.TxHashes) > 0 && !available {
			return point, ca.Object{}, fmt.Errorf("TRON native transaction reader unavailable")
		}
		for _, txHash := range q.TxHashes {
			transaction, e := reader.TxRecord(ctx, "tron", txHash)
			if e != nil {
				return point, ca.Object{}, e
			}
			receipt, e := reader.TxReceipt(ctx, "tron", txHash)
			if e != nil {
				return point, ca.Object{}, e
			}
			var identity struct {
				ID    string `json:"id"`
				Block uint64 `json:"blockNumber"`
			}
			if nextDecode(receipt, &identity) != nil || identity.ID != txHash || identity.Block > point.Block {
				return point, ca.Object{}, fmt.Errorf("TRON receipt identity outside pinned snapshot")
			}
			transactions = append(transactions, ca.Row(map[string]any{"tx_hash": txHash, "transaction": transaction, "receipt": receipt}))
		}
		input.Program = ca.Row(map[string]any{"vm_family": "tron_tvm", "rpc_address": rpcAddress, "transactions": transactions})
	} else {
		if s.deps.DRPC == nil {
			return point, ca.Object{}, summaryError(503, "DRPC_SOLANA_NOT_CONFIGURED", "Solana program provider unavailable")
		}
		read := func(method string, params []any) (json.RawMessage, error) {
			return s.deps.DRPC.ReadProgramRPC(ctx, method, params)
		}
		options := map[string]any{"encoding": "base64", "commitment": "finalized"}
		first, e := read("getAccountInfo", []any{q.Address, options})
		if e != nil {
			return point, ca.Object{}, e
		}
		var probe struct {
			Context struct {
				Slot uint64 `json:"slot"`
			} `json:"context"`
			Value *programAccount `json:"value"`
		}
		if nextDecode(first, &probe) != nil || probe.Value == nil || !probe.Value.Executable {
			return point, ca.Object{}, fmt.Errorf("address is not an executable Solana program")
		}
		bytes, e := accountBytes(*probe.Value)
		if e != nil {
			return point, ca.Object{}, e
		}
		accounts := first
		programData := ""
		var loaderState any
		slot, loader := probe.Context.Slot, probe.Value.Owner
		if loader == upgradeableLoader {
			if len(bytes) != 36 || binary.LittleEndian.Uint32(bytes[:4]) != 2 {
				return point, ca.Object{}, fmt.Errorf("invalid upgradeable program account")
			}
			programData = solana.EncodeBase58(bytes[4:36])
			options["minContextSlot"] = slot
			accounts, e = read("getMultipleAccounts", []any{[]string{q.Address, programData}, options})
			if e != nil {
				return point, ca.Object{}, e
			}
			var atomic struct {
				Context struct {
					Slot uint64 `json:"slot"`
				} `json:"context"`
				Value []*programAccount `json:"value"`
			}
			if nextDecode(accounts, &atomic) != nil || atomic.Context.Slot < slot || len(atomic.Value) != 2 || atomic.Value[0] == nil || atomic.Value[1] == nil || !atomic.Value[0].Executable || atomic.Value[1].Executable || atomic.Value[0].Owner != loader || atomic.Value[1].Owner != loader {
				return point, ca.Object{}, fmt.Errorf("program accounts unavailable in one finalized snapshot")
			}
			current, e := accountBytes(*atomic.Value[0])
			if e != nil || len(current) != 36 || binary.LittleEndian.Uint32(current[:4]) != 2 || solana.EncodeBase58(current[4:]) != programData {
				return point, ca.Object{}, fmt.Errorf("program changed while pinning code")
			}
			data, e := accountBytes(*atomic.Value[1])
			if e != nil || len(data) < 45 || binary.LittleEndian.Uint32(data[:4]) != 3 {
				return point, ca.Object{}, fmt.Errorf("invalid programdata account")
			}
			var authority any
			if data[12] == 1 {
				authority = solana.EncodeBase58(data[13:45])
			} else if data[12] != 0 {
				return point, ca.Object{}, fmt.Errorf("invalid program upgrade authority encoding")
			}
			loaderState = map[string]any{"programdata_address": programData, "last_deployed_slot": binary.LittleEndian.Uint64(data[4:12]), "upgrade_authority": authority, "basis": "observed_finalized_upgradeable_loader_programdata"}
			bytes, slot = data[45:], atomic.Context.Slot
		} else if loader != "BPFLoader1111111111111111111111111111111111" && loader != "BPFLoader2111111111111111111111111111111111" {
			return point, ca.Object{}, fmt.Errorf("unsupported Solana program loader")
		}
		if len(bytes) < 4 || string(bytes[:4]) != "\x7fELF" {
			return point, ca.Object{}, fmt.Errorf("unsupported Solana program loader or ELF")
		}
		genesis, e := read("getGenesisHash", []any{})
		if e != nil {
			return point, ca.Object{}, e
		}
		var genesisHash string
		if json.Unmarshal(genesis, &genesisHash) != nil || genesisHash == "" {
			return point, ca.Object{}, fmt.Errorf("Solana network identity unavailable")
		}
		block, e := read("getBlock", []any{slot, map[string]any{"commitment": "finalized", "transactionDetails": "none", "rewards": false, "maxSupportedTransactionVersion": 1}})
		if e != nil {
			return point, ca.Object{}, e
		}
		var observed struct {
			Blockhash string `json:"blockhash"`
			BlockTime *int64 `json:"blockTime"`
		}
		if nextDecode(block, &observed) != nil || observed.Blockhash == "" || observed.BlockTime == nil {
			return point, ca.Object{}, fmt.Errorf("finalized program slot identity unavailable")
		}
		point = chainread.StatePoint{Block: slot, Hash: observed.Blockhash, Time: time.Unix(*observed.BlockTime, 0).UTC()}
		if programData == "" {
			programData = q.Address
		}
		snapshot := ca.Snapshot{VMFamily: "solana_sbf", Chain: "solana", ChainID: genesisHash, Slot: &slot, BlockNumber: slot,
			BlockHash: point.Hash, BlockTime: point.Time, EntryAddress: q.Address, CodeAddress: q.Address, StorageAddress: programData,
			CodeHash: "0x" + ca.SHA256(bytes), CodeSHA256: ca.SHA256(bytes), ProgramLoader: loader, EngineDigest: engine,
			StateGranularity: "observed_finalized_program_accounts", FetchedAt: time.Now().UTC()}
		transactions := []json.RawMessage{}
		for _, signature := range q.TxHashes {
			raw, e := read("getTransaction", []any{signature, map[string]any{"encoding": "json", "commitment": "finalized", "maxSupportedTransactionVersion": 1}})
			if e != nil || string(raw) == "null" {
				return point, ca.Object{}, fmt.Errorf("selected Solana transaction unavailable")
			}
			transactions = append(transactions, ca.Row(map[string]any{"signature": signature, "record": json.RawMessage(raw)}))
		}
		input = ca.Input{Operation: "program", Snapshot: snapshot, Engines: []string{"solana-sbpf"}, Program: ca.Row(map[string]any{
			"vm_family": "solana_sbf", "elf_base64": base64.StdEncoding.EncodeToString(bytes), "program_accounts": json.RawMessage(accounts),
			"transactions": transactions, "selected_code_basis": "observed_finalized_accounts", "transaction_prestate_code_verified": false,
			"loader_state": loaderState,
			"simulation":   q.Program.Simulation,
		})}
	}
	object, err := s.contractSaveObject(ctx, "program-input", ca.Row(input), "application/json")
	return point, object, err
}
