import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createAuth, rememberedTokenKey } from "./auth.ts";

function memoryStorage() {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
    removeItem: (key: string) => {
      values.delete(key);
    },
  };
}
const accepted = async () => Response.json({ owner: "default", version: 1 });

test("remembered login survives a new app instance and verifies with the server again", async () => {
  const storage = memoryStorage();
  let verified = 0;
  const fetcher: typeof fetch = async (url, options) => {
    assert.equal(url, "/admin/api/session");
    assert.equal(
      new Headers(options?.headers).get("Authorization"),
      "Bearer valid-token",
    );
    if (verified === 0) assert.equal(storage.getItem(rememberedTokenKey), null);
    verified++;
    return accepted();
  };
  const first = createAuth(() => storage, fetcher);
  await first.signIn(" valid-token ", true);
  const reopened = createAuth(() => storage, fetcher);
  const restored = await reopened.signIn(reopened.rememberedToken(), true);
  assert.equal(restored.session.owner, "default");
  assert.equal(verified, 2);
  assert.equal(reopened.rememberedToken(), "valid-token");
});

test("unchecked login and explicit sign-out remove the remembered token", async () => {
  const storage = memoryStorage();
  storage.setItem("unrelated-preference", "keep");
  const auth = createAuth(() => storage, accepted);
  await auth.signIn("token", true);
  await auth.signIn("token", false);
  assert.equal(auth.rememberedToken(), "");
  await auth.signIn("token", true);
  assert.equal(auth.forget(), true);
  assert.equal(auth.rememberedToken(), "");
  assert.equal(storage.getItem("unrelated-preference"), "keep");
});

test("invalid remembered credentials are removed and never restored", async () => {
  const storage = memoryStorage();
  storage.setItem(rememberedTokenKey, "revoked");
  const auth = createAuth(
    () => storage,
    async () =>
      Response.json({ error: "Invalid admin token" }, { status: 401 }),
  );
  await assert.rejects(
    auth.signIn(auth.rememberedToken(), true),
    /Invalid admin token/,
  );
  assert.equal(auth.rememberedToken(), "");
});

test("temporary server failures preserve remembered credentials for retry", async () => {
  const storage = memoryStorage();
  storage.setItem(rememberedTokenKey, "valid-token");
  const auth = createAuth(
    () => storage,
    async () =>
      Response.json({ error: "Temporarily unavailable" }, { status: 503 }),
  );
  await assert.rejects(
    auth.signIn(auth.rememberedToken(), true),
    /Temporarily unavailable/,
  );
  assert.equal(auth.rememberedToken(), "valid-token");
});

test("blocked browser storage allows login for the current tab", async () => {
  const auth = createAuth(() => {
    throw new Error("Storage blocked");
  }, accepted);
  assert.equal(auth.rememberedToken(), "");
  const result = await auth.signIn("token", true);
  assert.equal(result.session.owner, "default");
  assert.equal(result.storageAvailable, false);
  assert.equal(auth.forget(), false);
});
