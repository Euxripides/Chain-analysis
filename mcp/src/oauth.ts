import type { Response } from "express";
import {
  createHmac,
  randomBytes,
  timingSafeEqual,
} from "node:crypto";
import type { OAuthRegisteredClientsStore } from "@modelcontextprotocol/sdk/server/auth/clients.js";
import type { AuthorizationParams, OAuthServerProvider } from "@modelcontextprotocol/sdk/server/auth/provider.js";
import type { AuthInfo } from "@modelcontextprotocol/sdk/server/auth/types.js";
import { InvalidGrantError, InvalidRequestError } from "@modelcontextprotocol/sdk/server/auth/errors.js";
import type {
  OAuthClientInformationFull,
  OAuthTokenRevocationRequest,
  OAuthTokens,
} from "@modelcontextprotocol/sdk/shared/auth.js";
import { authorized, sha256, type Config } from "./config.js";

type SignedPayload = {
  typ: "client" | "access" | "refresh";
  iat: number;
  exp: number;
  [key: string]: unknown;
};

type CodeRecord = {
  clientId: string;
  codeChallenge: string;
  redirectUri: string;
  resource: string;
  scopes: string[];
  subject: string;
  expiresAt: number;
};

const b64 = (value: Buffer | string): string => Buffer.from(value).toString("base64url");
const now = (): number => Math.floor(Date.now() / 1000);

function sign(kind: string, payload: SignedPayload, key: Buffer): string {
  const body = b64(JSON.stringify(payload));
  const input = `${kind}.${body}`;
  const signature = createHmac("sha256", key).update(input).digest();
  return `${input}.${b64(signature)}`;
}

function verify<T extends SignedPayload>(token: string, kind: string, key: Buffer): T {
  const parts = token.split(".");
  if (parts.length !== 3 || parts[0] !== kind) throw new InvalidGrantError("Invalid signed credential");
  const input = `${parts[0]}.${parts[1]}`;
  const expected = createHmac("sha256", key).update(input).digest();
  let actual: Buffer;
  try {
    actual = Buffer.from(parts[2], "base64url");
  } catch {
    throw new InvalidGrantError("Invalid signed credential");
  }
  if (actual.length !== expected.length || !timingSafeEqual(actual, expected)) {
    throw new InvalidGrantError("Invalid signed credential");
  }
  let payload: T;
  try {
    payload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8")) as T;
  } catch {
    throw new InvalidGrantError("Invalid signed credential");
  }
  if (payload.typ !== kind || !Number.isFinite(payload.exp) || payload.exp <= now()) {
    throw new InvalidGrantError("Expired signed credential");
  }
  return payload;
}

class StatelessClientsStore implements OAuthRegisteredClientsStore {
  constructor(private readonly signingKey: Buffer) {}

  async registerClient(client: Omit<OAuthClientInformationFull, "client_id" | "client_id_issued_at">): Promise<OAuthClientInformationFull> {
    if (client.token_endpoint_auth_method !== "none") {
      throw new InvalidRequestError("Only public OAuth clients with token_endpoint_auth_method=none are supported");
    }
    const issuedAt = now();
    const metadata = {
      ...client,
      client_secret: undefined,
      client_secret_expires_at: undefined,
      token_endpoint_auth_method: "none" as const,
    };
    const clientId = sign("client", {
      typ: "client",
      iat: issuedAt,
      exp: issuedAt + 365 * 24 * 60 * 60,
      metadata,
    }, this.signingKey);
    return { ...metadata, client_id: clientId, client_id_issued_at: issuedAt };
  }

  async getClient(clientId: string): Promise<OAuthClientInformationFull | undefined> {
    try {
      const payload = verify<SignedPayload & { metadata: Omit<OAuthClientInformationFull, "client_id" | "client_id_issued_at"> }>(clientId, "client", this.signingKey);
      return { ...payload.metadata, client_id: clientId, client_id_issued_at: payload.iat };
    } catch {
      return undefined;
    }
  }
}

const escapeHTML = (value: string): string => value
  .replaceAll("&", "&amp;")
  .replaceAll("<", "&lt;")
  .replaceAll(">", "&gt;")
  .replaceAll('"', "&quot;")
  .replaceAll("'", "&#39;");

function authorizationPage(params: AuthorizationParams, client: OAuthClientInformationFull, invalid: boolean): string {
  const fields: Record<string, string> = {
    response_type: "code",
    client_id: client.client_id,
    redirect_uri: params.redirectUri,
    code_challenge: params.codeChallenge,
    code_challenge_method: "S256",
  };
  if (params.state) fields.state = params.state;
  if (params.scopes?.length) fields.scope = params.scopes.join(" ");
  if (params.resource) fields.resource = params.resource.href;
  const hidden = Object.entries(fields).map(([name, value]) => `<input type="hidden" name="${escapeHTML(name)}" value="${escapeHTML(value)}">`).join("");
  const error = invalid ? '<p role="alert" class="error">授权码不正确，请重新输入。</p>' : "";
  return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>链上数据平台授权</title><style>body{margin:0;background:#f5f7fb;color:#111827;font:16px/1.55 system-ui,-apple-system,"Segoe UI",sans-serif}.card{max-width:440px;margin:10vh auto;padding:32px;border:1px solid #d1d5db;border-radius:18px;background:#fff;box-shadow:0 18px 50px rgba(15,23,42,.12)}h1{margin:0 0 12px;font-size:24px}p{margin:8px 0 20px}.error{font-weight:600}label{display:block;margin-bottom:8px;font-weight:650}input[type=password]{box-sizing:border-box;width:100%;padding:12px 14px;border:1px solid #9ca3af;border-radius:10px;font:inherit}button{width:100%;margin-top:18px;padding:12px;border:0;border-radius:10px;background:#1457d9;color:#fff;font:700 16px system-ui;cursor:pointer}small{display:block;margin-top:18px;color:#374151}</style></head><body><main class="card"><h1>链上数据平台授权</h1><p>允许 ChatGPT 调用链上调查、覆盖检查、下载补齐和资金追踪工具。</p>${error}<form method="post" action="/authorize" autocomplete="off">${hidden}<label for="access_code">MCP 授权码</label><input id="access_code" name="access_code" type="password" required autofocus autocomplete="off"><button type="submit">授权并继续</button></form><small>授权码只用于本次服务端验证，不会写入插件包或返回给 ChatGPT。</small></main></body></html>`;
}

export class ChainOAuthProvider implements OAuthServerProvider {
  readonly clientsStore: OAuthRegisteredClientsStore;
  private readonly codes = new Map<string, CodeRecord>();
  private readonly revoked = new Set<string>();

  constructor(private readonly config: Config) {
    this.clientsStore = new StatelessClientsStore(config.oauthSigningKey);
  }

  async authorize(client: OAuthClientInformationFull, params: AuthorizationParams, res: Response): Promise<void> {
    if (!client.redirect_uris.includes(params.redirectUri)) throw new InvalidRequestError("Unregistered redirect_uri");
    const resource = params.resource?.href ?? this.config.publicMcpUrl;
    if (resource !== this.config.publicMcpUrl) throw new InvalidRequestError("Invalid resource");
    const scopes = params.scopes?.length ? params.scopes : ["mcp:tools"];
    if (scopes.some((scope) => scope !== "mcp:tools")) throw new InvalidRequestError("Unsupported scope");
    const submitted = typeof res.req.body?.access_code === "string" ? res.req.body.access_code.trim() : "";
    if (!submitted || !authorized(`Bearer ${submitted}`, this.config.oauthAccessCodeHashes)) {
      res.status(submitted ? 401 : 200);
      res.set({
        "Cache-Control": "no-store",
        "Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' https://chatgpt.com; frame-ancestors 'none'; base-uri 'none'",
        "Referrer-Policy": "no-referrer",
        "X-Content-Type-Options": "nosniff",
        "X-Frame-Options": "DENY",
      });
      res.type("html").send(authorizationPage(params, client, Boolean(submitted)));
      return;
    }
    const code = randomBytes(32).toString("base64url");
    this.codes.set(code, {
      clientId: client.client_id,
      codeChallenge: params.codeChallenge,
      redirectUri: params.redirectUri,
      resource,
      scopes,
      subject: "mcp-authorized-user",
      expiresAt: now() + 5 * 60,
    });
    const target = new URL(params.redirectUri);
    target.searchParams.set("code", code);
    if (params.state) target.searchParams.set("state", params.state);
    res.redirect(302, target.href);
  }

  async challengeForAuthorizationCode(client: OAuthClientInformationFull, authorizationCode: string): Promise<string> {
    const record = this.codes.get(authorizationCode);
    if (!record || record.clientId !== client.client_id || record.expiresAt <= now()) throw new InvalidGrantError("Invalid authorization code");
    return record.codeChallenge;
  }

  async exchangeAuthorizationCode(client: OAuthClientInformationFull, authorizationCode: string, _codeVerifier?: string, redirectUri?: string, resource?: URL): Promise<OAuthTokens> {
    const record = this.codes.get(authorizationCode);
    this.codes.delete(authorizationCode);
    if (!record || record.clientId !== client.client_id || record.expiresAt <= now()) throw new InvalidGrantError("Invalid authorization code");
    if (redirectUri && redirectUri !== record.redirectUri) throw new InvalidGrantError("redirect_uri mismatch");
    if (resource && resource.href !== record.resource) throw new InvalidGrantError("resource mismatch");
    return this.issueTokens(client.client_id, record.subject, record.scopes, record.resource);
  }

  async exchangeRefreshToken(client: OAuthClientInformationFull, refreshToken: string, scopes?: string[], resource?: URL): Promise<OAuthTokens> {
    if (this.revoked.has(sha256(refreshToken).toString("hex"))) throw new InvalidGrantError("Refresh token revoked");
    const payload = verify<SignedPayload & { cid: string; sub: string; scopes: string[]; resource: string }>(refreshToken, "refresh", this.config.oauthSigningKey);
    if (payload.cid !== sha256(client.client_id).toString("hex")) throw new InvalidGrantError("Refresh token client mismatch");
    if (resource && resource.href !== payload.resource) throw new InvalidGrantError("resource mismatch");
    const requested = scopes?.length ? scopes : payload.scopes;
    if (requested.some((scope) => !payload.scopes.includes(scope))) throw new InvalidGrantError("scope escalation is not allowed");
    return this.issueTokens(client.client_id, payload.sub, requested, payload.resource);
  }

  async verifyAccessToken(token: string): Promise<AuthInfo> {
    if (this.revoked.has(sha256(token).toString("hex"))) throw new InvalidGrantError("Access token revoked");
    const payload = verify<SignedPayload & { cid: string; sub: string; scopes: string[]; resource: string }>(token, "access", this.config.oauthSigningKey);
    if (payload.resource !== this.config.publicMcpUrl) throw new InvalidGrantError("Access token audience mismatch");
    return {
      token,
      clientId: payload.cid,
      scopes: payload.scopes,
      expiresAt: payload.exp,
      resource: new URL(payload.resource),
      extra: { subject: payload.sub },
    };
  }

  async revokeToken(_client: OAuthClientInformationFull, request: OAuthTokenRevocationRequest): Promise<void> {
    this.revoked.add(sha256(request.token).toString("hex"));
  }

  private issueTokens(clientId: string, subject: string, scopes: string[], resource: string): OAuthTokens {
    const issuedAt = now();
    const cid = sha256(clientId).toString("hex");
    const access = sign("access", { typ: "access", iat: issuedAt, exp: issuedAt + 60 * 60, cid, sub: subject, scopes, resource }, this.config.oauthSigningKey);
    const refresh = sign("refresh", { typ: "refresh", iat: issuedAt, exp: issuedAt + 30 * 24 * 60 * 60, cid, sub: subject, scopes, resource }, this.config.oauthSigningKey);
    return { access_token: access, refresh_token: refresh, token_type: "bearer", expires_in: 3600, scope: scopes.join(" ") };
  }
}
