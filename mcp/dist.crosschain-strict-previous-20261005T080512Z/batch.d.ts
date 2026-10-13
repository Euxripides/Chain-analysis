import { z } from "zod";
import { RESTClient, type RESTEnvelope } from "./rest-client.js";
export type ToolDefinition = {
    name: string;
    title: string;
    description: string;
    path: string;
    method?: "GET" | "POST";
    readOnly?: boolean;
    inputSchema: Record<string, z.ZodTypeAny> | z.ZodTypeAny;
    summarize: (result: RESTEnvelope) => string;
    batch?: BatchSpec;
};
type BatchSpec = {
    kind: "addresses";
    operation: ToolDefinition;
    key: "address" | "source" | "tx_hash";
} | {
    kind: "calls";
    operations: Map<string, ToolDefinition>;
};
export type BatchItem = {
    index: number;
    tool: string;
    address?: string;
    input: Record<string, unknown>;
    success: boolean;
    started?: boolean;
    response?: RESTEnvelope;
    error?: {
        code: string;
        message: string;
    };
};
export declare function operationSchema(definition: ToolDefinition): z.ZodTypeAny;
export declare function createBatchTools(base: ToolDefinition[]): ToolDefinition[];
export declare function executeBatch(client: RESTClient, spec: BatchSpec, input: Record<string, unknown>, validate: (name: string, input: Record<string, unknown>) => void, options?: {
    signal?: AbortSignal;
    budgetMs?: number;
    responseLimitBytes?: number;
    processResult?: (item: BatchItem) => void;
    discardResult?: (item: BatchItem) => void;
}): Promise<{
    envelope: RESTEnvelope;
    items: BatchItem[];
}>;
export {};
