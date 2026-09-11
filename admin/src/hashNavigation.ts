import type { LeaveGuard } from "./vocabularyDraft";

type Browser = {
  location: Pick<Location, "hash" | "pathname" | "search">;
  history: Pick<History, "state" | "replaceState" | "pushState" | "go">;
};
const positionKey = "englishMcpNavigationPosition";

export function createHashNavigation(
  browser: Browser,
  requestLeave: LeaveGuard,
  accept: (hash: string) => void,
) {
  const { location, history } = browser;
  const position = (): number | undefined => {
    const value = history.state?.[positionKey];
    return Number.isSafeInteger(value) ? value : undefined;
  };
  const stateAt = (index: number) => ({
    ...history.state,
    [positionKey]: index,
  });
  const url = (hash: string) => `${location.pathname}${location.search}${hash}`;
  let currentHash = location.hash;
  let currentPosition = position() ?? 0;
  history.replaceState(stateAt(currentPosition), "");
  let generation = 0;
  let dismissPending: void | (() => void);
  const supersede = () => {
    generation++;
    dismissPending?.();
    dismissPending = undefined;
    return generation;
  };

  const onHash = () => {
    // Even a return to the displayed hash supersedes an earlier Back decision.
    const attempt = supersede();
    const nextHash = location.hash;
    const nextPosition = position();
    if (nextHash === currentHash) {
      if (nextPosition !== undefined) currentPosition = nextPosition;
      return;
    }
    // Traversal has already moved the browser. Keep the current workspace/draft
    // mounted during confirmation, but never rewrite the visited entry or push
    // a replacement. Cancel traverses back to the original history position.
    dismissPending = requestLeave(
      () => {
        if (attempt !== generation) return;
        currentHash = nextHash;
        currentPosition = nextPosition ?? currentPosition + 1;
        if (nextPosition === undefined)
          history.replaceState(stateAt(currentPosition), "");
        accept(nextHash);
      },
      () => {
        if (attempt !== generation) return;
        if (nextPosition !== undefined && nextPosition !== currentPosition) {
          history.go(currentPosition - nextPosition);
        } else {
          // Manually entered hashes/foreign entries have no known position.
          history.replaceState(stateAt(currentPosition), "", url(currentHash));
        }
      },
    );
  };
  const navigate = (hash: string) => {
    const attempt = supersede();
    dismissPending = requestLeave(() => {
      if (attempt !== generation) return;
      if (hash !== currentHash) {
        currentPosition++;
        history.pushState(stateAt(currentPosition), "", url(hash));
        currentHash = hash;
      }
      accept(hash);
    });
  };
  return { onHash, navigate };
}
