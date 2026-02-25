<!--
  TerminalPage.svelte - Browser terminal using xterm.js

  Purpose:
  - Provides shell access to the BirdNET-Go host via WebSocket PTY bridge
  - Only functional when EnableTerminal is set in server config
  - Shows disabled state when terminal is turned off
  - Supports multiple color themes (Dark, Light, High Contrast)
  - Can be detached into a separate browser window

  Security: Access is auth-protected at the API level. This page is only
  reachable when the user is authenticated.
-->
<script lang="ts">
  /* global WebSocket, ResizeObserver, Element */
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import { t } from '$lib/i18n';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { settingsStore } from '$lib/stores/settings';
  import {
    TerminalSquare,
    WifiOff,
    Copy,
    Maximize2,
    Minimize2,
    ChevronsUpDown,
    ChevronsDownUp,
    Check,
    Palette,
    ExternalLink,
  } from '@lucide/svelte';
  import '@xterm/xterm/css/xterm.css';
  import xtermCssRaw from '@xterm/xterm/css/xterm.css?raw';

  // ── Terminal theme definitions ───────────────────────────────────────────
  type TerminalThemeId = 'dark' | 'light' | 'highContrast';

  interface TerminalTheme {
    background: string;
    foreground: string;
    cursor: string;
    scrollThumb: string;
    scrollThumbHover: string;
  }

  const TERMINAL_THEMES: Record<TerminalThemeId, TerminalTheme> = {
    dark: {
      background: '#1a1b26',
      foreground: '#a9b1d6',
      cursor: '#c0caf5',
      scrollThumb: 'rgba(255, 255, 255, 0.15)',
      scrollThumbHover: 'rgba(255, 255, 255, 0.25)',
    },
    light: {
      background: '#fafafa',
      foreground: '#383a42',
      cursor: '#526eff',
      scrollThumb: 'rgba(0, 0, 0, 0.15)',
      scrollThumbHover: 'rgba(0, 0, 0, 0.25)',
    },
    highContrast: {
      background: '#000000',
      foreground: '#00ff00',
      cursor: '#ffffff',
      scrollThumb: 'rgba(0, 255, 0, 0.3)',
      scrollThumbHover: 'rgba(0, 255, 0, 0.5)',
    },
  };

  const THEME_STORAGE_KEY = 'birdnet-terminal-theme';
  const POPUP_WINDOW_NAME = 'birdnet-terminal';
  const POPUP_WINDOW_FEATURES = 'width=900,height=600,menubar=no,toolbar=no,location=no,status=no';
  const POPUP_DOM_SETTLE_MS = 50;
  const POPUP_RECONNECT_DELAY_MS = 100;

  function loadThemeId(): TerminalThemeId {
    if (typeof window === 'undefined') return 'dark';
    try {
      const stored = localStorage.getItem(THEME_STORAGE_KEY);
      if (stored && stored in TERMINAL_THEMES) return stored as TerminalThemeId;
    } catch {
      // localStorage may be disabled in restrictive browsing modes
    }
    return 'dark';
  }

  // ── State ────────────────────────────────────────────────────────────────
  let terminalContainer = $state<HTMLDivElement | null>(null);
  let cardElement = $state<HTMLDivElement | null>(null);
  let statusMessage = $state(t('terminal.connecting'));
  let isConnected = $state(false);
  let isCopied = $state(false);
  let isFullscreen = $state(false);
  let isExpanded = $state(false);
  let isDetached = $state(false);
  let showThemeMenu = $state(false);
  let activeThemeId = $state<TerminalThemeId>(loadThemeId());

  // Use originalData (server-confirmed state) so the terminal only shows as
  // enabled after the user has actually saved their settings — formData reflects
  // unsaved local changes and would cause a 403 if connected before saving.
  let isEnabled = $derived($settingsStore.originalData.webServer?.enableTerminal ?? false);

  let activeTheme = $derived(TERMINAL_THEMES[activeThemeId]);

  // Terminal dimensions (updated on resize)
  let termCols = $state(80);
  let termRows = $state(24);

  // Intentionally plain let (not $state): connect() and cleanup() are only
  // called from within the $effect below, so the effect never needs to track
  // term directly.
  let term: Terminal | null = null;
  let fitAddon: FitAddon | null = null;
  let ws: WebSocket | null = null;
  let resizeObserver: ResizeObserver | null = null;
  let popoutWindow: Window | null = null;

  function getWebSocketUrl(): string {
    const base = buildAppUrl('/api/v2/terminal/ws');
    return base.replace(/^http/, 'ws');
  }

  function applyThemeToTerminal() {
    if (!term) return;
    const theme = TERMINAL_THEMES[activeThemeId];
    term.options.theme = {
      background: theme.background,
      foreground: theme.foreground,
      cursor: theme.cursor,
    };
  }

  function setTheme(id: TerminalThemeId) {
    activeThemeId = id;
    try {
      localStorage.setItem(THEME_STORAGE_KEY, id);
    } catch {
      // localStorage may be disabled in restrictive browsing modes
    }
    applyThemeToTerminal();
    showThemeMenu = false;
  }

  function connect() {
    if (!terminalContainer || !isEnabled || isDetached) return;

    const theme = TERMINAL_THEMES[activeThemeId];

    term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontWeight: '300',
      fontFamily:
        "'JetBrains Mono', ui-monospace, 'Cascadia Code', 'Source Code Pro', menlo, consolas, monospace",
      theme: {
        background: theme.background,
        foreground: theme.foreground,
        cursor: theme.cursor,
      },
    });

    fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(terminalContainer);
    fitAddon.fit();

    // Track terminal dimensions
    termCols = term.cols;
    termRows = term.rows;

    const wsUrl = getWebSocketUrl();
    ws = new WebSocket(wsUrl);
    ws.binaryType = 'arraybuffer';

    ws.onopen = () => {
      isConnected = true;
      statusMessage = t('terminal.connected');
      fitAddon?.fit();
      // Send initial resize so the backend PTY matches our dimensions.
      // The first fit() above runs before onResize is registered, so
      // the backend would otherwise stay at its default size.
      if (term) {
        ws?.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
      }
    };

    ws.onmessage = event => {
      if (event.data instanceof ArrayBuffer) {
        term?.write(new Uint8Array(event.data));
      } else {
        term?.write(event.data as string);
      }
    };

    ws.onclose = () => {
      isConnected = false;
      statusMessage = t('terminal.disconnected');
      term?.write(`\r\n\r\n[${t('terminal.connectionClosed')}]\r\n`);
    };

    ws.onerror = () => {
      isConnected = false;
      statusMessage = t('terminal.connectionError');
    };

    // Terminal → WebSocket
    term.onData(data => {
      if (ws?.readyState === WebSocket.OPEN) {
        ws.send(data);
      }
    });

    // Handle container resize — fitAddon.fit() triggers term.onResize which sends
    // the resize message to the backend.
    resizeObserver = new ResizeObserver(() => {
      fitAddon?.fit();
    });
    resizeObserver.observe(terminalContainer);
    term.onResize(({ cols, rows }) => {
      termCols = cols;
      termRows = rows;
      if (ws?.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }));
      }
    });
  }

  function cleanup() {
    resizeObserver?.disconnect();
    ws?.close();
    term?.dispose();
    term = null;
    ws = null;
    fitAddon = null;
    resizeObserver = null;
    // Don't touch popoutWindow or isDetached here. This function is called
    // by the connect $effect's cleanup, which re-runs when terminalContainer
    // becomes null during detach. Closing the popup here would kill the
    // detached terminal immediately.
  }

  async function copyTerminalOutput() {
    if (!term) return;
    const text = term.getSelection();
    if (text) {
      try {
        await navigator.clipboard.writeText(text);
        isCopied = true;
        setTimeout(() => (isCopied = false), 2000);
      } catch {
        // Clipboard write can fail if the document isn't focused
      }
    }
    term.focus();
  }

  function toggleExpanded() {
    isExpanded = !isExpanded;
    // Re-fit after layout change, then refocus terminal
    setTimeout(() => {
      fitAddon?.fit();
      term?.focus();
    }, 50);
  }

  function toggleFullscreen() {
    if (!cardElement) return;
    // State is updated by the fullscreenchange listener — no eager assignment needed.
    if (!document.fullscreenElement) {
      cardElement.requestFullscreen().catch(() => {});
    } else {
      document.exitFullscreen().catch(() => {});
    }
  }

  // ── Popout / detach ──────────────────────────────────────────────────────
  function detachTerminal() {
    if (isDetached || !term || !ws) return;

    const theme = TERMINAL_THEMES[activeThemeId];

    // Open a new window
    const popup = window.open('', POPUP_WINDOW_NAME, POPUP_WINDOW_FEATURES);
    if (!popup) return; // popup blocker

    // Transfer WebSocket ownership to the popup BEFORE setting isDetached.
    // isDetached is $state and tracked by the connect $effect — setting it
    // would trigger cleanup() which calls ws?.close(). By nulling ws first,
    // cleanup becomes a no-op for the WebSocket.
    const detachedWs = ws;
    ws = null;

    popoutWindow = popup;
    isDetached = true;

    // Tear down the inline terminal (ws is already transferred above)
    resizeObserver?.disconnect();
    resizeObserver = null;
    term.dispose();
    term = null;
    fitAddon = null;

    // Build popup HTML via concatenation — template literals with style tags
    // and interpolation confuse the Svelte CSS preprocessor.
    const popupHtml =
      '<!DOCTYPE html><html lang="' +
      (document.documentElement.lang || 'en') +
      '"><head><meta charset="UTF-8"/>' +
      '<title>BirdNET-Go \u2014 ' +
      t('terminal.title') +
      '</title></head>' +
      '<body>' +
      '<div id="toolbar">' +
      '<div class="toolbar-left">' +
      '<div class="status-group">' +
      '<span id="status-dot" class="status-dot connected"></span>' +
      '<span id="status-text" class="status-text">' +
      t('terminal.connected') +
      '</span>' +
      '</div>' +
      '<span id="dimensions" class="dimensions"></span>' +
      '</div>' +
      '<div class="toolbar-right">' +
      '<div class="theme-swatches">' +
      '<button id="theme-dark" class="swatch-btn" title="' +
      t('terminal.themeDark') +
      '"><span class="swatch" data-swatch="dark"></span></button>' +
      '<button id="theme-light" class="swatch-btn" title="' +
      t('terminal.themeLight') +
      '"><span class="swatch" data-swatch="light"></span></button>' +
      '<button id="theme-hc" class="swatch-btn" title="' +
      t('terminal.themeHighContrast') +
      '"><span class="swatch" data-swatch="highContrast"></span></button>' +
      '</div>' +
      '<button id="copy-btn" class="toolbar-btn" title="' +
      t('terminal.copySelection') +
      '">' +
      '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>' +
      '</button>' +
      '<button id="reattach-btn" class="toolbar-btn" title="' +
      t('terminal.reattach') +
      '">' +
      '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/></svg>' +
      '</button>' +
      '</div>' +
      '</div>' +
      '<div id="terminal"></div>' +
      '</body></html>';
    popup.document.open();
    popup.document.write(popupHtml);
    popup.document.close();

    // Apply styles programmatically to avoid Svelte CSS preprocessor issues
    const popupStyle = popup.document.createElement('style');
    popupStyle.textContent =
      '*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; border-color: transparent; }' +
      'html, body { width: 100%; height: 100%; overflow: hidden; display: flex; flex-direction: column; background: ' +
      theme.background +
      '; }' +
      '#toolbar { height: 40px; flex-shrink: 0; display: flex; align-items: center; justify-content: space-between; padding: 0 12px; border-bottom: 1px solid rgba(128,128,128,0.2); background: rgba(128,128,128,0.06); }' +
      '.toolbar-left, .toolbar-right { display: flex; align-items: center; gap: 10px; }' +
      '.status-group { display: flex; align-items: center; gap: 6px; }' +
      '.status-dot { width: 7px; height: 7px; border-radius: 50%; }' +
      '.status-dot.connected { background: #10b981; animation: pulse 2s infinite; }' +
      '.status-dot.disconnected { background: #ef4444; }' +
      '@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.5; } }' +
      '.status-text { font-size: 11px; font-weight: 500; font-family: system-ui, sans-serif; }' +
      '.status-text.connected { color: #10b981; }' +
      '.status-text.disconnected { color: #ef4444; }' +
      '.dimensions { font-size: 10px; font-family: monospace; opacity: 0.35; color: ' +
      theme.foreground +
      '; }' +
      '.theme-swatches { display: flex; gap: 4px; }' +
      '.swatch-btn { background: none; border: none; padding: 3px; border-radius: 6px; cursor: pointer; display: flex; align-items: center; justify-content: center; transition: background 0.15s; }' +
      '.swatch-btn:hover { background: rgba(128,128,128,0.15); }' +
      '.swatch-btn.active { background: rgba(128,128,128,0.2); }' +
      '.swatch { width: 14px; height: 14px; border-radius: 50%; border: 1.5px solid; }' +
      '[data-swatch="dark"] { background: ' +
      TERMINAL_THEMES.dark.background +
      '; border-color: ' +
      TERMINAL_THEMES.dark.foreground +
      '; }' +
      '[data-swatch="light"] { background: ' +
      TERMINAL_THEMES.light.background +
      '; border-color: ' +
      TERMINAL_THEMES.light.foreground +
      '; }' +
      '[data-swatch="highContrast"] { background: ' +
      TERMINAL_THEMES.highContrast.background +
      '; border-color: ' +
      TERMINAL_THEMES.highContrast.foreground +
      '; }' +
      '.toolbar-btn { background: none; border: none; padding: 6px; border-radius: 6px; cursor: pointer; color: ' +
      theme.foreground +
      '; opacity: 0.45; transition: opacity 0.15s, background 0.15s; display: flex; align-items: center; }' +
      '.toolbar-btn:hover { opacity: 1; background: rgba(128,128,128,0.15); }' +
      '.toolbar-btn.copied { opacity: 1; color: #10b981; }' +
      '#terminal { flex: 1; min-height: 0; overflow: hidden; }' +
      '.xterm { padding: 12px 16px; }' +
      '.xterm, .xterm * { border-color: transparent; }' +
      '.xterm .xterm-viewport { background-color: inherit !important; overflow-y: auto !important; }' +
      '.xterm .xterm-viewport::-webkit-scrollbar { width: 6px; }' +
      '.xterm .xterm-viewport::-webkit-scrollbar-track { background: transparent; }' +
      '.xterm .xterm-viewport::-webkit-scrollbar-thumb { background: ' +
      theme.scrollThumb +
      '; border-radius: 3px; }' +
      '.xterm .xterm-viewport::-webkit-scrollbar-thumb:hover { background: ' +
      theme.scrollThumbHover +
      '; }';
    popup.document.head.appendChild(popupStyle);

    // Inject xterm CSS into the popup using the raw import (bundler-independent)
    const xtermStyle = popup.document.createElement('style');
    xtermStyle.textContent = xtermCssRaw;
    popup.document.head.appendChild(xtermStyle);

    // Create a new Terminal in the popup
    const popupContainer = popup.document.getElementById('terminal');
    if (!popupContainer) {
      // Popup DOM failed — close the popup, restore ws, and reconnect inline
      popup.close();
      ws = detachedWs;
      popoutWindow = null;
      isDetached = false;
      connect();
      return;
    }

    // Register a fallback cleanup immediately in case the popup is closed
    // before the DOM-settle timeout fires (e.g., popup blocker closes it).
    function earlyCleanup() {
      detachedWs.onclose = null;
      detachedWs.close();
      popoutWindow = null;
      isDetached = false;
      setTimeout(() => {
        if (terminalContainer && isEnabled && !term) {
          connect();
        }
      }, POPUP_RECONNECT_DELAY_MS);
    }
    popup.addEventListener('beforeunload', earlyCleanup);

    // Use the popup window's setTimeout to let the DOM settle
    popup.setTimeout(() => {
      // Remove the early fallback; the full handler below replaces it.
      popup.removeEventListener('beforeunload', earlyCleanup);
      let currentThemeId: TerminalThemeId = activeThemeId;
      let currentTheme = TERMINAL_THEMES[currentThemeId];

      const popupTerm = new Terminal({
        cursorBlink: true,
        fontSize: 13,
        fontWeight: '300',
        fontFamily:
          "'JetBrains Mono', ui-monospace, 'Cascadia Code', 'Source Code Pro', menlo, consolas, monospace",
        theme: {
          background: currentTheme.background,
          foreground: currentTheme.foreground,
          cursor: currentTheme.cursor,
        },
      });

      const popupFit = new FitAddon();
      popupTerm.loadAddon(popupFit);
      popupTerm.open(popupContainer);
      popupFit.fit();

      // ── Toolbar references ──
      const doc = popup.document;
      const statusDot = doc.getElementById('status-dot');
      const statusText = doc.getElementById('status-text');
      const dimensionsEl = doc.getElementById('dimensions');
      const copyBtn = doc.getElementById('copy-btn');
      const reattachBtn = doc.getElementById('reattach-btn');
      const swatchBtns = {
        dark: doc.getElementById('theme-dark'),
        light: doc.getElementById('theme-light'),
        highContrast: doc.getElementById('theme-hc'),
      };

      // Mark the initial active theme swatch
      swatchBtns[currentThemeId]?.classList.add('active');

      // Update dimensions display
      function updateDimensions(cols: number, rows: number) {
        if (dimensionsEl) dimensionsEl.textContent = cols + '\u00d7' + rows;
      }
      updateDimensions(popupTerm.cols, popupTerm.rows);

      // ── Theme switching ──
      function applyPopupTheme(id: TerminalThemeId) {
        const th = TERMINAL_THEMES[id];
        currentThemeId = id;
        currentTheme = th;

        // Update xterm theme
        popupTerm.options.theme = {
          background: th.background,
          foreground: th.foreground,
          cursor: th.cursor,
        };

        // Update body and terminal container backgrounds
        doc.body.style.background = th.background;
        if (popupContainer) popupContainer.style.background = th.background;

        // Update scrollbar CSS custom properties via stylesheet replacement
        const existingCustomStyle = doc.getElementById('custom-theme-style');
        if (existingCustomStyle) existingCustomStyle.remove();
        const newStyle = doc.createElement('style');
        newStyle.id = 'custom-theme-style';
        newStyle.textContent =
          '.xterm .xterm-viewport::-webkit-scrollbar-thumb { background: ' +
          th.scrollThumb +
          '; }' +
          '.xterm .xterm-viewport::-webkit-scrollbar-thumb:hover { background: ' +
          th.scrollThumbHover +
          '; }';
        doc.head.appendChild(newStyle);

        // Update toolbar button colors
        const toolbarBtns = doc.querySelectorAll('.toolbar-btn');
        toolbarBtns.forEach((btn: Element) => {
          (btn as HTMLElement).style.color = th.foreground;
        });
        if (dimensionsEl) dimensionsEl.style.color = th.foreground;

        // Update active swatch indicator
        Object.entries(swatchBtns).forEach(([key, btn]) => {
          btn?.classList.toggle('active', key === id);
        });

        // Sync to main window state & localStorage
        activeThemeId = id;
        try {
          localStorage.setItem(THEME_STORAGE_KEY, id);
        } catch {
          // ignore
        }
      }

      // Wire up swatch buttons
      swatchBtns.dark?.addEventListener('click', () => applyPopupTheme('dark'));
      swatchBtns.light?.addEventListener('click', () => applyPopupTheme('light'));
      swatchBtns.highContrast?.addEventListener('click', () => applyPopupTheme('highContrast'));

      // ── Copy button ──
      copyBtn?.addEventListener('click', () => {
        const text = popupTerm.getSelection();
        if (text) {
          navigator.clipboard.writeText(text).then(() => {
            copyBtn.classList.add('copied');
            copyBtn.innerHTML =
              '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>';
            popup.setTimeout(() => {
              copyBtn.classList.remove('copied');
              copyBtn.innerHTML =
                '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>';
            }, 2000);
          });
        }
        popupTerm.focus();
      });

      // ── Reattach button — close popup, reconnect inline ──
      reattachBtn?.addEventListener('click', () => {
        popup.close();
      });

      // Send resize to backend
      if (detachedWs.readyState === WebSocket.OPEN) {
        detachedWs.send(
          JSON.stringify({ type: 'resize', cols: popupTerm.cols, rows: popupTerm.rows })
        );
      }

      // WebSocket → popup terminal
      detachedWs.onmessage = event => {
        if (event.data instanceof ArrayBuffer) {
          popupTerm.write(new Uint8Array(event.data));
        } else {
          popupTerm.write(event.data as string);
        }
      };

      detachedWs.onclose = () => {
        popupTerm.write(`\r\n\r\n[${t('terminal.connectionClosed')}]\r\n`);
        if (statusDot) {
          statusDot.className = 'status-dot disconnected';
        }
        if (statusText) {
          statusText.textContent = t('terminal.disconnected');
          statusText.className = 'status-text disconnected';
        }
      };

      // Popup terminal → WebSocket
      popupTerm.onData(data => {
        if (detachedWs.readyState === WebSocket.OPEN) {
          detachedWs.send(data);
        }
      });

      // Resize handling in popup
      const popupResizeObserver = new ResizeObserver(() => {
        popupFit.fit();
      });
      popupResizeObserver.observe(popupContainer);

      popupTerm.onResize(({ cols, rows }) => {
        termCols = cols;
        termRows = rows;
        updateDimensions(cols, rows);
        if (detachedWs.readyState === WebSocket.OPEN) {
          detachedWs.send(JSON.stringify({ type: 'resize', cols, rows }));
        }
      });

      popupTerm.focus();

      // Update dimensions
      termCols = popupTerm.cols;
      termRows = popupTerm.rows;

      // When the popup is closed, reattach the terminal in the main window
      popup.addEventListener('beforeunload', () => {
        popupResizeObserver.disconnect();
        detachedWs.onclose = null;
        popupTerm.dispose();
        detachedWs.close();
        popoutWindow = null;
        isDetached = false;

        // Reconnect in the main window on next tick
        setTimeout(() => {
          if (terminalContainer && isEnabled && !term) {
            connect();
          }
        }, POPUP_RECONNECT_DELAY_MS);
      });
    }, POPUP_DOM_SETTLE_MS);
  }

  // Close theme menu when clicking outside
  $effect(() => {
    if (!showThemeMenu) return;
    function onClickOutside(e: MouseEvent) {
      const target = e.target as HTMLElement;
      if (!target.closest('.theme-menu-wrapper')) {
        showThemeMenu = false;
      }
    }
    document.addEventListener('click', onClickOutside, true);
    return () => document.removeEventListener('click', onClickOutside, true);
  });

  // Listen for fullscreen changes (e.g. user presses Escape)
  $effect(() => {
    function onFullscreenChange() {
      isFullscreen = !!document.fullscreenElement;
      // Re-fit terminal after fullscreen transition, then refocus
      setTimeout(() => {
        fitAddon?.fit();
        term?.focus();
      }, 100);
    }
    document.addEventListener('fullscreenchange', onFullscreenChange);
    return () => document.removeEventListener('fullscreenchange', onFullscreenChange);
  });

  // Push theme-dependent scrollbar colors as CSS custom properties so the
  // global ::-webkit-scrollbar rules pick them up for any active theme.
  $effect(() => {
    if (!terminalContainer) return;
    const theme = TERMINAL_THEMES[activeThemeId];
    terminalContainer.style.setProperty('--terminal-scroll-thumb', theme.scrollThumb);
    terminalContainer.style.setProperty('--terminal-scroll-thumb-hover', theme.scrollThumbHover);
  });

  // Reactively connect when the terminal becomes enabled and the container is
  // bound. This handles both initial mount (isEnabled already true) and the
  // case where the user enables the terminal and saves settings while already
  // on this page — onMount would miss that second scenario.
  $effect(() => {
    if (isEnabled && terminalContainer && !term) {
      connect();
    }
    return cleanup;
  });

  // Close the popup window when the main page unloads (navigation, tab close).
  // This is separate from cleanup() because cleanup() runs on every $effect
  // re-evaluation (including detach), while this only runs on true teardown.
  $effect(() => {
    function onBeforeUnload() {
      if (popoutWindow && !popoutWindow.closed) {
        popoutWindow.close();
      }
    }
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  });
</script>

<div class="flex flex-col h-full min-h-0">
  {#if !isEnabled}
    <!-- Disabled state -->
    <div class="flex flex-col items-center justify-center h-full gap-4 opacity-60">
      <WifiOff class="size-12" />
      <div class="text-center">
        <p class="text-lg font-medium">{t('terminal.disabled')}</p>
        <p class="text-sm mt-1">{t('terminal.disabledDescription')}</p>
      </div>
    </div>
  {:else if isDetached}
    <!-- Detached state — terminal is in a separate window -->
    <div class="flex flex-col items-center justify-center h-full gap-4 opacity-60">
      <ExternalLink class="size-12" />
      <div class="text-center">
        <p class="text-lg font-medium">{t('terminal.detached')}</p>
        <p class="text-sm mt-1">{t('terminal.detachedDescription')}</p>
      </div>
    </div>
  {:else}
    <!-- Terminal card — constrained height by default, fills parent when expanded or fullscreen -->
    <div
      bind:this={cardElement}
      class="flex flex-col rounded-xl border overflow-hidden"
      class:flex-1={isExpanded || isFullscreen}
      style:border-color="var(--border-100)"
      style:background="var(--surface-100)"
      style:box-shadow="var(--shadow-sm)"
    >
      <!-- Terminal header bar -->
      <div
        class="flex items-center justify-between px-4 py-2.5 shrink-0 border-b"
        style:border-color="var(--border-100)"
      >
        <div class="flex items-center gap-3">
          <!-- Icon + label -->
          <div class="flex items-center gap-2">
            <div class="p-1.5 rounded-lg bg-emerald-500/10">
              <TerminalSquare class="w-4 h-4 text-emerald-500" />
            </div>
            <span
              class="text-xs font-semibold uppercase tracking-wider"
              style:color="var(--color-base-content)"
              style:opacity="0.45"
            >
              {t('terminal.title')}
            </span>
          </div>

          <!-- Connection status -->
          <div class="flex items-center gap-1.5">
            <span
              class="w-2 h-2 rounded-full {isConnected ? 'bg-emerald-500' : 'bg-red-500'}"
              class:animate-pulse={isConnected}
            ></span>
            <span
              role="status"
              aria-live="polite"
              class="text-xs font-medium {isConnected
                ? 'text-emerald-600 dark:text-emerald-400'
                : 'text-red-600 dark:text-red-400'}"
            >
              {statusMessage}
            </span>
          </div>

          <!-- Session dimensions -->
          {#if isConnected}
            <span
              class="text-[10px] font-mono tabular-nums"
              style:color="var(--color-base-content)"
              style:opacity="0.35"
            >
              {termCols}&times;{termRows}
            </span>
          {/if}
        </div>

        <!-- Action buttons — tabindex -1 prevents stealing focus from terminal -->
        <div class="flex items-center gap-1">
          <!-- Theme selector -->
          <div class="relative theme-menu-wrapper">
            <button
              class="p-1.5 rounded-md transition-colors cursor-pointer hover:bg-black/10 dark:hover:bg-white/10 hover:opacity-80"
              style:color="var(--color-base-content)"
              style:opacity={showThemeMenu ? 1 : 0.45}
              onclick={() => (showThemeMenu = !showThemeMenu)}
              title={t('terminal.colorTheme')}
              aria-label={t('terminal.colorTheme')}
              aria-expanded={showThemeMenu}
              aria-haspopup="true"
            >
              <Palette class="w-3.5 h-3.5" />
            </button>
            {#if showThemeMenu}
              <div
                class="absolute right-0 top-full mt-1 z-50 rounded-lg border shadow-lg py-1 min-w-[160px]"
                style:background="var(--surface-100)"
                style:border-color="var(--border-100)"
                role="menu"
                aria-label={t('terminal.colorTheme')}
              >
                <button
                  class="flex items-center gap-2 w-full px-3 py-1.5 text-xs text-left transition-colors hover:bg-black/5 dark:hover:bg-white/5 cursor-pointer"
                  style:color="var(--color-base-content)"
                  role="menuitem"
                  onclick={() => setTheme('dark')}
                >
                  <span
                    class="w-3 h-3 rounded-full border"
                    style:background={TERMINAL_THEMES.dark.background}
                    style:border-color={TERMINAL_THEMES.dark.foreground}
                  ></span>
                  {t('terminal.themeDark')}
                  {#if activeThemeId === 'dark'}
                    <Check class="w-3 h-3 ml-auto text-emerald-500" />
                  {/if}
                </button>
                <button
                  class="flex items-center gap-2 w-full px-3 py-1.5 text-xs text-left transition-colors hover:bg-black/5 dark:hover:bg-white/5 cursor-pointer"
                  style:color="var(--color-base-content)"
                  role="menuitem"
                  onclick={() => setTheme('light')}
                >
                  <span
                    class="w-3 h-3 rounded-full border"
                    style:background={TERMINAL_THEMES.light.background}
                    style:border-color={TERMINAL_THEMES.light.foreground}
                  ></span>
                  {t('terminal.themeLight')}
                  {#if activeThemeId === 'light'}
                    <Check class="w-3 h-3 ml-auto text-emerald-500" />
                  {/if}
                </button>
                <button
                  class="flex items-center gap-2 w-full px-3 py-1.5 text-xs text-left transition-colors hover:bg-black/5 dark:hover:bg-white/5 cursor-pointer"
                  style:color="var(--color-base-content)"
                  role="menuitem"
                  onclick={() => setTheme('highContrast')}
                >
                  <span
                    class="w-3 h-3 rounded-full border"
                    style:background={TERMINAL_THEMES.highContrast.background}
                    style:border-color={TERMINAL_THEMES.highContrast.foreground}
                  ></span>
                  {t('terminal.themeHighContrast')}
                  {#if activeThemeId === 'highContrast'}
                    <Check class="w-3 h-3 ml-auto text-emerald-500" />
                  {/if}
                </button>
              </div>
            {/if}
          </div>

          <button
            tabindex="-1"
            class="p-1.5 rounded-md transition-colors cursor-pointer hover:bg-black/10 dark:hover:bg-white/10 hover:opacity-80"
            style:color="var(--color-base-content)"
            style:opacity={isCopied ? 1 : 0.45}
            onclick={copyTerminalOutput}
            title={t('terminal.copySelection')}
            aria-label={t('terminal.copySelection')}
          >
            {#if isCopied}
              <Check class="w-3.5 h-3.5 text-emerald-500" />
            {:else}
              <Copy class="w-3.5 h-3.5" />
            {/if}
          </button>
          <button
            class="p-1.5 rounded-md transition-colors cursor-pointer hover:bg-black/10 dark:hover:bg-white/10 hover:opacity-80"
            style:color="var(--color-base-content)"
            style:opacity="0.45"
            onclick={detachTerminal}
            title={t('terminal.detach')}
            aria-label={t('terminal.detach')}
          >
            <ExternalLink class="w-3.5 h-3.5" />
          </button>
          <button
            tabindex="-1"
            class="p-1.5 rounded-md transition-colors cursor-pointer hover:bg-black/10 dark:hover:bg-white/10 hover:opacity-80"
            style:color="var(--color-base-content)"
            style:opacity="0.45"
            onclick={toggleExpanded}
            title={isExpanded ? t('terminal.collapse') : t('terminal.expand')}
            aria-label={isExpanded ? t('terminal.collapse') : t('terminal.expand')}
          >
            {#if isExpanded}
              <ChevronsDownUp class="w-3.5 h-3.5" />
            {:else}
              <ChevronsUpDown class="w-3.5 h-3.5" />
            {/if}
          </button>
          <button
            tabindex="-1"
            class="p-1.5 rounded-md transition-colors cursor-pointer hover:bg-black/10 dark:hover:bg-white/10 hover:opacity-80"
            style:color="var(--color-base-content)"
            style:opacity="0.45"
            onclick={toggleFullscreen}
            title={isFullscreen ? t('terminal.exitFullscreen') : t('terminal.fullscreen')}
            aria-label={isFullscreen ? t('terminal.exitFullscreen') : t('terminal.fullscreen')}
          >
            {#if isFullscreen}
              <Minimize2 class="w-3.5 h-3.5" />
            {:else}
              <Maximize2 class="w-3.5 h-3.5" />
            {/if}
          </button>
        </div>
      </div>

      <!-- Terminal container — FitAddon reads .xterm padding, not container padding -->
      <div
        bind:this={terminalContainer}
        class="overflow-hidden"
        class:flex-1={isExpanded || isFullscreen}
        class:min-h-0={isExpanded || isFullscreen}
        style:background={activeTheme.background}
        style:height={isExpanded || isFullscreen ? undefined : '480px'}
      ></div>
    </div>
  {/if}
</div>

<style>
  /* Padding on .xterm is the only way FitAddon accounts for it when
     calculating rows/cols. CSS padding on the parent container is ignored. */
  :global(.xterm) {
    padding: 12px 16px;
  }

  /* The project's global `*` rule sets border-color on every element,
     which causes visible borders inside the xterm widget. Reset it. */
  :global(.xterm),
  :global(.xterm *) {
    border-color: transparent;
  }

  /* xterm's viewport background — set dynamically via inline style on the
     container, but we still need the !important override so xterm's own
     default (#000) doesn't win. Inherit from parent's inline background. */
  :global(.xterm .xterm-viewport) {
    background-color: inherit !important;
    overflow-y: auto !important;
  }

  /* Hide the scrollbar track entirely — keep scroll functional but invisible.
     The thin scrollbar + transparent track avoids the dead-space gutter. */
  :global(.xterm .xterm-viewport::-webkit-scrollbar) {
    width: 6px;
  }

  :global(.xterm .xterm-viewport::-webkit-scrollbar-track) {
    background: transparent;
  }

  :global(.xterm .xterm-viewport::-webkit-scrollbar-thumb) {
    background: var(--terminal-scroll-thumb, rgba(255, 255, 255, 0.15));
    border-radius: 3px;
  }

  :global(.xterm .xterm-viewport::-webkit-scrollbar-thumb:hover) {
    background: var(--terminal-scroll-thumb-hover, rgba(255, 255, 255, 0.25));
  }
</style>
