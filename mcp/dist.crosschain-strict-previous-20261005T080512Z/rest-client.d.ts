export type RESTEnvelope = {
    success: boolean;
    data: unknown;
    meta: Record<string, unknown>;
    error: null | {
        code: string;
        message: string;
    };
};
export type RESTClientOptions = {
    baseUrl: string;
    apiKey: string;
    timeoutMs: number;
    signal?: AbortSignal;
    maxResponseBytes?: number;
};
export type RESTRequestOptions = {
    signal?: AbortSignal;
};
export declare function toQuery(args: Record<string, unknown>): string;
export declare class RESTClient {
    private readonly options;
    constructor(options: RESTClientOptions);
    get timeoutMs(): number;
    get signal(): AbortSignal | undefined;
    get(path: string, args: Record<string, unknown>, options?: RESTRequestOptions): Promise<RESTEnvelope>;
    post(path: string, args: Record<string, unknown>, options?: RESTRequestOptions): Promise<RESTEnvelope>;
    private request;
    private readBody;
}
