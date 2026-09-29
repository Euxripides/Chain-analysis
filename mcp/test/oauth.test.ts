import assert from "node:assert/strict";
import { createServer as createHTTPServer, type Server } from "node:http";
import { once } from "node:events";
import type { AddressInfo } from "node:net";
import { createHash, randomBytes } from "node:crypto";
import test, { after, before } from "node:test";
import { createApp } from "../src/server.js";
import { sha256, type Config } from "../src/config.js";

const mcpKey = "test-mcp-oauth-code";
let server: Server;
let base: URL;
const publicBaseUrl = "http://127.0.0.1";
const publicMcpUrl = `${publicBaseUrl}/mcp`;

before(async () => {
  const config: Config = {
    host: "127.0.0.1",
    allowedHosts: ["127.0.0.1", "localhost"],
    port: 0,
    restBaseUrl: "http://127.0.0.1:1/api/v1",
    restApiKey: "unused",
    mcpKeyHashes: [sha256(mcpKey)],
    oauthAccessCodeHashes: [sha256(mcpKey)],
    oauthSigningKey: sha256("oauth-test-signing-key"),
    publicBaseUrl,
    publicMcpUrl,
    requestTimeoutMs: 1_000,
    maxConcurrent: 4,
    rateLimitPerMinute: 100,
  };
  server = createHTTPServer(createApp(config));
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  base = new URL(`http://127.0.0.1:${(server.address() as AddressInfo).port}`);
});

after(async () => {
  server.close();
  await once(server, "close");
});

test("OAuth discovery is exposed and MCP advertises protected resource metadata", async () => {
  const metadata = await fetch(new URL("/.well-known/oauth-authorization-server", base));
  assert.equal(metadata.status, 200);
  const body = await metadata.json() as Record<string, unknown>;
  assert.equal(body.issuer, `${publicBaseUrl}/`);
  assert.equal(body.authorization_endpoint, `${publicBaseUrl}/authorize`);
  assert.equal(body.token_endpoint, `${publicBaseUrl}/token`);
  assert.equal(body.registration_endpoint, `${publicBaseUrl}/register`);
  assert.deepEqual(body.code_challenge_methods_supported, ["S256"]);

  const rejected = await fetch(new URL("/mcp", base), { method: "POST", headers: { "content-type": "application/json" }, body: "{}" });
  assert.equal(rejected.status, 401);
  assert.match(rejected.headers.get("www-authenticate") ?? "", /resource_metadata="http:\/\/127\.0\.0\.1\/\.well-known\/oauth-protected-resource\/mcp"/);
});

test("OAuth authorization code with PKCE produces an MCP access token", async () => {
  const callback = "https://chatgpt.com/connector_platform_oauth_redirect";
  const registered = await fetch(new URL("/register", base), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      redirect_uris: [callback],
      token_endpoint_auth_method: "none",
      grant_types: ["authorization_code", "refresh_token"],
      response_types: ["code"],
      client_name: "OAuth test",
    }),
  });
  assert.equal(registered.status, 201);
  const client = await registered.json() as { client_id: string };
  assert.ok(client.client_id.startsWith("client."));

  const verifier = randomBytes(32).toString("base64url");
  const challenge = createHash("sha256").update(verifier).digest("base64url");
  const authorization = new URL("/authorize", base);
  authorization.searchParams.set("response_type", "code");
  authorization.searchParams.set("client_id", client.client_id);
  authorization.searchParams.set("redirect_uri", callback);
  authorization.searchParams.set("code_challenge", challenge);
  authorization.searchParams.set("code_challenge_method", "S256");
  authorization.searchParams.set("scope", "mcp:tools");
  authorization.searchParams.set("resource", publicMcpUrl);
  authorization.searchParams.set("state", "test-state");

  const page = await fetch(authorization);
  assert.equal(page.status, 200);
  assert.match(page.headers.get("content-security-policy") ?? "", /form-action 'self' https:\/\/chatgpt\.com(?:;|$)/);
  const html = await page.text();
  assert.match(html, /MCP 授权码/);
  assert.doesNotMatch(html, new RegExp(mcpKey));

  const form = new URLSearchParams(authorization.searchParams);
  form.set("access_code", mcpKey);
  const approved = await fetch(new URL("/authorize", base), {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: form,
    redirect: "manual",
  });
  assert.equal(approved.status, 302);
  const redirect = new URL(approved.headers.get("location") ?? "");
  assert.equal(redirect.origin + redirect.pathname, callback);
  assert.equal(redirect.searchParams.get("state"), "test-state");
  const code = redirect.searchParams.get("code");
  assert.ok(code);

  const token = await fetch(new URL("/token", base), {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "authorization_code",
      client_id: client.client_id,
      code: code!,
      code_verifier: verifier,
      redirect_uri: callback,
      resource: publicMcpUrl,
    }),
  });
  assert.equal(token.status, 200);
  const tokens = await token.json() as { access_token: string; refresh_token: string; token_type: string; scope: string };
  assert.equal(tokens.token_type, "bearer");
  assert.equal(tokens.scope, "mcp:tools");
  assert.ok(tokens.access_token.startsWith("access."));
  assert.ok(tokens.refresh_token.startsWith("refresh."));

  const initialized = await fetch(new URL("/mcp", base), {
    method: "POST",
    headers: {
      authorization: `Bearer ${tokens.access_token}`,
      accept: "application/json, text/event-stream",
      "content-type": "application/json",
    },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-03-26", capabilities: {}, clientInfo: { name: "oauth-test", version: "1.0" } } }),
  });
  assert.equal(initialized.status, 200);
});
