// tokenindex 把外部代币目录导入平台库（幂等，可重复执行）。
//
// 两个来源、三张目录表，都不直接改 token_metadata：
//
//	-trustwallet <dir>   Trust Wallet assets 全量解压目录 → token_logo_assets
//	                     （info.json 提供名称/符号/类型，logo.png 记录 sha256 与字节数）
//	-coingecko <file>    CoinGecko /coins/list?include_platform=true 的 JSON
//	                     → coingecko_coins + coingecko_platforms（CoinGecko ID ↔ 链 ↔ 合约）
//	-coingecko-url      直接抓取上面的接口（默认地址，可覆盖）
//	-resolve-logos      把 token_logo_assets.logo_url 回填到缺图标的 token_metadata 行，
//	                    交给 internal/tokenmeta 的 logo worker 自托管抓取
//
// 为什么先落目录再回填：图标与跨源映射是**外部目录事实**，与链上事实分开存，
// 回填任务只读目录表，不必每次重新下载上游；同一次导入也能服务后续的价格/映射需求。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/logger"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

const (
	sourceTrustWallet = "trustwallet"
	// sourceCoinGecko：CoinGecko 聚合 tokenlist 快照（无数字 ID，见 cgTokenList 注释）。
	sourceCoinGecko = "coingecko-tokenlist"
	defaultCGURL    = "https://api.coingecko.com/api/v3/coins/list?include_platform=true"
)

// trustWalletChains：Trust Wallet assets 的链目录名 → 平台链 slug。
var trustWalletChains = map[string]string{
	"ethereum":   "eth",
	"smartchain": "bsc",
	"polygon":    "polygon",
	"arbitrum":   "arbitrum",
	"base":       "base",
	"optimism":   "optimism",
	"avalanchec": "avalanche",
	"solana":     "solana",
	"tron":       "tron",
}

// coinGeckoPlatforms：CoinGecko platforms 键 → 平台链 slug（未列出的平台保留 platform_key，chain 为空）。
var coinGeckoPlatforms = map[string]string{
	"ethereum":            "eth",
	"binance-smart-chain": "bsc",
	"polygon-pos":         "polygon",
	"arbitrum-one":        "arbitrum",
	"base":                "base",
	"optimistic-ethereum": "optimism",
	"avalanche":           "avalanche",
	"solana":              "solana",
	"tron":                "tron",
}

// evmChains：这些链的合约地址按小写归一（与平台口径一致）；其余链保留原样（base58 区分大小写）。
var evmChains = map[string]bool{
	"eth": true, "bsc": true, "polygon": true, "arbitrum": true, "base": true, "optimism": true, "avalanche": true,
}

func normalizeContract(chain, address string) string {
	address = strings.TrimSpace(address)
	if evmChains[chain] {
		return strings.ToLower(address)
	}
	return address
}

type logoAssetRow struct {
	Chain             string
	NormalizedAddress string
	SourceChain       string
	SourceAddress     string
	Symbol            string
	Name              string
	TokenType         string
	Decimals          *int
	Status            string
	LogoURL           string
	LogoSHA256        string
	LogoBytes         int64
}

type twInfo struct {
	Name     string `json:"name"`
	Symbol   string `json:"symbol"`
	Type     string `json:"type"`
	Decimals int    `json:"decimals"`
	Status   string `json:"status"`
}

func main() {
	twDir := flag.String("trustwallet", "", "Trust Wallet assets 解压目录（指向包含 blockchains/ 的那一层）")
	cgFile := flag.String("coingecko", "", "CoinGecko coins/list JSON 文件")
	cgTokenList := flag.String("coingecko-tokenlist", "", "CoinGecko 聚合 tokenlist JSON（含 chainId/address，无 CoinGecko 数字 ID）")
	cgURL := flag.String("coingecko-url", "", "直接抓取 CoinGecko coins/list（留空表示不用；传 default 使用官方地址）")
	resolve := flag.Bool("resolve-logos", false, "把目录里的 logo_url 回填到缺图标的 token_metadata")
	chains := flag.String("chains", "", "只导入这些链（逗号分隔，默认全部）")
	batch := flag.Int("batch", 500, "每批 upsert 行数")
	limit := flag.Int("limit", 0, "最多导入多少条（0=不限，用于试跑）")
	flag.Parse()

	if *twDir == "" && *cgFile == "" && *cgURL == "" && *cgTokenList == "" && !*resolve {
		fmt.Fprintln(os.Stderr, "至少指定 -trustwallet / -coingecko / -coingecko-url / -resolve-logos 之一")
		flag.Usage()
		os.Exit(2)
	}
	chainFilter := map[string]bool{}
	for _, item := range strings.Split(*chains, ",") {
		if v := strings.TrimSpace(item); v != "" {
			chainFilter[v] = true
		}
	}

	ctx := context.Background()
	cfg := config.Load()
	log := logger.New("info")
	st, err := store.New(ctx, cfg.MigrationDatabaseURL)
	if err != nil {
		log.Error("postgres connect failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if *twDir != "" {
		start := time.Now()
		stats, err := importTrustWallet(ctx, st, *twDir, chainFilter, *batch, *limit)
		if err != nil {
			log.Error("trustwallet import failed", "err", err)
			os.Exit(1)
		}
		fmt.Printf("trustwallet: 目录 %d 行，带 logo %d 行，跳过 %d 个链目录（用时 %s）\n",
			stats.Rows, stats.WithLogo, stats.SkippedChains, time.Since(start).Round(time.Second))
	}
	if *cgURL != "" || *cgFile != "" {
		start := time.Now()
		path := *cgFile
		if path == "" {
			url := *cgURL
			if url == "default" {
				url = defaultCGURL
			}
			path = filepath.Join(os.TempDir(), "coingecko-coins-list.json")
			if err := download(url, path); err != nil {
				log.Error("coingecko download failed", "url", url, "err", err)
				os.Exit(1)
			}
			fmt.Printf("coingecko: 已下载 %s → %s\n", url, path)
		}
		stats, err := importCoinGecko(ctx, st, path, chainFilter, *batch, *limit)
		if err != nil {
			log.Error("coingecko import failed", "err", err)
			os.Exit(1)
		}
		fmt.Printf("coingecko: 币种 %d 行，平台映射 %d 行，已映射链 %d 行（用时 %s）\n",
			stats.Coins, stats.Platforms, stats.MappedPlatforms, time.Since(start).Round(time.Second))
	}
	if *cgTokenList != "" {
		start := time.Now()
		stats, err := importCoinGeckoTokenList(ctx, st, *cgTokenList, chainFilter, *batch)
		if err != nil {
			log.Error("coingecko tokenlist import failed", "err", err)
			os.Exit(1)
		}
		fmt.Printf("coingecko-tokenlist: 代币 %d 行，带 logoURI %d 行，跳过链 %d 个（用时 %s）\n",
			stats.Rows, stats.WithLogo, stats.SkippedChains, time.Since(start).Round(time.Second))
	}
	if *resolve {
		n, err := resolveLogos(ctx, st)
		if err != nil {
			log.Error("resolve logos failed", "err", err)
			os.Exit(1)
		}
		fmt.Printf("resolve-logos: 回填 %d 行 token_metadata.logo_url\n", n)
	}
}

func download(url, path string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	written, err := io.Copy(out, resp.Body)
	fmt.Printf("coingecko: %d 字节\n", written)
	return err
}

type importStats struct {
	Rows            int
	WithLogo        int
	SkippedChains   int
	Coins           int
	Platforms       int
	MappedPlatforms int
}

func importTrustWallet(ctx context.Context, st *store.Store, dir string, chainFilter map[string]bool, batchSize, limit int) (importStats, error) {
	var stats importStats
	root := filepath.Join(dir, "blockchains")
	chainDirs, err := os.ReadDir(root)
	if err != nil {
		return stats, fmt.Errorf("读取 %s: %w", root, err)
	}
	rows := make([]logoAssetRow, 0, batchSize)
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		if err := upsertLogoAssets(ctx, st, sourceTrustWallet, rows); err != nil {
			return err
		}
		stats.Rows += len(rows)
		rows = rows[:0]
		return nil
	}
	for _, chainDir := range chainDirs {
		if !chainDir.IsDir() {
			continue
		}
		sourceChain := chainDir.Name()
		chain, ok := trustWalletChains[strings.ToLower(sourceChain)]
		if !ok || (len(chainFilter) > 0 && !chainFilter[chain]) {
			stats.SkippedChains++
			continue
		}
		assetsDir := filepath.Join(root, sourceChain, "assets")
		assetDirs, err := os.ReadDir(assetsDir)
		if err != nil {
			continue
		}
		for _, asset := range assetDirs {
			if !asset.IsDir() {
				continue
			}
			if limit > 0 && stats.Rows+len(rows) >= limit {
				break
			}
			row := logoAssetRow{
				Chain:             chain,
				NormalizedAddress: normalizeContract(chain, asset.Name()),
				SourceChain:       sourceChain,
				SourceAddress:     asset.Name(),
			}
			baseDir := filepath.Join(assetsDir, asset.Name())
			if raw, err := os.ReadFile(filepath.Join(baseDir, "info.json")); err == nil {
				var info twInfo
				if json.Unmarshal(raw, &info) == nil {
					row.Name = strings.TrimSpace(info.Name)
					row.Symbol = strings.TrimSpace(info.Symbol)
					row.TokenType = strings.TrimSpace(info.Type)
					row.Status = strings.TrimSpace(info.Status)
					if info.Decimals > 0 {
						d := info.Decimals
						row.Decimals = &d
					}
				}
			}
			if logoBytes, err := os.ReadFile(filepath.Join(baseDir, "logo.png")); err == nil {
				sum := sha256.Sum256(logoBytes)
				row.LogoSHA256 = hex.EncodeToString(sum[:])
				row.LogoBytes = int64(len(logoBytes))
				row.LogoURL = fmt.Sprintf("https://raw.githubusercontent.com/trustwallet/assets/master/blockchains/%s/assets/%s/logo.png", sourceChain, asset.Name())
				stats.WithLogo++
			}
			rows = append(rows, row)
			if len(rows) >= batchSize {
				if err := flush(); err != nil {
					return stats, err
				}
			}
		}
	}
	if err := flush(); err != nil {
		return stats, err
	}
	return stats, nil
}

func upsertLogoAssets(ctx context.Context, st *store.Store, source string, rows []logoAssetRow) error {
	placeholders := make([]string, 0, len(rows))
	args := make([]any, 0, len(rows)*13)
	for i, row := range rows {
		base := i * 13
		placeholders = append(placeholders, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11, base+12, base+13))
		args = append(args, source, row.Chain, row.NormalizedAddress, row.SourceChain, row.SourceAddress,
			row.Symbol, row.Name, row.TokenType, row.Decimals, row.Status, row.LogoURL, row.LogoSHA256, row.LogoBytes)
	}
	sql := `INSERT INTO token_logo_assets
        (source, chain, normalized_address, source_chain, source_address, symbol, name, token_type, decimals, status, logo_url, logo_sha256, logo_bytes)
        VALUES ` + strings.Join(placeholders, ",") + `
        ON CONFLICT (source, chain, normalized_address) DO UPDATE SET
          source_chain = EXCLUDED.source_chain,
          source_address = EXCLUDED.source_address,
          symbol = COALESCE(NULLIF(EXCLUDED.symbol, ''), token_logo_assets.symbol),
          name = COALESCE(NULLIF(EXCLUDED.name, ''), token_logo_assets.name),
          token_type = COALESCE(NULLIF(EXCLUDED.token_type, ''), token_logo_assets.token_type),
          decimals = COALESCE(EXCLUDED.decimals, token_logo_assets.decimals),
          status = COALESCE(NULLIF(EXCLUDED.status, ''), token_logo_assets.status),
          logo_url = COALESCE(NULLIF(EXCLUDED.logo_url, ''), token_logo_assets.logo_url),
          logo_sha256 = COALESCE(NULLIF(EXCLUDED.logo_sha256, ''), token_logo_assets.logo_sha256),
          logo_bytes = CASE WHEN EXCLUDED.logo_bytes > 0 THEN EXCLUDED.logo_bytes ELSE token_logo_assets.logo_bytes END,
          updated_at = now()`
	_, err := st.Pool.Exec(ctx, sql, args...)
	return err
}

type platformRow struct {
	CoinID            string
	PlatformKey       string
	Contract          string
	Chain             string
	NormalizedAddress string
}

// coinGeckoChainIDs：CoinGecko 聚合 tokenlist 的 chainId → 平台链 slug（只保留平台支持的链）。
var coinGeckoChainIDs = map[int]string{
	1: "eth", 56: "bsc", 137: "polygon", 42161: "arbitrum", 8453: "base", 10: "optimism", 43114: "avalanche",
}

// cgTokenList 是 CoinGecko 聚合 tokenlist（npm coingecko-tokens 的 tokenlist.json）的形状。
//
// 为什么还要这条路：/coins/list?include_platform=true 在本机被网络策略拦住（实测 000），
// 而这份快照经 npm registry 可达，提供 chainId+address+name+symbol+decimals+logoURI。
// 它的 extensions 里**没有** coingeckoId，因此只落"链 ↔ 合约 ↔ 名称/符号/图标 URL"，
// 数字 ID 仍然要等官方接口可达后由 -coingecko 补齐。
type cgTokenList struct {
	Tokens []struct {
		ChainID  int    `json:"chainId"`
		Address  string `json:"address"`
		Name     string `json:"name"`
		Symbol   string `json:"symbol"`
		Decimals int    `json:"decimals"`
		LogoURI  string `json:"logoURI"`
	} `json:"tokens"`
}

func importCoinGeckoTokenList(ctx context.Context, st *store.Store, path string, chainFilter map[string]bool, batchSize int) (importStats, error) {
	var stats importStats
	raw, err := os.ReadFile(path)
	if err != nil {
		return stats, err
	}
	var list cgTokenList
	if err := json.Unmarshal(raw, &list); err != nil {
		return stats, fmt.Errorf("解析 %s: %w", path, err)
	}
	rows := make([]logoAssetRow, 0, batchSize)
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		if err := upsertLogoAssets(ctx, st, sourceCoinGecko, rows); err != nil {
			return err
		}
		stats.Rows += len(rows)
		rows = rows[:0]
		return nil
	}
	seenChain := map[string]bool{}
	for _, token := range list.Tokens {
		chain, ok := coinGeckoChainIDs[token.ChainID]
		if !ok || (len(chainFilter) > 0 && !chainFilter[chain]) {
			if !ok {
				seenChain[fmt.Sprint(token.ChainID)] = true
			}
			continue
		}
		address := strings.TrimSpace(token.Address)
		if address == "" {
			continue
		}
		row := logoAssetRow{
			Chain:             chain,
			NormalizedAddress: normalizeContract(chain, address),
			SourceChain:       fmt.Sprint(token.ChainID),
			SourceAddress:     address,
			Symbol:            strings.TrimSpace(token.Symbol),
			Name:              strings.TrimSpace(token.Name),
			LogoURL:           strings.TrimSpace(token.LogoURI),
		}
		if token.Decimals > 0 {
			d := token.Decimals
			row.Decimals = &d
		}
		if row.LogoURL != "" {
			stats.WithLogo++
		}
		rows = append(rows, row)
		if len(rows) >= batchSize {
			if err := flush(); err != nil {
				return stats, err
			}
		}
	}
	if err := flush(); err != nil {
		return stats, err
	}
	stats.SkippedChains = len(seenChain)
	return stats, nil
}

type cgCoin struct {
	ID        string            `json:"id"`
	Symbol    string            `json:"symbol"`
	Name      string            `json:"name"`
	Platforms map[string]string `json:"platforms"`
}

func importCoinGecko(ctx context.Context, st *store.Store, path string, chainFilter map[string]bool, batchSize, limit int) (importStats, error) {
	var stats importStats
	file, err := os.Open(path)
	if err != nil {
		return stats, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if _, err := decoder.Token(); err != nil { // 读掉 '[' 或 '{'
		return stats, fmt.Errorf("解析 %s: %w", path, err)
	}
	coins := make([]cgCoin, 0, batchSize)
	platforms := make([]platformRow, 0, batchSize)
	flush := func() error {
		if err := upsertCoins(ctx, st, coins); err != nil {
			return err
		}
		if err := upsertPlatforms(ctx, st, platforms); err != nil {
			return err
		}
		stats.Coins += len(coins)
		stats.Platforms += len(platforms)
		coins = coins[:0]
		platforms = platforms[:0]
		return nil
	}
	for decoder.More() {
		var coin cgCoin
		if err := decoder.Decode(&coin); err != nil {
			return stats, fmt.Errorf("解析 %s: %w", path, err)
		}
		coin.ID = strings.TrimSpace(coin.ID)
		if coin.ID == "" {
			continue
		}
		coin.Symbol = strings.ToLower(strings.TrimSpace(coin.Symbol))
		coin.Name = strings.TrimSpace(coin.Name)
		coins = append(coins, cgCoin{ID: coin.ID, Symbol: coin.Symbol, Name: coin.Name})
		keys := make([]string, 0, len(coin.Platforms))
		for key := range coin.Platforms {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			contract := strings.TrimSpace(coin.Platforms[key])
			if contract == "" {
				continue
			}
			chain := coinGeckoPlatforms[strings.ToLower(key)]
			if chain == "" || (len(chainFilter) > 0 && !chainFilter[chain]) {
				continue
			}
			platforms = append(platforms, platformRow{
				CoinID:            coin.ID,
				PlatformKey:       strings.ToLower(key),
				Contract:          normalizeContract(chain, contract),
				Chain:             chain,
				NormalizedAddress: normalizeContract(chain, contract),
			})
			stats.MappedPlatforms++
		}
		if limit > 0 && stats.Coins+len(coins) >= limit {
			break
		}
		if len(coins) >= batchSize {
			if err := flush(); err != nil {
				return stats, err
			}
		}
	}
	if err := flush(); err != nil {
		return stats, err
	}
	return stats, nil
}

func upsertCoins(ctx context.Context, st *store.Store, coins []cgCoin) error {
	if len(coins) == 0 {
		return nil
	}
	placeholders := make([]string, 0, len(coins))
	args := make([]any, 0, len(coins)*3)
	for i, coin := range coins {
		base := i * 3
		placeholders = append(placeholders, fmt.Sprintf("($%d,$%d,$%d)", base+1, base+2, base+3))
		args = append(args, coin.ID, coin.Symbol, coin.Name)
	}
	sql := `INSERT INTO coingecko_coins (id, symbol, name, updated_at) VALUES ` + strings.Join(placeholders, ",") + `
        ON CONFLICT (id) DO UPDATE SET symbol = EXCLUDED.symbol, name = EXCLUDED.name, updated_at = now()`
	_, err := st.Pool.Exec(ctx, sql, args...)
	return err
}

func upsertPlatforms(ctx context.Context, st *store.Store, rows []platformRow) error {
	if len(rows) == 0 {
		return nil
	}
	placeholders := make([]string, 0, len(rows))
	args := make([]any, 0, len(rows)*5)
	for i, row := range rows {
		base := i * 5
		placeholders = append(placeholders, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d)", base+1, base+2, base+3, base+4, base+5))
		args = append(args, row.CoinID, row.PlatformKey, row.Contract, row.Chain, row.NormalizedAddress)
	}
	sql := `INSERT INTO coingecko_platforms (coin_id, platform_key, contract, chain, normalized_address, updated_at)
        VALUES ` + strings.Join(placeholders, ",") + `
        ON CONFLICT (coin_id, platform_key, contract) DO UPDATE SET
          chain = EXCLUDED.chain, normalized_address = EXCLUDED.normalized_address, updated_at = now()`
	_, err := st.Pool.Exec(ctx, sql, args...)
	return err
}

// resolveLogos 把 Trust Wallet 目录里的 logo_url 回填给缺图标的代币。
// 只补空值：已有 logo_url 或已经有自托管对象（logo_object_key）的行一律不动。
func resolveLogos(ctx context.Context, st *store.Store) (int64, error) {
	tag, err := st.Pool.Exec(ctx, `UPDATE token_metadata AS m
        SET logo_url = a.logo_url, metadata_source = COALESCE(NULLIF(m.metadata_source, ''), 'trustwallet-assets')
        FROM token_logo_assets AS a
        WHERE a.source = 'trustwallet'
          AND a.chain = m.chain
          AND a.normalized_address = m.normalized_address
          AND COALESCE(a.logo_url, '') <> ''
          AND COALESCE(m.logo_url, '') = ''
          AND COALESCE(m.logo_object_key, '') = ''`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
