package tokenmeta

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RegistryToken is a deliberately small, reviewed allow-list. A symbol match
// never grants verification; chain and full contract address must both match.
type RegistryToken struct {
	Chain, Contract, Name, Symbol, TokenType, LogoURL, SourceRef string
	Decimals                                                     int
}

var verifiedRegistry = []RegistryToken{
	{
		Chain: "bsc", Contract: "0x55d398326f99059ff775485246999027b3197955",
		Name: "Tether USD", Symbol: "USDT", Decimals: 18, TokenType: "BEP20",
		LogoURL:   "https://raw.githubusercontent.com/trustwallet/assets/master/blockchains/smartchain/assets/0x55d398326f99059fF775485246999027B3197955/logo.png",
		SourceRef: "https://github.com/trustwallet/assets/blob/master/blockchains/smartchain/tokenlist.json",
	},
}

// EnsureVerifiedRegistry persists reviewed registry assertions as observations
// and recomputes the affected contracts. It is idempotent across restarts.
func (s *Service) EnsureVerifiedRegistry(ctx context.Context) error {
	for _, token := range verifiedRegistry {
		contract := normalizeAddress(token.Contract)
		rawHash := fmt.Sprintf("verified-registry:v1:%s:%s", token.Chain, contract)
		_, err := s.Store.Pool.Exec(ctx, `INSERT INTO token_metadata_observations
			(id,chain,contract_address,normalized_address,name,symbol,decimals,token_type,logo_url,source,source_ref,observed_at,raw_hash,valid)
			VALUES($1,$2,$3,$3,$4,$5,$6,$7,$8,'verified_registry',$9,$10,$11,true)
			ON CONFLICT(chain,normalized_address,source,raw_hash) DO UPDATE SET name=EXCLUDED.name,symbol=EXCLUDED.symbol,decimals=EXCLUDED.decimals,token_type=EXCLUDED.token_type,logo_url=EXCLUDED.logo_url,source_ref=EXCLUDED.source_ref,observed_at=EXCLUDED.observed_at,valid=true,validation_error=NULL`,
			uuid.NewString(), strings.ToLower(token.Chain), contract, token.Name, token.Symbol, token.Decimals, token.TokenType, token.LogoURL, token.SourceRef, time.Now().UTC(), rawHash)
		if err != nil {
			return err
		}
		if err := s.Recompute(ctx, token.Chain, contract); err != nil {
			return err
		}
	}
	return nil
}
