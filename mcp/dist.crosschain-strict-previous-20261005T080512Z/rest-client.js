export function toQuery(args) {
    const query = new URLSearchParams();
    for (const [key, value] of Object.entries(args)) {
        if (value === undefined || value === null || value === "" || value === "all")
            continue;
        query.set(key, String(value));
    }
    return query.toString();
}
export class RESTClient {
    options;
    constructor(options) {
        this.options = options;
    }
    get timeoutMs() { return this.options.timeoutMs; }
    get signal() { return this.options.signal; }
    async get(path, args, options = {}) {
        const query = toQuery(args);
        return this.request(`${path}${query ? `?${query}` : ""}`, {
            headers: {
                authorization: `Bearer ${this.options.apiKey}`,
                accept: "application/json",
                "user-agent": "blockchain-platform-mcp/1.0.0",
            },
        }, options);
    }
    async post(path, args, options = {}) {
        return this.request(path, {
            method: "POST",
            headers: {
                authorization: `Bearer ${this.options.apiKey}`,
                "content-type": "application/json",
                accept: "application/json",
                "user-agent": "blockchain-platform-mcp/1.0.0",
            },
            body: JSON.stringify(args),
        }, options);
    }
    async request(path, init, options) {
        const controller = new AbortController();
        const listeners = [];
        for (const signal of new Set([this.options.signal, options.signal])) {
            if (!signal)
                continue;
            const abort = () => controller.abort(signal.reason instanceof Error && /^[A-Z][A-Z0-9_]+:/.test(signal.reason.message)
                ? signal.reason : new Error("REQUEST_CANCELLED: REST request cancelled"));
            if (signal.aborted)
                abort();
            else {
                signal.addEventListener("abort", abort, { once: true });
                listeners.push(() => signal.removeEventListener("abort", abort));
            }
        }
        const timer = setTimeout(() => controller.abort(new Error("REST_TIMEOUT: REST request exceeded its timeout")), this.options.timeoutMs);
        try {
            controller.signal.throwIfAborted();
            const response = await fetch(`${this.options.baseUrl}${path}`, { ...init, signal: controller.signal });
            const body = await this.readBody(response);
            if (!response.ok || !body.success) {
                throw new Error(`${body.error?.code ?? `HTTP_${response.status}`}: ${body.error?.message ?? "REST request failed"}`);
            }
            return body;
        }
        catch (error) {
            if (error instanceof Error && error.message.startsWith("REST_RESPONSE_TOO_LARGE:"))
                throw error;
            if (controller.signal.aborted)
                throw controller.signal.reason;
            throw error;
        }
        finally {
            clearTimeout(timer);
            for (const remove of listeners)
                remove();
        }
    }
    async readBody(response) {
        const maximum = 8 << 20;
        const override = this.options.maxResponseBytes;
        const limit = Number.isFinite(override) ? Math.max(1, Math.min(maximum, Math.floor(override))) : maximum;
        const tooLarge = () => new Error(`REST_RESPONSE_TOO_LARGE: REST JSON response exceeds ${limit} bytes`);
        // Content-Length permits an early rejection, but compressed/chunked or
        // misleading lengths still require counting the actual decoded stream.
        const declared = response.headers.get("content-length");
        if (declared && Number(declared) > limit) {
            await response.body?.cancel().catch(() => { });
            throw tooLarge();
        }
        if (!response.body)
            throw new Error("REST_INVALID_RESPONSE: REST response has no JSON body");
        const reader = response.body.getReader();
        const chunks = [];
        let size = 0, complete = false;
        try {
            for (;;) {
                const { value, done } = await reader.read();
                if (done) {
                    complete = true;
                    break;
                }
                size += value.byteLength;
                if (size > limit)
                    throw tooLarge();
                chunks.push(value);
            }
            let parsed;
            const invalid = () => new Error(`REST_INVALID_RESPONSE: HTTP ${response.status} did not return a valid REST JSON envelope`);
            try {
                parsed = JSON.parse(Buffer.concat(chunks, size).toString("utf8"));
            }
            catch {
                throw invalid();
            }
            if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
                throw invalid();
            const envelope = parsed;
            if (typeof envelope.success !== "boolean" || !envelope.meta || typeof envelope.meta !== "object" || Array.isArray(envelope.meta)
                || !("data" in envelope) || !("error" in envelope))
                throw invalid();
            if (envelope.error !== null && (typeof envelope.error !== "object" || typeof envelope.error.code !== "string" || typeof envelope.error.message !== "string"))
                throw invalid();
            return envelope;
        }
        finally {
            if (!complete)
                await reader.cancel().catch(() => { });
            reader.releaseLock();
        }
    }
}
//# sourceMappingURL=rest-client.js.map