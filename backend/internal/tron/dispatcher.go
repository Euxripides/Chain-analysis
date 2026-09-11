package tron

import (
	"context"
	"encoding/json"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// Dispatcher is the single download-entry split. EVM traffic is passed through
// unchanged; only chain=tron is routed to TronGrid.
type Dispatcher struct {
	EVM  provider.Provider
	Tron provider.Provider
}

// DatasetRouter keeps the TRON provider decision at dataset granularity. It
// deliberately implements provider.Provider so the existing Scheduler and
// EVM path remain unchanged.
type DatasetRouter struct {
	Default          provider.Provider
	Transactions     provider.Provider
	TRC20            provider.Provider
	Internal         provider.Provider
	ReceiptProvider  provider.Provider
	TxDetailProvider provider.Provider
}

func (r *DatasetRouter) Name() string { return "tron-dataset-router" }

func (r *DatasetRouter) providerFor(dataset string) provider.Provider {
	var selected provider.Provider
	switch dataset {
	case "transactions":
		selected = r.Transactions
	case "token_transfers":
		selected = r.TRC20
	case "internal_transactions":
		selected = r.Internal
	}
	if selected == nil {
		selected = r.Default
	}
	return selected
}

func (r *DatasetRouter) providerForCapability(capability string) provider.Provider {
	switch capability {
	case "receipt":
		if r.ReceiptProvider != nil {
			return r.ReceiptProvider
		}
	case "tx_detail":
		if r.TxDetailProvider != nil {
			return r.TxDetailProvider
		}
	}
	return r.Default
}

func (r *DatasetRouter) Capabilities() provider.Capabilities {
	sets := map[string]bool{}
	for _, p := range []provider.Provider{r.Transactions, r.TRC20, r.Internal, r.Default} {
		if p == nil {
			continue
		}
		for _, dataset := range p.Capabilities().Datasets {
			sets[dataset] = true
		}
	}
	datasets := make([]string, 0, len(sets))
	for _, dataset := range []string{"transactions", "token_transfers", "internal_transactions", "nft_transfers", "logs", "balances"} {
		if sets[dataset] {
			datasets = append(datasets, dataset)
		}
	}
	return provider.Capabilities{Name: r.Name(), Datasets: datasets, MaxPageSize: 200,
		SupportsTimeRange: map[string]bool{"transactions": true, "token_transfers": true, "internal_transactions": true}}
}

func (r *DatasetRouter) Health(ctx context.Context) provider.HealthStatus {
	if r.Default != nil {
		return r.Default.Health(ctx)
	}
	if p := r.providerFor("transactions"); p != nil {
		return p.Health(ctx)
	}
	return provider.HealthStatus{Provider: r.Name(), State: provider.HealthOpen, Error: "no TRON provider configured"}
}

func (r *DatasetRouter) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	if req.Chain != "tron" {
		return provider.FetchResult{}, &provider.Error{Provider: r.Name(), Class: provider.ClassUnsupported, Message: "TRON dataset router only supports chain=tron"}
	}
	p := r.providerFor(req.Dataset)
	if p == nil {
		return provider.FetchResult{}, &provider.Error{Provider: r.Name(), Class: provider.ClassUnsupported, Message: "no provider configured for TRON dataset: " + req.Dataset}
	}
	return p.Fetch(ctx, req)
}

func (r *DatasetRouter) TRC20ActivityBounds(ctx context.Context, address string) (time.Time, time.Time, error) {
	p := r.providerFor("token_transfers")
	if resolver, ok := p.(interface {
		TRC20ActivityBounds(context.Context, string) (time.Time, time.Time, error)
	}); ok {
		return resolver.TRC20ActivityBounds(ctx, address)
	}
	return time.Time{}, time.Time{}, &provider.Error{Provider: r.Name(), Class: provider.ClassUnsupported, Message: "TRC20 activity bounds unsupported"}
}

func (r *DatasetRouter) TRC20ActivityProfile(ctx context.Context, address string) (provider.TRC20ActivityProfile, error) {
	p := r.providerFor("token_transfers")
	if resolver, ok := p.(interface {
		TRC20ActivityProfile(context.Context, string) (provider.TRC20ActivityProfile, error)
	}); ok {
		return resolver.TRC20ActivityProfile(ctx, address)
	}
	return provider.TRC20ActivityProfile{}, &provider.Error{Provider: r.Name(), Class: provider.ClassUnsupported, Message: "TRC20 activity profile unsupported"}
}

func (r *DatasetRouter) TxDetail(ctx context.Context, chain, txHash string, decodeTxData, decodeLogs bool) (json.RawMessage, error) {
	p := r.providerForCapability("tx_detail")
	if f, ok := p.(provider.TxDetailFetcher); ok {
		return f.TxDetail(ctx, chain, txHash, decodeTxData, decodeLogs)
	}
	return nil, &provider.Error{Provider: r.Name(), Class: provider.ClassUnsupported, Message: "transaction detail unsupported"}
}

func (r *DatasetRouter) TxReceipt(ctx context.Context, chain, txHash string) (json.RawMessage, error) {
	p := r.providerForCapability("receipt")
	if f, ok := p.(provider.TxReceiptFetcher); ok {
		return f.TxReceipt(ctx, chain, txHash)
	}
	return nil, &provider.Error{Provider: r.Name(), Class: provider.ClassUnsupported, Message: "transaction receipt unsupported"}
}

func (r *DatasetRouter) ResolveAddressTypes(ctx context.Context, chain string, addresses []string) (map[string]bool, error) {
	p := r.Default
	if p == nil {
		p = r.Transactions
	}
	if f, ok := p.(provider.AddressTypeResolver); ok {
		return f.ResolveAddressTypes(ctx, chain, addresses)
	}
	return map[string]bool{}, nil
}

func (r *DatasetRouter) CurrentBalances(ctx context.Context, chain, address string) ([]provider.CurrentBalance, error) {
	for _, p := range []provider.Provider{r.Transactions, r.Default, r.TRC20} {
		if resolver, ok := p.(provider.CurrentBalanceResolver); ok {
			return resolver.CurrentBalances(ctx, chain, address)
		}
	}
	return nil, &provider.Error{Provider: r.Name(), Class: provider.ClassUnsupported, Message: "current balance lookup unsupported"}
}

func (d *Dispatcher) Name() string                                     { return d.EVM.Name() }
func (d *Dispatcher) Capabilities() provider.Capabilities              { return d.EVM.Capabilities() }
func (d *Dispatcher) Health(ctx context.Context) provider.HealthStatus { return d.EVM.Health(ctx) }
func (d *Dispatcher) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	if req.Chain == "tron" && d.Tron != nil {
		return d.Tron.Fetch(ctx, req)
	}
	return d.EVM.Fetch(ctx, req)
}
func (d *Dispatcher) TRC20ActivityBounds(ctx context.Context, address string) (time.Time, time.Time, error) {
	if resolver, ok := d.Tron.(interface {
		TRC20ActivityBounds(context.Context, string) (time.Time, time.Time, error)
	}); ok {
		return resolver.TRC20ActivityBounds(ctx, address)
	}
	return time.Time{}, time.Time{}, &provider.Error{Provider: d.Name(), Class: provider.ClassUnsupported, Message: "TRC20 activity bounds unsupported"}
}
func (d *Dispatcher) TRC20ActivityProfile(ctx context.Context, address string) (provider.TRC20ActivityProfile, error) {
	if resolver, ok := d.Tron.(interface {
		TRC20ActivityProfile(context.Context, string) (provider.TRC20ActivityProfile, error)
	}); ok {
		return resolver.TRC20ActivityProfile(ctx, address)
	}
	return provider.TRC20ActivityProfile{}, &provider.Error{Provider: d.Name(), Class: provider.ClassUnsupported, Message: "TRC20 activity profile unsupported"}
}
func (d *Dispatcher) TxDetail(ctx context.Context, chain, txHash string, decodeTxData, decodeLogs bool) (json.RawMessage, error) {
	if chain == "tron" && d.Tron != nil {
		if f, ok := d.Tron.(provider.TxDetailFetcher); ok {
			return f.TxDetail(ctx, chain, txHash, decodeTxData, decodeLogs)
		}
	}
	f, ok := d.EVM.(provider.TxDetailFetcher)
	if !ok {
		return nil, &provider.Error{Provider: d.EVM.Name(), Class: provider.ClassUnsupported, Message: "transaction detail unsupported"}
	}
	return f.TxDetail(ctx, chain, txHash, decodeTxData, decodeLogs)
}
func (d *Dispatcher) TxReceipt(ctx context.Context, chain, txHash string) (json.RawMessage, error) {
	if chain == "tron" && d.Tron != nil {
		if f, ok := d.Tron.(provider.TxReceiptFetcher); ok {
			return f.TxReceipt(ctx, chain, txHash)
		}
	}
	return nil, &provider.Error{Provider: d.Name(), Class: provider.ClassUnsupported, Message: "transaction receipt unsupported"}
}
func (d *Dispatcher) ResolveAddressTypes(ctx context.Context, chain string, addresses []string) (map[string]bool, error) {
	if chain == "tron" && d.Tron != nil {
		if f, ok := d.Tron.(provider.AddressTypeResolver); ok {
			return f.ResolveAddressTypes(ctx, chain, addresses)
		}
		return map[string]bool{}, nil
	}
	f, ok := d.EVM.(provider.AddressTypeResolver)
	if !ok {
		return map[string]bool{}, nil
	}
	return f.ResolveAddressTypes(ctx, chain, addresses)
}

func (d *Dispatcher) CurrentBalances(ctx context.Context, chain, address string) ([]provider.CurrentBalance, error) {
	if chain == "tron" && d.Tron != nil {
		if resolver, ok := d.Tron.(provider.CurrentBalanceResolver); ok {
			return resolver.CurrentBalances(ctx, chain, address)
		}
	}
	if resolver, ok := d.EVM.(provider.CurrentBalanceResolver); ok {
		return resolver.CurrentBalances(ctx, chain, address)
	}
	return nil, &provider.Error{Provider: d.Name(), Class: provider.ClassUnsupported, Message: "current balance lookup unsupported"}
}
