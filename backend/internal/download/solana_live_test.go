package download

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/drpc"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/route"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/blockchain-platform/blockchain-platform/internal/secret"
)

// Writes only real, finalized Solana records to the same ClickHouse tables as
// the worker. Explicitly opt in: this is a data-plane acceptance probe, not a
// unit test and not a full download-job completion assertion.
func TestLiveSolanaDecodeIngestAndReadback(t *testing.T) {
	if os.Getenv("SOLANA_INGEST_LIVE") != "1" {
		t.Skip("explicit live Solana ingest test only")
	}
	dsn := os.Getenv("CLICKHOUSE_URL")
	if dsn == "" {
		t.Fatal("CLICKHOUSE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ch, err := clickhouse.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	client := drpc.New(drpc.Options{Enabled: true, RPCHistory: true, Tier: drpc.TierPaid,
		BaseURL: "https://lb.drpc.live/solana", RuntimeKeyRef: "store://drpc/runtime_api_key",
		Secrets: secret.NewManager(secret.ManagerOptions{}), HistoryPageSize: 100,
		ModeResolver: func(context.Context) string { return drpc.ModeSafe }}, nil)
	chainHead, err := client.ChainHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fixtureSlot := uint64(451610692)
	if chainHead < fixtureSlot {
		t.Fatal("finalized head is behind the fixture slot")
	}
	routes := route.NewRegistry(slog.Default())
	routes.Register(route.ProviderDRPC, "dRPC Solana", []string{route.ChannelSolanaRPC}, client)
	routes.SetSolanaRoute(true, route.ProviderDRPC)
	sched := &Scheduler{cfg: &config.Config{}, routes: routes, log: slog.Default()}
	writer := ingest.NewWriter(ch, 100)
	const address = "5x3ENUr1P1DRXHqPLZ5LeGwccDXnNUxBR1UMCrq2aZ3e"
	var signature string
	var nativeFrom string
	for _, dataset := range []string{drpc.DatasetTransactions, drpc.DatasetTokenTransfers, drpc.DatasetInternalTransactions, drpc.DatasetLogs, drpc.DatasetBalances} {
		var rows []json.RawMessage
		cursor := ""
		for page := 0; page < 50; page++ {
			request := provider.FetchRequest{Chain: drpc.ChainSolana, Dataset: dataset, Address: address,
				FromBlock: &fixtureSlot, ToBlock: &fixtureSlot, PageSize: 100, PageToken: cursor,
				RequestID: "live-ingest-" + dataset}
			var result provider.FetchResult
			var fetchErr error
			for attempt := 0; attempt < 3; attempt++ {
				result, fetchErr = client.Fetch(ctx, request)
				if fetchErr == nil {
					break
				}
				select {
				case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if fetchErr != nil {
				t.Fatalf("%s page %d fetch: %v", dataset, page, fetchErr)
			}
			rows = append(rows, result.Rows...)
			if !result.HasMore {
				break
			}
			cursor = result.NextPageToken
			if page == 49 {
				t.Fatalf("%s did not reach fixture slot after 50 pages", dataset)
			}
		}
		batch, parseErr := sched.buildRows(ctx, dataset, rows, "solana", address, fixtureSlot)
		if parseErr != nil {
			t.Fatalf("%s normalize: %v", dataset, parseErr)
		}
		switch dataset {
		case drpc.DatasetTransactions:
			if len(batch.txs) == 0 || len(batch.txs) != len(batch.acts) {
				t.Fatalf("transaction row/activity mismatch: %d/%d", len(batch.txs), len(batch.acts))
			}
			signature = batch.txs[0].TxHash
			err = writer.WriteTransactions(ctx, batch.txs)
		case drpc.DatasetTokenTransfers:
			err = writer.WriteTokenTransfers(ctx, batch.transfers)
		case drpc.DatasetInternalTransactions:
			seen := map[uint32]bool{}
			for _, row := range batch.internals {
				if seen[row.CallIndex] {
					t.Fatalf("duplicate internal call index %d for %s", row.CallIndex, row.TxHash)
				}
				seen[row.CallIndex] = true
				if row.AssetType == "SOL" && row.ValueRaw != "0" {
					nativeFrom = row.From
					break
				}
			}
			err = writer.WriteInternalTransfers(ctx, batch.internals)
		case drpc.DatasetLogs:
			err = writer.WriteLogs(ctx, batch.logRows)
			if err == nil {
				err = writer.WriteSolanaProgramLogs(ctx, batch.solanaLogs)
			}
		case drpc.DatasetBalances:
			err = writer.WriteBalances(ctx, batch.bals)
		}
		if err != nil {
			t.Fatalf("%s ingest: %v", dataset, err)
		}
		if len(batch.acts) > 0 {
			if err := writer.WriteActivity(ctx, batch.acts); err != nil {
				t.Fatalf("%s activity ingest: %v", dataset, err)
			}
		}
		t.Logf("dataset=%s rows=%d activity=%d", dataset, len(rows), len(batch.acts))
	}
	q := query.New(ch)
	tx, err := q.ActionTransactionRecord(ctx, "solana", signature)
	if err != nil || tx == nil {
		t.Fatalf("transaction readback: found=%t err=%v", tx != nil, err)
	}
	page, err := q.ActionRows(ctx, "transactions", query.ActionFilter{Chain: "solana", Address: address, Page: 1, PageSize: 10})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("signature activity readback: rows=%d err=%v", len(page.Items), err)
	}
	if _, _, err := q.ActionAssets(ctx, "solana", address); err != nil {
		t.Fatalf("assets readback: %v", err)
	}
	if nativeFrom != "" {
		flow, err := q.ActionFlowSummary(ctx, "solana", nativeFrom, "SOL", "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("native source=%s outgoing=%s", nativeFrom, flow.TotalOut)
	}
}
