import { strict as assert } from "node:assert";
import { test } from "node:test";
import { APIError, createAPI } from "./api.ts";

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
    api("/database", {}, "database"),
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
  const blob = await api<Blob>("/database", {}, "database");
  assert.deepEqual(new Uint8Array(await blob.arrayBuffer()), bytes);
});

test("media uploads preserve the browser multipart boundary and media downloads accept image blobs", async () => {
  const requests: RequestInit[] = [];
  const api = createAPI(
    "token",
    () => {},
    async (_input, init) => {
      requests.push(init || {});
      if (init?.method === "POST")
        return Response.json({ itemId: "word", images: [] });
      return new Response(new Uint8Array([0x89, 0x50, 0x4e, 0x47]), {
        headers: { "Content-Type": "image/png" },
      });
    },
  );
  const form = new FormData();
  form.append("expectedRevision", "1");
  form.append(
    "image",
    new Blob([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], {
      type: "image/png",
    }),
    "example.png",
  );
  await api("/vocabulary/word/images", { method: "POST", body: form });
  const media = await api<Blob>("/media/hash", {}, "blob");
  assert.equal(new Headers(requests[0].headers).has("Content-Type"), false);
  assert.equal(new Headers(requests[0].headers).get("Accept"), "application/json");
  assert.equal(new Headers(requests[1].headers).get("Accept"), "image/*,audio/*");
  assert.equal(media.type, "image/png");
});

test("stale edit failures preserve the session and expose a conflict without retrying", async () => {
  let requests = 0;
  let invalidated = false;
  const api = createAPI(
    "token",
    () => {
      invalidated = true;
    },
    async () => {
      requests++;
      return Response.json(
        { error: "Vocabulary changed; reload before editing" },
        { status: 409 },
      );
    },
  );
  await assert.rejects(
    api("/vocabulary/word-one", {
      method: "PATCH",
      body: JSON.stringify({ expectedRevision: 4, personalInterest: "high" }),
    }),
    (error: unknown) => error instanceof APIError && error.status === 409,
  );
  assert.equal(requests, 1);
  assert.equal(invalidated, false);
});
