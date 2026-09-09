// V1.3 Deployment 矩阵：chain 名称 ↔ EVM chain_id + Chainbase 可 Count 的 (chain,dataset)。
// 来源：chain_capabilities（部署时静态读取一次）；测试可注入 fake。
package countoracle

import (
	"context"

	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

type deployment struct {
	chainIDByChain map[string]uint64
	chainByID      map[uint64]string
	countable      map[string]bool // chain|dataset → chain_capabilities.chainbase_*_count && enabled
	order          []string        // 注册顺序（Runtime Profile 行展开用）
}

func (d *deployment) chainID(chain string) uint64 {
	if d == nil {
		return 0
	}
	return d.chainIDByChain[chain]
}

func (d *deployment) chainName(id uint64) string {
	if d == nil {
		return ""
	}
	return d.chainByID[id]
}

func (d *deployment) chainbaseCountable(ctx context.Context, chainID uint64, dataset string) bool {
	if d == nil {
		return false
	}
	name := d.chainByID[chainID]
	return name != "" && d.countable[name+"|"+dataset]
}

// buildDeployment 从 chain_capabilities 构建部署矩阵（失败返回空 deployment，不阻断启动）。
func buildDeployment(ctx context.Context, st *store.Store) *deployment {
	d := &deployment{
		chainIDByChain: map[string]uint64{},
		chainByID:      map[uint64]string{},
		countable:      map[string]bool{},
	}
	if st == nil {
		return d
	}
	caps, err := st.ListChainCapabilities(ctx)
	if err != nil || len(caps) == 0 {
		return d
	}
	for _, c := range caps {
		if !c.Enabled {
			continue
		}
		if c.EVMChainID == nil || *c.EVMChainID <= 0 {
			continue
		}
		id := uint64(*c.EVMChainID)
		if _, ok := d.chainIDByChain[c.Chain]; !ok {
			d.chainIDByChain[c.Chain] = id
			d.chainByID[id] = c.Chain
			d.order = append(d.order, c.Chain)
		}
		if c.ChainbaseTxCount {
			d.countable[c.Chain+"|transactions"] = true
		}
		if c.ChainbaseTokenCount {
			d.countable[c.Chain+"|token_transfers"] = true
		}
	}
	return d
}

// setDeployment 注入部署矩阵（worker wiring / 测试）。
func (s *Service) setDeployment(d *deployment) { s.deploy = d }

// SetDeployment 从 chain_capabilities 构建部署矩阵（Worker wiring 用；测试可注入 fake）。
func (s *Service) SetDeployment(ctx context.Context, st *store.Store) {
	s.setDeployment(buildDeployment(ctx, st))
}
