import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createAPI } from "./api.ts";

test("auth is sent in a header and private requests never use cookies, redirects, or cache", async () => {
  const fakeFetch = (async (input, init) => {
    assert.equal(input, "/admin/api/session");
    assert.equal(
      new Headers(init?.headers).get("Authorization"),
      "Bearer private-token",
    );
    assert.equal(init?.credentials, "omit");
    assert.equal(init?.cache, "no-store");
    assert.equal(init?.redirect, "error");
    return Response.json({ owner: "default", version: 1 });
  }) as typeof fetch;
  const session = await createAPI(
    "private-token",
    () => {},
    fakeFetch,
  )("/session");
  assert.deepEqual(session, { owner: "default", version: 1 });
});

test("401 invalidates the session and surfaces the error", async () => {
  let invalidated = false;
  const api = createAPI(
    "bad",
    () => {
      invalidated = true;
    },
    async () =>
      Response.json({ error: "Invalid admin token" }, { status: 401 }),
  );
  await assert.rejects(api("/tables"), /Invalid admin token/);
  assert.equal(invalidated, true);
});

test("nginx HTML fallback is reported as a configuration error", async () => {
  const api = createAPI(
    "token",
    () => {},
    async () =>
      new Response("<html>", { headers: { "Content-Type": "text/html" } }),
  );
  await assert.rejects(api("/tables"), /Expected JSON/);
  await assert.rejects(
    api("/database", {}, "blob"),
    /Expected a SQLite database/,
  );
});

test("SQLite export returns the binary response intact", async () => {
  const bytes = new TextEncoder().encode("SQLite format 3\0test");
  const api = createAPI(
    "token",
    () => {},
    async () =>
      new Response(bytes, {
        headers: { "Content-Type": "application/vnd.sqlite3" },
      }),
  );
  const blob = await api<Blob>("/database", {}, "blob");
  assert.deepEqual(new Uint8Array(await blob.arrayBuffer()), bytes);
});
