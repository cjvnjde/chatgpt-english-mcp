import { createAPI } from "./api.ts";
import type { Session } from "./types.ts";

export const rememberedTokenKey = "english-mcp.admin.token";
type TokenStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;

export function createAuth(
  storage: () => TokenStorage = () => window.localStorage,
  fetcher: typeof fetch = fetch,
) {
  const rememberedToken = () => {
    try {
      return storage().getItem(rememberedTokenKey) || "";
    } catch {
      return "";
    }
  };
  const forget = () => {
    try {
      storage().removeItem(rememberedTokenKey);
      return true;
    } catch {
      return false;
    }
  };
  const signIn = async (candidate: string, remember: boolean) => {
    const token = candidate.trim();
    const session = await createAPI(
      token,
      () => {
        forget();
      },
      fetcher,
    )<Session>("/session");
    if (session.version !== 1) {
      throw new Error(
        "Unsupported admin API version. Update the UI and MCP service together.",
      );
    }
    let storageAvailable = true;
    try {
      if (remember) storage().setItem(rememberedTokenKey, token);
      else storage().removeItem(rememberedTokenKey);
    } catch {
      storageAvailable = false;
    }
    return { session, token, storageAvailable };
  };
  return { rememberedToken, signIn, forget };
}
