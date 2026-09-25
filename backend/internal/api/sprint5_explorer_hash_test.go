package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
)

/*
交易详情的 hash 形状契约（回归：用户端「一直报错 · 交易哈希不合法」）。

背景：用户端 /transactions/{chain}/{hash} 的 URL 是**去 0x 前缀**的
（frontend/apps/user-console/src/pages/ExplorerPage.tsx 的
`value.replace(/^0x/i, "")`，并由 explorer-visual-acceptance.mjs 冻结），
而详情页把该路由参数原样拼进 /api/v2/explorer/transactions/{chain}/{hash}。
因此服务端必须同时接受两种书写：0x + 64 hex 与裸 64 hex。
*/

const (
	txBareHash    = "e8c8df75c395c4e468cfc9ae9ed58e5f321b0805c8496414a9af7f035f71d400"
	txPrefixedURI = "0x" + txBareHash
)

// explorerTxServer 复刻生产的链校验：只有注册表里的链能通过。
func explorerTxServer() *Server {
	return &Server{svc: &dataset.Service{ValidateChain: func(chain string) error {
		switch chain {
		case "tron", "bsc", "eth", "polygon", "arbitrum":
			return nil
		default:
			return fmt.Errorf("chain %q is not registered", chain)
		}
	}}}
}

func resolveTx(t *testing.T, server *Server, chainSlug, hash string) (string, string, int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/explorer/transactions/"+chainSlug+"/"+hash, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("chain", chainSlug)
	rctx.URLParams.Add("hash", hash)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	gotChain, gotHash, ok := server.resolveExplorerTx(rec, req)
	if !ok {
		return "", "", rec.Code
	}
	return gotChain, gotHash, rec.Code
}

func TestResolveExplorerTxAcceptsBareHash(t *testing.T) {
	server := explorerTxServer()
	// 每条链都必须接受裸 64 hex：URL 契约不区分链，服务端不能只对 TRON 放行。
	for _, slug := range []string{"tron", "bsc", "eth", "polygon", "arbitrum"} {
		chainSlug, hash, code := resolveTx(t, server, slug, txBareHash)
		if code != http.StatusOK {
			t.Fatalf("[%s] bare hash rejected with status %d（用户端详情页 URL 就是去 0x 形式）", slug, code)
		}
		if chainSlug != slug {
			t.Fatalf("[%s] chain = %q", slug, chainSlug)
		}
		if hash != txPrefixedURI {
			t.Fatalf("[%s] hash = %q, want %q（下游按带 0x 的库内形式查询）", slug, hash, txPrefixedURI)
		}
	}
}

func TestResolveExplorerTxNormalizesPrefixedHash(t *testing.T) {
	// 大写 hex 必须归一化成小写；大小写不同的同一笔交易不能在库内找不到。
	upper := "0xE8C8DF75C395C4E468CFC9AE9ED58E5F321B0805C8496414A9AF7F035F71D400"
	_, hash, code := resolveTx(t, explorerTxServer(), "bsc", upper)
	if code != http.StatusOK {
		t.Fatalf("prefixed hash rejected with status %d", code)
	}
	if hash != txPrefixedURI {
		t.Fatalf("hash = %q, want %q", hash, txPrefixedURI)
	}
}

func TestResolveExplorerTxRejectsMalformedHash(t *testing.T) {
	server := explorerTxServer()
	cases := map[string]string{
		"one char too long": txBareHash + "a",
		"truncated 62 hex":  txBareHash[:62],
		"not hex":           "zzc8df75c395c4e468cfc9ae9ed58e5f321b0805c8496414a9af7f035f71d4",
		"address":           "0x0000000000000000000000000000000000000000",
		"empty":             "",
		"only prefix":       "0x",
	}
	for name, value := range cases {
		if _, _, code := resolveTx(t, server, "bsc", value); code != http.StatusBadRequest {
			t.Fatalf("[%s] status = %d, want 400（非法 hash 仍必须拒绝）", name, code)
		}
	}
}

func TestResolveExplorerTxRejectsUnknownChain(t *testing.T) {
	if _, _, code := resolveTx(t, explorerTxServer(), "not-a-chain", txPrefixedURI); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}
