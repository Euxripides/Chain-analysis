import { createHash, timingSafeEqual } from "node:crypto";

export type Config = {
  host: string;
  allowedHosts: string[];
  port: number;
  restBaseUrl: string;
  contractRestBaseUrl?: string;
  legacyOAuthValidationUrl?: string;
  restApiKey: string;
  mcpKeyHashes: Buffer[];
  oauthAccessCodeHashes: Buffer[];
  oauthSigningKey: Buffer;
  publicBaseUrl: string;
  publicMcpUrl: string;
  requestTimeoutMs: number;
  maxConcurrent: number;
  rateLimitPerMinute: number;
};

const integer = (name: string, fallback: number, min: number, max: number): number => {
  const parsed = Number.parseInt(process.env[name] ?? "", 10);
  return Number.isFinite(parsed) ? Math.min(max, Math.max(min, parsed)) : fallback;
};

export const sha256 = (value: string): Buffer => createHash("sha256").update(value).digest();

export function loadConfig(): Config {
  const keys = (process.env.MCP_API_KEYS ?? process.env.MCP_API_KEY ?? "")
    .split(",")
    .map((key) => key.trim())
    .filter(Boolean);
  const restApiKey = (process.env.REST_API_KEY ?? process.env.GPT_ACTIONS_API_KEY ?? "").trim();
  if (keys.length === 0) throw new Error("MCP_API_KEY or MCP_API_KEYS is required");
  if (!restApiKey) throw new Error("REST_API_KEY or GPT_ACTIONS_API_KEY is required");
  const oauthAccessCodes = (process.env.MCP_OAUTH_ACCESS_CODES ?? "")
    .split(",")
    .map((key) => key.trim())
    .filter(Boolean);
  const publicBaseUrl = (process.env.MCP_PUBLIC_URL ?? "https://aurore.online").replace(/\/$/, "");
  const oauthSigningSecret = (process.env.MCP_OAUTH_SIGNING_KEY ?? "").trim() || `oauth:${keys[0]}`;
  return {
    host: process.env.MCP_HOST ?? "127.0.0.1",
    allowedHosts: (process.env.MCP_ALLOWED_HOSTS ?? "aurore.online,localhost,127.0.0.1").split(",").map((host) => host.trim()).filter(Boolean),
    port: integer("MCP_PORT", 8090, 1, 65535),
    restBaseUrl: (process.env.REST_API_BASE_URL ?? "http://127.0.0.1:8080/api/v1").replace(/\/$/, ""),
    contractRestBaseUrl: process.env.CONTRACT_REST_API_BASE_URL?.replace(/\/$/, ""),
    legacyOAuthValidationUrl: process.env.MCP_LEGACY_OAUTH_VALIDATION_URL,
    restApiKey,
    mcpKeyHashes: keys.map(sha256),
    oauthAccessCodeHashes: (oauthAccessCodes.length > 0 ? oauthAccessCodes : keys).map(sha256),
    oauthSigningKey: sha256(oauthSigningSecret),
    publicBaseUrl,
    publicMcpUrl: `${publicBaseUrl}/mcp`,
    requestTimeoutMs: integer("MCP_REQUEST_TIMEOUT_MS", 60_000, 1_000, 120_000),
    maxConcurrent: integer("MCP_MAX_CONCURRENT", 16, 1, 128),
    rateLimitPerMinute: integer("MCP_RATE_LIMIT_PER_MINUTE", 60, 1, 10_000),
  };
}

export function authorized(header: string | undefined, expected: Buffer[]): boolean {
  if (!header?.startsWith("Bearer ")) return false;
  const actual = sha256(header.slice(7).trim());
  return expected.some((hash) => hash.length === actual.length && timingSafeEqual(hash, actual));
}
