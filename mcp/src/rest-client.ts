export type RESTEnvelope = {
  success: boolean;
  data: unknown;
  meta: Record<string, unknown>;
  error: null | { code: string; message: string };
};

export type RESTClientOptions = {
  baseUrl: string;
  apiKey: string;
  timeoutMs: number;
};

export function toQuery(args: Record<string, unknown>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(args)) {
    if (value === undefined || value === null || value === "" || value === "all") continue;
    query.set(key, String(value));
  }
  return query.toString();
}

export class RESTClient {
  constructor(private readonly options: RESTClientOptions) {}

  async get(path: string, args: Record<string, unknown>): Promise<RESTEnvelope> {
    const query = toQuery(args);
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.options.timeoutMs);
    try {
      const response = await fetch(`${this.options.baseUrl}${path}${query ? `?${query}` : ""}`, {
        headers: {
          authorization: `Bearer ${this.options.apiKey}`,
          accept: "application/json",
          "user-agent": "blockchain-platform-mcp/1.0.0",
        },
        signal: controller.signal,
      });
      const body = (await response.json()) as RESTEnvelope;
      if (!response.ok || !body.success) {
        throw new Error(`${body.error?.code ?? `HTTP_${response.status}`}: ${body.error?.message ?? "REST request failed"}`);
      }
      return body;
    } finally {
      clearTimeout(timer);
    }
  }

  async post(path: string, args: Record<string, unknown>): Promise<RESTEnvelope> {
    return this.request(path, {
      method: "POST",
      headers: {
        authorization: `Bearer ${this.options.apiKey}`,
        "content-type": "application/json",
      },
      body: JSON.stringify(args),
    });
  }

  private async request(path: string, init: RequestInit): Promise<RESTEnvelope> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.options.timeoutMs);
    try {
      const response = await fetch(`${this.options.baseUrl}${path}`, { ...init, signal: controller.signal });
      const body = (await response.json()) as RESTEnvelope;
      if (!response.ok || !body.success) {
        throw new Error(`${body.error?.code ?? `HTTP_${response.status}`}: ${body.error?.message ?? "REST request failed"}`);
      }
      return body;
    } finally {
      clearTimeout(timer);
    }
  }
}
