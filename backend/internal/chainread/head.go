package chainread

import (
	"context"

	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// HeadService 链头服务（§69-§71）。
//
// 1000 个地址的 Update Preview 只应产生极少量真实 eth_blockNumber：
// Gateway 的 1~3 秒缓存 + Singleflight 负责收敛（§48/§242）。
type HeadService struct {
	reader *Service
}

func NewHeadService(reader *Service) *HeadService { return &HeadService{reader: reader} }

// Head 返回链头与安全高度。SafeBlock 由 Chain Registry 策略决定（§24）。
func (h *HeadService) Head(ctx context.Context, chainSlug string) (HeadInfo, error) {
	opt := Options{Purpose: rpc.PurposeChainHead, Priority: rpc.PriorityRealtime}
	head, cacheHit, err := h.reader.BlockNumber(ctx, chainSlug, opt)
	if err != nil {
		return HeadInfo{}, err
	}
	info := HeadInfo{
		Chain: chainSlug, HeadBlock: head,
		SafeBlock: h.reader.chains.SafeHead(chainSlug, head),
		FetchedAt: nowUTC(), CacheHit: cacheHit,
	}
	return info, nil
}

// SafeHead 只要安全高度（Coverage/Update 用；禁止直接把 head 当目标，§24）。
func (h *HeadService) SafeHead(ctx context.Context, chainSlug string) (uint64, error) {
	info, err := h.Head(ctx, chainSlug)
	if err != nil {
		return 0, err
	}
	return info.SafeBlock, nil
}
