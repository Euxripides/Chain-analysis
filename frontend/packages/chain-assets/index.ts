import ethereumLogo from "./assets/ethereum.png";
import bscLogo from "./assets/bsc.png";
import arbitrumLogo from "./assets/arbitrum.png";
import optimismLogo from "./assets/optimism.png";
import baseLogo from "./assets/base.png";
import polygonLogo from "./assets/polygon.png";
import solanaLogo from "./assets/solana.png";
import avalancheLogo from "./assets/avalanche.png";
import tronLogo from "./assets/tron.png";
import bitcoinLogo from "./assets/bitcoin.png";
import fantomLogo from "./assets/fantom.png";
import gnosisLogo from "./assets/gnosis.png";
import lineaLogo from "./assets/linea.png";
import scrollLogo from "./assets/scroll.png";
import storyLogo from "./assets/story.png";
import xlayerLogo from "./assets/xlayer.png";
import flareLogo from "./assets/flare.svg";
import taikoLogo from "./assets/taiko.svg";
import telosLogo from "./assets/telos.svg";
import somniaLogo from "./assets/somnia.ico?url";

// Local brand assets shared by download, explorer and legacy selectors.
// Identity and source hashes are recorded in sources.json.
const chainLogos: Readonly<Record<string, string>> = {
  eth: ethereumLogo,
  ethereum: ethereumLogo,
  bsc: bscLogo,
  bnb: bscLogo,
  arb: arbitrumLogo,
  arbitrum: arbitrumLogo,
  op: optimismLogo,
  optimism: optimismLogo,
  base: baseLogo,
  matic: polygonLogo,
  polygon: polygonLogo,
  sol: solanaLogo,
  solana: solanaLogo,
  avax: avalancheLogo,
  avalanche: avalancheLogo,
  trx: tronLogo,
  tron: tronLogo,
  btc: bitcoinLogo,
  bitcoin: bitcoinLogo,
  fantom: fantomLogo,
  flare: flareLogo,
  gnosis: gnosisLogo,
  xdai: gnosisLogo,
  linea: lineaLogo,
  scroll: scrollLogo,
  somnia_mainnet: somniaLogo,
  somnia: somniaLogo,
  story_mainnet: storyLogo,
  story: storyLogo,
  taiko: taikoLogo,
  telos: telosLogo,
  xlayer: xlayerLogo,
  x_layer: xlayerLogo,
};

export function getChainLogo(chain: string): string | undefined {
  const slug = chain.trim().toLowerCase();
  return Object.hasOwn(chainLogos, slug) ? chainLogos[slug] : undefined;
}
