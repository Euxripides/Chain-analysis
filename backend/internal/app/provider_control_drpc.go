package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/providercontrol"
	"github.com/blockchain-platform/blockchain-platform/internal/secret"
)

// providerControlDRPCResolver makes the provider center's saved switch and
// credentials the data plane's effective values. Existing managed secrets are
// used as defaults until an administrator changes them in the center.
type providerControlDRPCResolver struct {
	base    secret.Resolver
	control *providercontrol.Controller
	cfg     config.DRPCConfig
}

func (r providerControlDRPCResolver) Describe(ctx context.Context, ref string) secret.RefStatus {
	status := r.base.Describe(ctx, ref)
	if ref != r.cfg.RuntimeKeyRef && ref != r.cfg.StatisticsTokenRef && ref != r.cfg.KeysTokenRef {
		return status
	}
	value, err := r.Resolve(ctx, ref)
	status.Configured = err == nil && value != ""
	status.Prefix, status.Fingerprint = "", ""
	if status.Configured {
		runes := []rune(value)
		if len(runes) > 6 {
			runes = runes[:6]
		}
		status.Prefix = string(runes)
		sum := sha256.Sum256([]byte(value))
		status.Fingerprint = hex.EncodeToString(sum[:])[:16]
	}
	return status
}

func (r providerControlDRPCResolver) Resolve(ctx context.Context, ref string) (string, error) {
	if ref != r.cfg.RuntimeKeyRef && ref != r.cfg.StatisticsTokenRef && ref != r.cfg.KeysTokenRef {
		return r.base.Resolve(ctx, ref)
	}
	state, err := r.state(ctx)
	if err != nil {
		return "", err
	}
	switch ref {
	case r.cfg.RuntimeKeyRef:
		if !state.Enabled || strings.TrimSpace(state.RuntimeKey) == "" {
			return "", secret.ErrNotConfigured
		}
		return state.RuntimeKey, nil
	case r.cfg.StatisticsTokenRef:
		if strings.TrimSpace(state.StatisticsToken) == "" {
			return "", secret.ErrNotConfigured
		}
		return state.StatisticsToken, nil
	default:
		if strings.TrimSpace(state.KeysToken) == "" {
			return "", secret.ErrNotConfigured
		}
		return state.KeysToken, nil
	}
}

func (r providerControlDRPCResolver) state(ctx context.Context) (providercontrol.State, error) {
	defaults := providercontrol.State{Enabled: r.cfg.Enabled, Endpoints: map[string]string{
		"solana_rpc": strings.TrimRight(r.cfg.BaseURL, "/") + "/{key}",
	}}
	defaults.RuntimeKey, _ = r.base.Resolve(ctx, r.cfg.RuntimeKeyRef)
	defaults.StatisticsToken, _ = r.base.Resolve(ctx, r.cfg.StatisticsTokenRef)
	defaults.KeysToken, _ = r.base.Resolve(ctx, r.cfg.KeysTokenRef)
	return r.control.Resolve(ctx, "drpc", defaults)
}
