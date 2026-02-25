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
  /* global WebSocket, ResizeObserver */
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

  function loadThemeId(): TerminalThemeId {
    if (typeof window === 'undefined') return 'dark';
    try {
      const stored = localStorage.getItem(THEME_STORAGE_KEY);
      if (stored === 'dark' || stored === 'light' || stored === 'highContrast') return stored;
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
    if (popoutWindow && !popoutWindow.closed) {
      popoutWindow.close();
    }
    popoutWindow = null;
    isDetached = false;
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
    const popup = window.open(
      '',
      'birdnet-terminal',
      'width=900,height=600,menubar=no,toolbar=no,location=no,status=no'
    );
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
      '<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"/>' +
      '<title>BirdNET-Go \u2014 ' +
      t('terminal.title') +
      '</title></head>' +
      '<body><div id="terminal"></div></body></html>';
    popup.document.open();
    popup.document.write(popupHtml);
    popup.document.close();

    // Apply styles programmatically to avoid Svelte CSS preprocessor issues
    const popupStyle = popup.document.createElement('style');
    popupStyle.textContent =
      '*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; border-color: transparent; }' +
      'html, body { width: 100%; height: 100%; overflow: hidden; background: ' +
      theme.background +
      '; }' +
      '#terminal { width: 100%; height: 100%; }' +
      '.xterm { padding: 12px 16px; }' +
      '.xterm, .xterm * { border-color: transparent; }' +
      '.xterm .xterm-viewport { background-color: ' +
      theme.background +
      ' !important; overflow-y: auto !important; }' +
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

    // Use the popup window's setTimeout to let the DOM settle
    popup.setTimeout(() => {
      const popupTerm = new Terminal({
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

      const popupFit = new FitAddon();
      popupTerm.loadAddon(popupFit);
      popupTerm.open(popupContainer);
      popupFit.fit();

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
        popupTerm.dispose();
        detachedWs.close();
        popoutWindow = null;
        isDetached = false;

        // Reconnect in the main window on next tick
        setTimeout(() => {
          if (terminalContainer && isEnabled && !term) {
            connect();
          }
        }, 100);
      });
    }, 50);
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
              tabindex="-1"
              class="p-1.5 rounded-md transition-colors cursor-pointer"
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
                    style:background="#1a1b26"
                    style:border-color="#a9b1d6"
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
                    style:background="#fafafa"
                    style:border-color="#383a42"
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
                    style:background="#000000"
                    style:border-color="#00ff00"
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
            class="p-1.5 rounded-md transition-colors cursor-pointer"
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
            tabindex="-1"
            class="p-1.5 rounded-md transition-colors cursor-pointer"
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
            class="p-1.5 rounded-md transition-colors cursor-pointer"
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
            class="p-1.5 rounded-md transition-colors cursor-pointer"
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
