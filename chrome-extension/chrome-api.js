// Shared Firefox UI/controller compatibility. Loaded before any shared scripts.
(() => {
  const openWindows = new Set();
  const messageListeners = new Map();
  const onMessage = {
    addListener(listener) {
      const wrapped = (message, sender, sendResponse) => {
        const result = listener(message, sender);
        if (result?.then) {
          result.then(sendResponse, error => sendResponse({ ok: false, error: error.message }));
          return true;
        }
        if (result !== undefined) sendResponse(result);
        return false;
      };
      messageListeners.set(listener, wrapped);
      chrome.runtime.onMessage.addListener(wrapped);
    },
    removeListener(listener) {
      const wrapped = messageListeners.get(listener);
      if (wrapped) chrome.runtime.onMessage.removeListener(wrapped);
      messageListeners.delete(listener);
    },
  };
  function open(options = {}) {
    // Call synchronously from the gesture handler, before any storage awaits.
    const target = Number.isInteger(options.windowId)
      ? options : { windowId: chrome.windows.WINDOW_ID_CURRENT };
    return chrome.sidePanel.open(target).then(() => {
      if (target.windowId >= 0) openWindows.add(target.windowId);
    });
  }
  if (chrome.sidePanel) {
    chrome.sidePanel.onOpened.addListener(({ windowId }) => openWindows.add(windowId));
    chrome.sidePanel.onClosed.addListener(({ windowId }) => openWindows.delete(windowId));
    chrome.windows.onRemoved.addListener(windowId => openWindows.delete(windowId));
  }
  globalThis.browser = {
    isChrome: true,
    runtime: {
      id: chrome.runtime.id,
      getURL: path => chrome.runtime.getURL(path),
      sendMessage: message => chrome.runtime.sendMessage(message),
      connect: options => chrome.runtime.connect(options),
      openOptionsPage: () => chrome.runtime.openOptionsPage(),
      getPlatformInfo: () => chrome.runtime.getPlatformInfo(),
      onConnect: chrome.runtime.onConnect,
      onMessage,
    },
    storage: chrome.storage,
    tabs: chrome.tabs,
    windows: chrome.windows,
    commands: chrome.commands,
    browserAction: chrome.action,
    menus: chrome.contextMenus && {
      // Context menus persist across worker restarts; create only on install/update.
      create: options => chrome.runtime.onInstalled.addListener(() => {
        chrome.contextMenus.removeAll(() => chrome.contextMenus.create(options));
      }),
      onClicked: chrome.contextMenus.onClicked,
    },
    chromePanelEvents: {
      onOpened: chrome.sidePanel?.onOpened,
      onClosed: chrome.sidePanel?.onClosed,
    },
    sidebarAction: {
      open,
      isOpen: async ({ windowId }) => openWindows.has(windowId),
      toggle: (options = {}) => openWindows.has(options.windowId)
        ? chrome.sidePanel.close(options).then(() => openWindows.delete(options.windowId))
        : open(options),
    },
  };
})();
