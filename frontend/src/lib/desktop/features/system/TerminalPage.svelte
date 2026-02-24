<!--
  TerminalPage.svelte - Browser terminal using xterm.js

  Purpose:
  - Provides shell access to the BirdNET-Go host via WebSocket PTY bridge
  - Only functional when EnableTerminal is set in server config
  - Shows disabled state when terminal is turned off

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
  } from '@lucide/svelte';
  import '@xterm/xterm/css/xterm.css';

  // Terminal theme colors — used in Terminal config, inline styles, and CSS overrides.
  const TERM_BG = '#1a1b26';
  const TERM_FG = '#a9b1d6';
  const TERM_CURSOR = '#c0caf5';

  let terminalContainer = $state<HTMLDivElement | null>(null);
  let cardElement = $state<HTMLDivElement | null>(null);
  let statusMessage = $state(t('terminal.connecting'));
  let isConnected = $state(false);
  let isCopied = $state(false);
  let isFullscreen = $state(false);
  let isExpanded = $state(false);
  // Use originalData (server-confirmed state) so the terminal only shows as
  // enabled after the user has actually saved their settings — formData reflects
  // unsaved local changes and would cause a 403 if connected before saving.
  let isEnabled = $derived($settingsStore.originalData.webServer?.enableTerminal ?? false);

  // Terminal dimensions (updated on resize)
  let termCols = $state(80);
  let termRows = $state(24);

  // Intentionally plain let (not $state): connect() and cleanup() are only
  // called from within the $effect below, so the effect never needs to track
  // term directly. A future "Reconnect" button would need to make term $state.
  let term: Terminal | null = null;
  let fitAddon: FitAddon | null = null;
  let ws: WebSocket | null = null;
  let resizeObserver: ResizeObserver | null = null;

  function getWebSocketUrl(): string {
    const base = buildAppUrl('/api/v2/terminal/ws');
    return base.replace(/^http/, 'ws');
  }

  function connect() {
    if (!terminalContainer || !isEnabled) return;

    term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontWeight: '300',
      fontFamily:
        "'JetBrains Mono', ui-monospace, 'Cascadia Code', 'Source Code Pro', menlo, consolas, monospace",
      theme: {
        background: TERM_BG,
        foreground: TERM_FG,
        cursor: TERM_CURSOR,
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
        style:background={TERM_BG}
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

  /* xterm's viewport background defaults to #000 which creates a visible
     mismatch against the terminal theme background. Must match TERM_BG constant. */
  :global(.xterm .xterm-viewport) {
    background-color: #1a1b26 !important;
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
    background: rgba(255, 255, 255, 0.15);
    border-radius: 3px;
  }

  :global(.xterm .xterm-viewport::-webkit-scrollbar-thumb:hover) {
    background: rgba(255, 255, 255, 0.25);
  }
</style>
