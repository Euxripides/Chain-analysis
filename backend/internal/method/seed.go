package method

import (
	"context"
	"strings"
)

// ---- Local Signature Seed（§32）----
//
// 预置常见协议签名，使绝大多数交易无需任何外部解析即可得到方法名。
// Selector 由 keccak256(signature)[:4] 在启动时计算，不硬编码，避免手抄错误。

// seedSignatures 覆盖 ERC20 / ERC721 / ERC1155 / Uniswap V2 / Uniswap V3 /
// PancakeSwap / WETH-WBNB / Proxy / Ownable / AccessControl 及常见质押、桥、风险方法。
var seedSignatures = []string{
	// ERC20
	"transfer(address,uint256)",
	"approve(address,uint256)",
	"transferFrom(address,address,uint256)",
	"balanceOf(address)",
	"allowance(address,address)",
	"totalSupply()",
	"decimals()",
	"symbol()",
	"name()",
	"increaseAllowance(address,uint256)",
	"decreaseAllowance(address,uint256)",
	"mint(address,uint256)",
	"burn(uint256)",
	"burnFrom(address,uint256)",
	"permit(address,address,uint256,uint256,uint8,bytes32,bytes32)",

	// ERC721
	"safeTransferFrom(address,address,uint256)",
	"safeTransferFrom(address,address,uint256,bytes)",
	"setApprovalForAll(address,bool)",
	"isApprovedForAll(address,address)",
	"getApproved(uint256)",
	"ownerOf(uint256)",
	"tokenURI(uint256)",
	"safeMint(address,uint256)",
	"mint(address)",

	// ERC1155
	"safeTransferFrom(address,address,uint256,uint256,bytes)",
	"safeBatchTransferFrom(address,address,uint256[],uint256[],bytes)",
	"balanceOfBatch(address[],uint256[])",
	"uri(uint256)",

	// Uniswap V2 / PancakeSwap Router
	"swapExactTokensForTokens(uint256,uint256,address[],address,uint256)",
	"swapTokensForExactTokens(uint256,uint256,address[],address,uint256)",
	"swapExactETHForTokens(uint256,address[],address,uint256)",
	"swapTokensForExactETH(uint256,uint256,address[],address,uint256)",
	"swapExactTokensForETH(uint256,uint256,address[],address,uint256)",
	"swapETHForExactTokens(uint256,address[],address,uint256)",
	"swapExactTokensForTokensSupportingFeeOnTransferTokens(uint256,uint256,address[],address,uint256)",
	"swapExactETHForTokensSupportingFeeOnTransferTokens(uint256,address[],address,uint256)",
	"swapExactTokensForETHSupportingFeeOnTransferTokens(uint256,uint256,address[],address,uint256)",
	"addLiquidity(address,address,uint256,uint256,uint256,uint256,address,uint256)",
	"addLiquidityETH(address,uint256,uint256,uint256,address,uint256)",
	"removeLiquidity(address,address,uint256,uint256,uint256,address,uint256)",
	"removeLiquidityETH(address,uint256,uint256,uint256,address,uint256)",
	"removeLiquidityETHSupportingFeeOnTransferTokens(address,uint256,uint256,uint256,address,uint256)",
	"removeLiquidityWithPermit(address,address,uint256,uint256,uint256,address,uint256,bool,uint8,bytes32,bytes32)",
	"removeLiquidityETHWithPermit(address,uint256,uint256,uint256,address,uint256,bool,uint8,bytes32,bytes32)",
	"getAmountsOut(uint256,address[])",
	"getAmountsIn(uint256,address[])",
	"quote(uint256,uint256,uint256)",

	// Uniswap V2 Pair
	"swap(uint256,uint256,address,bytes)",
	"sync()",
	"skim(address)",
	"burn(address)",

	// Uniswap V3
	"exactInputSingle((address,address,uint24,address,uint256,uint256,uint256,uint160))",
	"exactInput((bytes,address,uint256,uint256,uint256))",
	"exactOutputSingle((address,address,uint24,address,uint256,uint256,uint256,uint160))",
	"exactOutput((bytes,address,uint256,uint256,uint256))",
	"multicall(bytes[])",
	"multicall(uint256,bytes[])",
	"multicall(bytes32,bytes[])",
	"selfPermit(address,uint256,uint256,uint8,bytes32,bytes32)",
	"unwrapWETH9(uint256,address)",
	"refundETH()",
	"sweepToken(address,uint256,address)",
	"mint((address,address,uint24,int24,int24,uint256,uint256,uint256,uint256,address,uint256))",
	"increaseLiquidity((uint256,uint256,uint256,uint256,uint256,uint256))",
	"decreaseLiquidity((uint256,uint128,uint256,uint256,uint256))",
	"collect((uint256,address,uint128,uint128))",
	"swap(address,bool,int256,uint160,bytes)",
	"execute(bytes,bytes[],uint256)",
	"execute(bytes,bytes[])",

	// Multicall3
	"aggregate((address,bytes)[])",
	"aggregate3((address,bool,bytes)[])",

	// WETH / WBNB
	"deposit()",
	"withdraw(uint256)",

	// Proxy
	"upgradeTo(address)",
	"upgradeToAndCall(address,bytes)",
	"setImplementation(address)",
	"implementation()",
	"changeAdmin(address)",
	"admin()",
	"initialize()",
	"initialize(address)",
	"upgrade(address,address)",

	// Ownable
	"owner()",
	"transferOwnership(address)",
	"renounceOwnership()",

	// AccessControl
	"grantRole(bytes32,address)",
	"revokeRole(bytes32,address)",
	"renounceRole(bytes32,address)",
	"hasRole(bytes32,address)",
	"getRoleAdmin(bytes32)",

	// 质押 / 收益 / 桥 / 管理（含 §46 风险方法）
	"deposit(uint256)",
	"deposit(uint256,address)",
	"withdraw(uint256,address,address)",
	"stake(uint256)",
	"unstake(uint256)",
	"claim()",
	"claimRewards()",
	"harvest()",
	"emergencyWithdraw()",
	"emergencyWithdraw(uint256)",
	"setTax(uint256)",
	"blacklist(address)",
	"pause()",
	"unpause()",
	"execute(address,uint256,bytes)",
	"bridge(address,uint256,uint256)",
	"depositETH(uint256,bytes)",
}

// seedEventSignatures 常见事件（§29：让 Logs 显示 Transfer/Swap/Approval 而非 topic0）。
var seedEventSignatures = []string{
	"Transfer(address,address,uint256)",
	"Approval(address,address,uint256)",
	"ApprovalForAll(address,address,bool)",
	"TransferSingle(address,address,address,uint256,uint256)",
	"TransferBatch(address,address,address,uint256[],uint256[])",
	"Swap(address,uint256,uint256,uint256,uint256,address)",
	"Swap(address,address,int256,int256,uint160,uint128,int24)",
	"Sync(uint112,uint112)",
	"Mint(address,uint256,uint256)",
	"Burn(address,uint256,uint256,address)",
	"Deposit(address,uint256)",
	"Withdrawal(address,uint256)",
	"OwnershipTransferred(address,address)",
	"RoleGranted(bytes32,address,address)",
	"RoleRevoked(bytes32,address,address)",
	"Paused(address)",
	"Unpaused(address)",
	"Upgraded(address)",
	"AdminChanged(address,address)",
}

// SeedSignatures 生成本地签名种子记录（幂等，按 signature 去重）。
func SeedSignatures() []SignatureRecord {
	seen := map[string]bool{}
	out := make([]SignatureRecord, 0, len(seedSignatures))
	for _, signature := range seedSignatures {
		sig := strings.TrimSpace(signature)
		if sig == "" || seen[sig] {
			continue
		}
		seen[sig] = true
		out = append(out, SignatureRecord{
			Selector:  SelectorOf(sig),
			Name:      NameOf(sig),
			Signature: sig,
			Source:    SourceLocalSignature,
			// verified 在这里表示"签名与 selector 自洽且来自策划过的标准协议"，
			// 与"合约已验证"是两件事：解析结果的 Verified 由 verifiedFor() 决定（§27）。
			Verified: true,
		})
	}
	return out
}

// SeedEventSignatures 生成事件签名种子记录。
func SeedEventSignatures() []EventSignatureRecord {
	seen := map[string]bool{}
	out := make([]EventSignatureRecord, 0, len(seedEventSignatures))
	for _, signature := range seedEventSignatures {
		sig := strings.TrimSpace(signature)
		if sig == "" || seen[sig] {
			continue
		}
		seen[sig] = true
		out = append(out, EventSignatureRecord{
			Topic0:    Topic0Of(sig),
			Name:      NameOf(sig),
			Signature: sig,
			Source:    SourceLocalSignature,
			Verified:  true,
		})
	}
	return out
}

// EnsureSeed 幂等写入种子（每次启动执行，冲突即忽略）。
func EnsureSeed(ctx context.Context, store Store) (int64, int64, error) {
	methods, err := store.UpsertMethodSignatures(ctx, SeedSignatures())
	if err != nil {
		return 0, 0, err
	}
	events, err := store.UpsertEventSignatures(ctx, SeedEventSignatures())
	if err != nil {
		return methods, 0, err
	}
	return methods, events, nil
}

// ---- Method Category（§45，P1：仅用于展示，绝不覆盖真实 method_name）----

var categoryByName = map[string]string{
	"transfer": "transfer", "transferfrom": "transfer", "safetransferfrom": "transfer",
	"safebatchtransferfrom": "transfer", "send": "transfer",
	"approve": "approval", "increaseallowance": "approval", "decreaseallowance": "approval",
	"setapprovalforall": "approval", "permit": "approval", "selfpermit": "approval",
	"addliquidity": "liquidity", "addliquidityeth": "liquidity", "removeliquidity": "liquidity",
	"removeliquidityeth": "liquidity", "removeliquiditywithpermit": "liquidity",
	"removeliquidityethwithpermit": "liquidity", "increaseliquidity": "liquidity", "decreaseliquidity": "liquidity",
	"stake": "staking", "unstake": "staking", "harvest": "staking", "claimrewards": "claim", "claim": "claim",
	"deposit": "deposit", "depositeth": "deposit", "withdraw": "withdraw", "emergencywithdraw": "withdraw",
	"mint": "mint", "safemint": "mint", "burn": "burn", "burnfrom": "burn",
	"upgradeto": "proxy", "upgradetoandcall": "proxy", "setimplementation": "proxy", "upgrade": "proxy",
	"changeadmin": "admin", "transferownership": "admin", "renounceownership": "admin",
	"grantrole": "admin", "revokerole": "admin", "renouncerole": "admin", "pause": "admin", "unpause": "admin",
	"blacklist": "admin", "settax": "admin", "initialize": "admin",
	"bridge": "bridge",
}

// Category 由方法名推断展示分类（§45）。数据库保存真实 method_name，UI 才使用 category。
func Category(name string) string {
	if name == "" {
		return "unknown"
	}
	lower := strings.ToLower(name)
	if category, ok := categoryByName[lower]; ok {
		return category
	}
	switch {
	case strings.HasPrefix(lower, "swap"), strings.Contains(lower, "exactinput"), strings.Contains(lower, "exactoutput"):
		return "swap"
	case strings.HasPrefix(lower, "deposit"):
		return "deposit"
	case strings.HasPrefix(lower, "withdraw"):
		return "withdraw"
	case strings.HasPrefix(lower, "claim"):
		return "claim"
	case strings.HasPrefix(lower, "transfer"):
		return "transfer"
	case name == NameNativeTransfer:
		return "transfer"
	case name == NameContractCreation:
		return "admin"
	}
	return "unknown"
}

// RiskMethods 智能调查关注的高风险方法（§46）。
var RiskMethods = map[string]bool{
	"emergencywithdraw": true, "mint": true, "blacklist": true, "settax": true,
	"upgradeto": true, "upgradetoandcall": true, "setimplementation": true,
	"transferownership": true, "renounceownership": true, "pause": true,
}

// IsRiskMethod 是否属于风险方法（§46）。
func IsRiskMethod(name string) bool { return RiskMethods[strings.ToLower(name)] }
