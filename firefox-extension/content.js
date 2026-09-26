(() => {
  let selectedRange = null;
  let selectedTerm = '';
  let selectionSourceId = '';
  let selectionUrl = '';
  let host = null;
  let selectionTimer;
  let requestId = 0;
  let pendingRequest = null;
  const privateSelector = 'input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"]';

  function hide(clearTimer = true) {
    requestId += 1;
    pendingRequest = null;
    if (clearTimer) clearTimeout(selectionTimer);
    host?.style.setProperty('display', 'none', 'important');
  }

  function matchesSelection(selection) {
    if (!selectedRange || !selection?.rangeCount || selection.isCollapsed) return false;
    const range = selection.getRangeAt(0);
    return range.startContainer === selectedRange.startContainer
      && range.startOffset === selectedRange.startOffset
      && range.endContainer === selectedRange.endContainer
      && range.endOffset === selectedRange.endOffset
      && selection.toString().replace(/\s+/gu, ' ').trim() === selectedTerm;
  }

  function cacheSelection(selection) {
    if (matchesSelection(selection)) return;
    selectedRange = selection.getRangeAt(0).cloneRange();
    selectedTerm = selection.toString().replace(/\s+/gu, ' ').trim();
    selectionSourceId = crypto.getRandomValues(new Uint32Array(4)).join('-');
    selectionUrl = location.href;
  }

  function positionPopup(popup, anchor) {
    const margin = 8;
    const bounds = popup.getBoundingClientRect();
    const left = Math.max(margin, Math.min(anchor.right - bounds.width / 2, innerWidth - bounds.width - margin));
    const below = anchor.bottom + 5;
    const top = below + bounds.height <= innerHeight - margin ? below : anchor.top - bounds.height - 5;
    popup.style.setProperty('left', `${left}px`, 'important');
    popup.style.setProperty('top', `${Math.max(margin, Math.min(top, innerHeight - bounds.height - margin))}px`, 'important');
  }

  function isPrivate(node) {
    const element = node?.nodeType === Node.ELEMENT_NODE ? node : node?.parentElement;
    return Boolean(element?.closest(privateSelector));
  }

  function reportFocusedFrame() {
    // Remember the source before native-sidebar focus moves away. Only frame
    // identity is sent here; content is captured after an explanation action.
    if (!document.hasFocus() || ['IFRAME', 'FRAME'].includes(document.activeElement?.tagName)) return;
    browser.runtime.sendMessage({ type: 'SELECTION_FRAME_FOCUSED' }).catch(() => {});
  }

  function updateSelection() {
    reportFocusedFrame();
    const selection = window.getSelection();
    if (!selection?.rangeCount || selection.isCollapsed || isPrivate(selection.anchorNode) || isPrivate(selection.focusNode) || isPrivate(document.activeElement)) {
      hide();
      selectedRange = null;
      return;
    }
    const term = selection.toString().replace(/\s+/gu, ' ').trim();
    if (!term || Array.from(term).length > 200 || term.includes('\0')) {
      hide();
      selectedRange = null;
      return;
    }
    hide();
    cacheSelection(selection);
    const rects = selectedRange.getClientRects();
    const rect = rects[rects.length - 1];
    if (!rect || (!rect.width && !rect.height)) return;
    // The selection action stays in a closed shadow tree, isolated from page CSS.
    host?.remove();
    host = document.createElement('div');
    host.style.cssText = 'all:initial!important;color-scheme:light!important;position:fixed!important;z-index:2147483647!important;display:block!important;';
    const shadow = host.attachShadow({ mode: 'closed' });
    const style = document.createElement('style');
    style.textContent = `
      :host { color-scheme: light; --ink: #111111; --body: #374151; --muted: #6b7280; --surface: #f5f5f5; --hairline: #e5e7eb; }
      * { box-sizing: border-box; }
      button, p { font: 13px/1.5 Inter,-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif; }
      button { display: grid; place-items: center; width: 36px; height: 36px; padding: 0; border: 1px solid var(--ink); border-radius: 9999px; color: #fff; background: var(--ink); cursor: pointer; box-shadow: 0 4px 12px rgb(0 0 0 / .16); }
      button:active { background: #242424; }
      button:focus-visible { outline: 2px solid var(--ink); outline-offset: 2px; }
      button:disabled { color: var(--muted); background: var(--surface); border-color: var(--surface); cursor: wait; }
      svg { display: block; width: 18px; height: 18px; }
      .spinner { width: 12px; height: 12px; border: 1.5px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: spin .8s linear infinite; }
      @keyframes spin { to { transform: rotate(360deg); } }
      @media (prefers-reduced-motion: reduce) { .spinner { animation: none; } }
      .answer { display: flex; flex-direction: column; width: min(360px, calc(100vw - 16px)); max-height: min(360px, calc(100vh - 16px)); overflow: auto; color: var(--body); background: #fff; border: 1px solid var(--hairline); border-radius: 12px; font: 13px/1.5 Inter,-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif; box-shadow: 0 4px 12px rgb(0 0 0 / .12); }
      p { margin: 0; min-height: 0; padding: 16px; flex: 1 1 auto; overflow: auto; overflow-wrap: anywhere; white-space: pre-wrap; color: var(--body); background: #fff; font-weight: 400; border: 0; }
      .status { display: flex; flex: 0 0 auto; align-items: center; flex-wrap: wrap; gap: 10px; padding: 10px 12px; overflow-wrap: anywhere; color: var(--muted); background: var(--surface); border-top: 1px solid var(--hairline); }
      .status button { display: inline-flex; width: auto; height: 32px; padding: 7px 12px; color: var(--ink); background: #fff; border: 1px solid var(--hairline); border-radius: 8px; box-shadow: none; font-weight: 600; }
    `;
    const button = document.createElement('button');
    button.type = 'button';
    button.setAttribute('aria-label', 'Explain');
    const icon = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    icon.setAttribute('viewBox', '0 0 24 24');
    icon.setAttribute('aria-hidden', 'true');
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('d', 'M12 5C9 3 5 3 2 4v15c3-1 7-1 10 1 3-2 7-2 10-1V4c-3-1-7-1-10 1Zm0 0v15M5 8h4M5 11h4M15 8h4M15 11h4');
    path.setAttribute('fill', 'none');
    path.setAttribute('stroke', 'currentColor');
    path.setAttribute('stroke-width', '1.5');
    path.setAttribute('stroke-linecap', 'round');
    path.setAttribute('stroke-linejoin', 'round');
    icon.append(path);
    button.append(icon);
    button.addEventListener('pointerdown', event => event.preventDefault());
    const popup = host;
    button.addEventListener('click', async event => {
      if (!event.isTrusted || button.disabled) return;
      const id = ++requestId;
      let partialText = '';
      let answer;
      let panel;
      let status;
      const isCurrent = () => {
        if (id !== requestId || host !== popup) return false;
        if (!matchesSelection(window.getSelection()) || isPrivate(document.activeElement)) {
          hide();
          return false;
        }
        return true;
      };
      const showAnswer = () => {
        if (!panel) {
          panel = document.createElement('div');
          panel.className = 'answer';
          answer = document.createElement('p');
          answer.setAttribute('role', 'status');
          status = document.createElement('div');
          status.className = 'status';
          status.setAttribute('role', 'status');
          panel.append(answer, status);
          shadow.replaceChildren(style, panel);
        }
        popup.style.setProperty('display', 'block', 'important');
      };
      pendingRequest = {
        id,
        update(text) {
          if (!isCurrent() || !text.trim()) return;
          partialText = text;
          showAnswer();
          answer.setAttribute('aria-busy', 'true');
          answer.textContent = text;
          status.textContent = 'Answering…';
          positionPopup(popup, rect);
        },
      };
      button.disabled = true;
      button.setAttribute('aria-label', 'Thinking');
      button.setAttribute('aria-busy', 'true');
      const spinner = document.createElement('span');
      spinner.className = 'spinner';
      spinner.setAttribute('aria-hidden', 'true');
      button.replaceChildren(spinner);
      shadow.replaceChildren(style, button);
      positionPopup(popup, rect);
      try {
        const result = await browser.runtime.sendMessage({ type: 'EXPLAIN_SELECTION', requestId: id });
        if (!isCurrent()) return;
        pendingRequest = null;
        if (!result?.ok) throw new Error('Could not explain.');
        if (result.mode !== 'quick') {
          hide();
          return;
        }
        if (typeof result.text !== 'string' || !result.text.trim()) throw new Error('Could not explain.');
        showAnswer();
        answer.removeAttribute('aria-busy');
        answer.textContent = result.text;
        status.remove();
        positionPopup(popup, rect);
      } catch {
        if (!isCurrent()) return;
        pendingRequest = null;
        button.disabled = false;
        button.removeAttribute('aria-busy');
        button.setAttribute('aria-label', 'Retry explanation');
        button.textContent = 'Retry';
        showAnswer();
        answer.removeAttribute('aria-busy');
        answer.textContent = partialText;
        answer.hidden = !partialText;
        const error = document.createElement('span');
        error.textContent = partialText ? 'Incomplete answer. Retry to try again.' : 'Could not explain.';
        status.replaceChildren(error, button);
        positionPopup(popup, rect);
      }
    });
    shadow.append(style, button);
    document.documentElement.append(host);
    positionPopup(host, rect);
  }

  // Bound the excerpt around the selection, not the beginning of a long article.
  // Form values, editable text, scripts, and hidden elements are never included.
  function excerpt(root, limit) {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
      acceptNode(node) {
        const parent = node.parentElement;
        if (!parent || !node.data.trim() || isPrivate(parent) || parent.closest('script,style,noscript,template,[hidden],[aria-hidden="true"]')) return NodeFilter.FILTER_REJECT;
        const style = getComputedStyle(parent);
        if (style.display === 'none' || style.visibility === 'hidden' || !parent.getClientRects().length) return NodeFilter.FILTER_REJECT;
        return NodeFilter.FILTER_ACCEPT;
      },
    });
    let before = '';
    let after = '';
    let reached = false;
    let node;
    const half = Math.floor(limit / 2);
    while ((node = walker.nextNode())) {
      let offset = 0;
      if (!reached && selectedRange.intersectsNode(node)) {
        reached = true;
        offset = node === selectedRange.startContainer ? selectedRange.startOffset : 0;
        before = (before + node.data.slice(Math.max(0, offset - half), offset)).slice(-half);
      }
      if (reached) {
        after += node.data.slice(offset, offset + half - after.length) + ' ';
        if (after.length >= half) break;
      } else {
        before = (before + node.data.slice(-half) + ' ').slice(-half);
      }
    }
    return (before + after).replace(/\s+/gu, ' ').trim().slice(0, limit);
  }

  function capture(mode) {
    if (!selectedRange || !selectedRange.startContainer.isConnected || !selectedRange.endContainer.isConnected) return null;
    if (isPrivate(selectedRange.startContainer) || isPrivate(selectedRange.endContainer)) return { privacyDenied: true };
    if (selectionUrl !== location.href || selectedRange.toString().replace(/\s+/gu, ' ').trim() !== selectedTerm) return null;
    const anchor = selectedRange.commonAncestorContainer;
    const element = anchor.nodeType === Node.ELEMENT_NODE ? anchor : anchor.parentElement;
    let context = '';
    if (mode !== 'none') {
      const root = mode === 'page'
        ? element.closest('article, main, [role="main"]') || document.body
        : element.closest('p,li,blockquote,pre,td,dd,dt,figcaption,h1,h2,h3,h4') || element;
      context = excerpt(root, mode === 'page' ? 12000 : 2400);
    }
    return { term: selectedTerm, context, title: mode === 'none' ? '' : document.title, url: mode === 'none' ? '' : location.href, sourceId: selectionSourceId };
  }

  document.addEventListener('focusin', reportFocusedFrame);
  window.addEventListener('focus', reportFocusedFrame);
  reportFocusedFrame();
  document.addEventListener('pointerup', event => {
    if (event.target === host) return;
    clearTimeout(selectionTimer);
    selectionTimer = setTimeout(updateSelection, 20);
  }, true);
  document.addEventListener('keyup', event => {
    if (event.key === 'Escape') { hide(); return; }
    if (event.shiftKey || event.key.startsWith('Arrow')) {
      clearTimeout(selectionTimer);
      selectionTimer = setTimeout(updateSelection, 50);
    }
  }, true);
  document.addEventListener('pointerdown', event => { if (event.target !== host) hide(); }, true);
  document.addEventListener('selectionchange', () => {
    const current = window.getSelection();
    if (!matchesSelection(current)) {
      hide(false);
      // Sidebar focus may collapse the selection; a different selection invalidates its source.
      if (current?.rangeCount && !current.isCollapsed) selectedRange = null;
    }
  });
  window.addEventListener('scroll', event => { if (event.target !== host) hide(); }, { passive: true, capture: true });
  window.addEventListener('resize', hide, { passive: true });
  browser.runtime.onMessage.addListener(message => {
    if (message?.type === 'QUICK_UPDATED') {
      if (pendingRequest?.id === message.requestId && message.requestId === requestId && typeof message.text === 'string') {
        pendingRequest.update(message.text);
      }
      return undefined;
    }
    if (message?.type === 'DISMISS_QUICK') {
      hide();
      return undefined;
    }
    if (message?.type === 'CAPTURE_SELECTION') {
      // Input/textarea selections may not appear in window.getSelection().
      if (isPrivate(document.activeElement)) return Promise.resolve({ privacyDenied: true });
      // Toolbar/context-menu actions can precede the pointerup debounce.
      if (!message.useCachedSelection) {
        const current = window.getSelection();
        if (current?.rangeCount && !current.isCollapsed) {
          if (isPrivate(current.anchorNode) || isPrivate(current.focusNode)) return Promise.resolve({ privacyDenied: true });
          if (!matchesSelection(current)) hide();
          cacheSelection(current);
        } else {
          return Promise.resolve(null);
        }
      } else {
        const current = window.getSelection();
        if (current?.rangeCount && !current.isCollapsed && !matchesSelection(current)) return Promise.resolve(null);
        if (message.sourceId !== undefined && message.sourceId !== selectionSourceId) return Promise.resolve(null);
        if (message.expectedTerm !== undefined && message.expectedTerm !== selectedTerm) return Promise.resolve(null);
      }
      const result = capture(message.contextMode);
      // Keep the pending indicator visible while the background captures context.
      if (pendingRequest === null) hide();
      return Promise.resolve(result);
    }
    return undefined;
  });
})();
