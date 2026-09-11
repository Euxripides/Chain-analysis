/** 设计令牌的 TypeScript 视图，供图表与内联样式复用（颜色改动只需改 tokens.css）。 */
export const tokens = {
  graphite: "#101828",
  cobalt: "#175cd3",
  cobaltStrong: "#0f47a8",
  ice: "#f3f7fc",
  slate: "#667085",
  amber: "#d97706",
  green: "#067647",
  danger: "#b42318",
  border: "#d8e0ec",
} as const;

export const chartPalette = [
  "#175cd3", "#067647", "#d97706", "#6941c6", "#0e7490", "#b42318", "#475467", "#ca8504",
] as const;

/** 金额展示：整数用 BigInt 安全字符串，禁止把大整数塞进 JS number（§21.2）。 */
export function formatRawAmount(raw: string, decimals: number): string {
  const negative = raw.startsWith("-");
  const digits = (negative ? raw.slice(1) : raw).padStart(decimals + 1, "0");
  const intPart = digits.slice(0, digits.length - decimals);
  const fracPart = decimals > 0 ? digits.slice(digits.length - decimals).replace(/0+$/, "") : "";
  const grouped = intPart.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return `${negative ? "-" : ""}${grouped}${fracPart ? "." + fracPart : ""}`;
}

export function shortHash(value: string, head = 8, tail = 6): string {
  if (!value) return "";
  if (value.length <= head + tail + 1) return value;
  return `${value.slice(0, head)}…${value.slice(-tail)}`;
}
