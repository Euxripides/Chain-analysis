export type Config = {
    host: string;
    allowedHosts: string[];
    port: number;
    restBaseUrl: string;
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
export declare const sha256: (value: string) => Buffer;
export declare function loadConfig(): Config;
export declare function authorized(header: string | undefined, expected: Buffer[]): boolean;
