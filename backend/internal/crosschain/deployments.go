package crosschain

import "strings"

// Generated from the frozen primary sources listed in qa/specs/manifest.json.
// Historical code is still read at the transaction block; this registry alone
// never upgrades an event signature or API record into a bridge proof.
type Deployment struct {
	Protocol, Chain, Role, Address string
	FromBlock                      uint64
	Source                         string
}

var Deployments = []Deployment{
	{"layerzero", "arbitrum", "stargate_pool", "0xa45b5130f36cdca45667738e2a258ab09f4a5f7f", 215658702, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/arbitrum-mainnet/StargatePoolNative.json"},
	{"layerzero", "arbitrum", "stargate_pool", "0xe8cdf27acd73a434d661c84887215f7598e7d0d3", 215658734, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/arbitrum-mainnet/StargatePoolUSDC.json"},
	{"layerzero", "arbitrum", "stargate_pool", "0xce8cca271ebc0533920c83d39f417ed6a0abb7d0", 215658745, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/arbitrum-mainnet/StargatePoolUSDT.json"},
	{"layerzero", "arbitrum", "stargate_messaging", "0x19cfce47ed54a88614648dc3f19a5980097007dd", 215658770, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/arbitrum-mainnet/TokenMessaging.json"},
	{"layerzero", "base", "stargate_pool", "0xdc181bd607330aeebef6ea62e03e5e1fb4b6f7c7", 15023831, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/base-mainnet/StargatePoolNative.json"},
	{"layerzero", "base", "stargate_pool", "0x27a16dc786820b16e5c9028b75b99f6f604b5d26", 15023835, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/base-mainnet/StargatePoolUSDC.json"},
	{"layerzero", "base", "stargate_messaging", "0x5634c4a5fed09819e3c46d86a965dd9447d86e47", 15023838, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/base-mainnet/TokenMessaging.json"},
	{"layerzero", "bsc", "stargate_pool", "0x962bd449e630b0d928f308ce63f1a21f02576057", 40968483, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/bsc-mainnet/StargatePoolUSDC.json"},
	{"layerzero", "bsc", "stargate_pool", "0x138eb30f73bc423c6455c53df6d89cb01d9ebc63", 39096424, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/bsc-mainnet/StargatePoolUSDT.json"},
	{"layerzero", "bsc", "stargate_messaging", "0x6e3d884c96d640526f273c61dfcf08915ebd7e2b", 39096427, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/bsc-mainnet/TokenMessaging.json"},
	{"layerzero", "ethereum", "stargate_pool", "0x77b2043768d28e9c9ab44e1abfc95944bce57931", 19963227, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/ethereum-mainnet/StargatePoolNative.json"},
	{"layerzero", "ethereum", "stargate_pool", "0xc026395860db2d07ee33e05fe50ed7bd583189c7", 19963230, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/ethereum-mainnet/StargatePoolUSDC.json"},
	{"layerzero", "ethereum", "stargate_pool", "0x933597a323eb81cae705c5bc29985172fd5a3973", 19963231, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/ethereum-mainnet/StargatePoolUSDT.json"},
	{"layerzero", "ethereum", "stargate_messaging", "0x6d6620efa72948c5f68a3c8646d58c00d3f4a980", 19963232, "https://raw.githubusercontent.com/stargate-protocol/stargate-v2/9f4f5c0fc3e1df570415e441523c82fd94173588/packages/stg-evm-v2/deployments/ethereum-mainnet/TokenMessaging.json"},
	{"layerzero", "ethereum", "oft_application", "0xc08cd26474722ce93f4d0c34d16201461c10aa8c", 0, "https://docs.carv.io/carv-ecosystem/carv-play/smart-contracts-and-security"},
	{"layerzero", "arbitrum", "oft_application", "0xc08cd26474722ce93f4d0c34d16201461c10aa8c", 0, "https://docs.carv.io/carv-ecosystem/carv-play/smart-contracts-and-security"},
	{"layerzero", "base", "oft_application", "0xc08cd26474722ce93f4d0c34d16201461c10aa8c", 0, "https://docs.carv.io/carv-ecosystem/carv-play/smart-contracts-and-security"},
	{"layerzero", "bsc", "oft_application", "0xc08cd26474722ce93f4d0c34d16201461c10aa8c", 0, "https://docs.carv.io/carv-ecosystem/carv-play/smart-contracts-and-security"},
	{"layerzero", "polygon", "oft_application", "0xf4c3fac9c98aa62474998e299495b699dfdb00eb", 0, "https://www.handlpay.com/token"},
	{"layerzero", "base", "oft_application", "0x3bbcb624cb9a1f73163a886f460f47603e5e4425", 0, "https://www.handlpay.com/token"},
	{"wormhole", "base", "core_bridge", "0xbebdb6c8ddc678ffa9f8748f85c815c556dd8ac6", 0, "https://wormhole.com/docs/reference/contract-addresses/"},
	{"wormhole", "base", "token_bridge", "0x8d2de8d2f73f1f4cab472ac9a881c9b123c79627", 0, "https://wormhole.com/docs/reference/contract-addresses/"},
	{"cctp", "ethereum", "token_messenger_v2", CCTPMessengerV2, 0, "https://developers.circle.com/cctp/references/contract-addresses"},
	{"cctp", "avalanche", "token_messenger_v2", CCTPMessengerV2, 0, "https://developers.circle.com/cctp/references/contract-addresses"},
	{"cctp", "optimism", "token_messenger_v2", CCTPMessengerV2, 0, "https://developers.circle.com/cctp/references/contract-addresses"},
	{"cctp", "arbitrum", "token_messenger_v2", CCTPMessengerV2, 0, "https://developers.circle.com/cctp/references/contract-addresses"},
	{"cctp", "base", "token_messenger_v2", CCTPMessengerV2, 0, "https://developers.circle.com/cctp/references/contract-addresses"},
	{"cctp", "polygon", "token_messenger_v2", CCTPMessengerV2, 0, "https://developers.circle.com/cctp/references/contract-addresses"},
	{"layerzero", "ethereum", "usdt0_application", "0x6c96de32cea08842dcc4058c14d3aaad7fa41dee", 0, "https://docs.usdt0.to/technical-documentation/deployments"},
	{"layerzero", "arbitrum", "usdt0_application", "0x14e4a1b13bf7f943c8ff7c51fb60fa964a298d92", 0, "https://docs.usdt0.to/technical-documentation/deployments"},
	{"layerzero", "optimism", "usdt0_application", "0xf03b4d9ac1d5d1e7c4cef54c2a313b9fe051a0ad", 0, "https://docs.usdt0.to/technical-documentation/deployments"},
	{"layerzero", "polygon", "usdt0_application", "0x6ba10300f0dc58b7a1e4c0e41f5dabb7d7829e13", 0, "https://docs.usdt0.to/technical-documentation/deployments"},
	{"across", "ethereum", "spoke_pool", "0x5c7bcd6e7de5423a257d81b442095a1a6ced35c5", 17117454, "https://raw.githubusercontent.com/across-protocol/contracts/a634bea927668519c748e46036181c89c7bd9b40/broadcast/deployed-addresses.json"},
	{"across", "bsc", "spoke_pool", "0x4e8e101924ede233c13e2d8622dc8aed2872d505", 48762335, "https://raw.githubusercontent.com/across-protocol/contracts/a634bea927668519c748e46036181c89c7bd9b40/broadcast/deployed-addresses.json"},
	{"across", "polygon", "spoke_pool", "0x9295ee1d8c5b022be115a2ad3c30c72e34e7f096", 41908657, "https://raw.githubusercontent.com/across-protocol/contracts/a634bea927668519c748e46036181c89c7bd9b40/broadcast/deployed-addresses.json"},
	{"across", "arbitrum", "spoke_pool", "0xe35e9842fceaca96570b734083f4a58e8f7c5f2a", 83868041, "https://raw.githubusercontent.com/across-protocol/contracts/a634bea927668519c748e46036181c89c7bd9b40/broadcast/deployed-addresses.json"},
	{"across", "optimism", "spoke_pool", "0x6f26bf09b1c792e3228e5467807a900a503c0281", 93903076, "https://raw.githubusercontent.com/across-protocol/contracts/a634bea927668519c748e46036181c89c7bd9b40/broadcast/deployed-addresses.json"},
	{"across", "base", "spoke_pool", "0x09aea4b2242abc8bb4bb78d537a67a245a7bec64", 2164878, "https://raw.githubusercontent.com/across-protocol/contracts/a634bea927668519c748e46036181c89c7bd9b40/broadcast/deployed-addresses.json"},
	{"across", "avalanche", "spoke_pool", "0xfe9d541c92e4e90437c7152a00244886de37a658", 89364263, "https://raw.githubusercontent.com/across-protocol/contracts/a634bea927668519c748e46036181c89c7bd9b40/broadcast/deployed-addresses.json"},
	{"layerzero", "arbitrum", "endpoint_v2", "0x1a44076050125825900e736c501f859c50fe728c", 0, "https://metadata.layerzero-api.com/v1/metadata"},
	{"layerzero", "avalanche", "endpoint_v2", "0x1a44076050125825900e736c501f859c50fe728c", 0, "https://metadata.layerzero-api.com/v1/metadata"},
	{"layerzero", "base", "endpoint_v2", "0x1a44076050125825900e736c501f859c50fe728c", 0, "https://metadata.layerzero-api.com/v1/metadata"},
	{"layerzero", "bsc", "endpoint_v2", "0x1a44076050125825900e736c501f859c50fe728c", 0, "https://metadata.layerzero-api.com/v1/metadata"},
	{"layerzero", "ethereum", "endpoint_v2", "0x1a44076050125825900e736c501f859c50fe728c", 0, "https://metadata.layerzero-api.com/v1/metadata"},
	{"layerzero", "optimism", "endpoint_v2", "0x1a44076050125825900e736c501f859c50fe728c", 0, "https://metadata.layerzero-api.com/v1/metadata"},
	{"layerzero", "polygon", "endpoint_v2", "0x1a44076050125825900e736c501f859c50fe728c", 0, "https://metadata.layerzero-api.com/v1/metadata"},
	{"wormhole", "solana", "core_bridge", "worm2ZoG2kUd4vFXhvjh93UUH596ayRfgQ2MgjNMTth", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "ethereum", "core_bridge", "0x98f3c9e6e3face36baad05fe09d375ef1464288b", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "bsc", "core_bridge", "0x98f3c9e6e3face36baad05fe09d375ef1464288b", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "polygon", "core_bridge", "0x7a4b5a56256163f07b2c80a7ca55abe66c4ec4d7", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "avalanche", "core_bridge", "0x54a8e5f9c4cba08f9943965859f6c34eaf03e26c", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "arbitrum", "core_bridge", "0xa5f208e072434bc67592e4c49c1b991ba79bca46", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "optimism", "core_bridge", "0xee91c335eab126df5fdb3797ea9d6ad93aec9722", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "solana", "token_bridge", "wormDTUJ6AWPNvk59vGQbDvGJmqbDTdgWgAqcLBCgUb", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "ethereum", "token_bridge", "0x3ee18b2214aff97000d974cf647e7c347e8fa585", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "bsc", "token_bridge", "0xb6f6d86a8f9879a9c87f643768d9efc38c1da6e7", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "polygon", "token_bridge", "0x5a58505a96d1dbf8df91cb21b54419fc36e93fde", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "avalanche", "token_bridge", "0x0e082f06ff657d94310cb8ce8b0d9a04541d8052", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "arbitrum", "token_bridge", "0x0b2402144bb366a632d14b83f244d2e0e21bd39c", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"wormhole", "optimism", "token_bridge", "0x1d68124e65fafc907325e3edbf8c4d84499daa8b", 0, "https://raw.githubusercontent.com/wormhole-foundation/xdapp-book/main/src/reference/contracts.md"},
	{"cctp", "ethereum", "message_transmitter_v2", "0x81d40f21f12a8f0e3252bccb954d722d4c464b64", 0, "https://developers.circle.com/cctp/references/contract-addresses"},
	{"cctp", "polygon", "message_transmitter_v2", "0x81d40f21f12a8f0e3252bccb954d722d4c464b64", 0, "https://developers.circle.com/cctp/references/contract-addresses"},
	{"cctp", "arbitrum", "message_transmitter_v2", "0x81d40f21f12a8f0e3252bccb954d722d4c464b64", 0, "https://developers.circle.com/cctp/references/contract-addresses"},
	{"cctp", "optimism", "message_transmitter_v2", "0x81d40f21f12a8f0e3252bccb954d722d4c464b64", 0, "https://developers.circle.com/cctp/references/contract-addresses"},
	{"cctp", "base", "message_transmitter_v2", "0x81d40f21f12a8f0e3252bccb954d722d4c464b64", 0, "https://developers.circle.com/cctp/references/contract-addresses"},
	{"cctp", "avalanche", "message_transmitter_v2", "0x81d40f21f12a8f0e3252bccb954d722d4c464b64", 0, "https://developers.circle.com/cctp/references/contract-addresses"},
}

func FindDeployment(protocol, chain, address string) (Deployment, bool) {
	for _, d := range Deployments {
		if d.Protocol == protocol && d.Chain == chain && Same(chain, d.Address, address) {
			return d, true
		}
	}
	return Deployment{}, false
}
func TrustedDeployment(protocol, chain, address string) bool {
	_, ok := FindDeployment(protocol, chain, address)
	return ok
}
func DeploymentAddress(protocol, chain, role string) string {
	for _, d := range Deployments {
		if d.Protocol == protocol && d.Chain == chain && d.Role == role {
			return d.Address
		}
	}
	return ""
}

var LayerZeroEIDs = map[string]string{
	"arbitrum":  "30110",
	"avalanche": "30106",
	"base":      "30184",
	"bsc":       "30102",
	"ethereum":  "30101",
	"optimism":  "30111",
	"polygon":   "30109",
}

func LayerZeroChain(eid string) string {
	for c, v := range LayerZeroEIDs {
		if v == strings.TrimSpace(eid) {
			return c
		}
	}
	return ""
}

func StrictCapabilities() []map[string]any {
	return []map[string]any{
		{"protocol": "across", "state": "conditional", "chains": []string{"ethereum", "bsc", "polygon", "arbitrum", "optimism", "base", "avalanche"}, "requires": "official historical SpokePool; complete relay payload; unique executed funding path; finalized exact receipts"},
		{"protocol": "layerzero", "state": "conditional", "applications": []string{"USDT0", "Stargate V2 taxi", "CARV", "HANDL"}, "requires": "registered historical application; EndpointV2 packet and successful exact payload delivery; mutual peers; OFT funding events and precision; finalized receipts"},
		{"protocol": "wormhole", "state": "conditional_evm_token_bridge", "requires": "official core and token bridge; emitter chain/address/sequence; exact published and redeemed VAA payload; unique actual asset funding; finalized receipts"},
		{"protocol": "cctp", "state": "conditional_evm_v2", "chains": []string{"ethereum", "avalanche", "optimism", "arbitrum", "base", "polygon"}, "requires": "official source-tx attestation; immutable MessageSent fields; exact successful attested receive; actual burn, mint and executed fee; historical messenger and transmitter; finalized receipts"},
		{"protocol": "lifi,near_intents,bridgers,cbridge,relay", "state": "protocol_discovery_only_unless_underlying_supported", "reason": "API reports and amount/time candidates alone do not satisfy strict funds verification"},
		{"chains": []string{"solana", "tron"}, "state": "strict_association_unsupported", "reason": "existing movement decoding remains available in discovery; protocol-specific complete proof and finality attestation not implemented"},
	}
}
