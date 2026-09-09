export class APIError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

export function createAPI(
  token: string,
  onUnauthorized: () => void,
  fetcher: typeof fetch = fetch,
) {
  return async function request<T>(
    path: string,
    options: RequestInit = {},
    responseType: "json" | "blob" = "json",
  ): Promise<T> {
    const headers = new Headers(options.headers);
    headers.set("Authorization", `Bearer ${token}`);
    headers.set(
      "Accept",
      responseType === "blob" ? "application/vnd.sqlite3" : "application/json",
    );
    if (options.body) headers.set("Content-Type", "application/json");
    let response: Response;
    try {
      response = await fetcher(`/admin/api${path}`, {
        ...options,
        headers,
        cache: "no-store",
        credentials: "omit",
        redirect: "error",
        signal: options.signal ?? AbortSignal.timeout(20000),
      });
    } catch (error) {
      if (error instanceof Error && error.name === "AbortError") throw error;
      throw new Error(
        "Cannot reach the admin API. Check the MCP service and nginx upstream.",
      );
    }
    if (response.status === 401) onUnauthorized();
    if (response.ok && responseType === "blob") {
      if (
        !response.headers
          .get("Content-Type")
          ?.includes("application/vnd.sqlite3")
      )
        throw new Error(
          "Expected a SQLite database. Check your reverse proxy configuration.",
        );
      return (await response.blob()) as T;
    }
    const isJSON = response.headers
      .get("Content-Type")
      ?.includes("application/json");
    const payload = isJSON ? await response.json() : null;
    if (!response.ok)
      throw new APIError(
        payload?.error ||
          `API returned HTTP ${response.status}. Check that ADMIN_BEARER_TOKEN is configured on the MCP service.`,
        response.status,
      );
    if (!isJSON)
      throw new Error(
        "Expected JSON from /admin/api. Check your reverse proxy configuration.",
      );
    return payload as T;
  };
}
export type API = ReturnType<typeof createAPI>;
