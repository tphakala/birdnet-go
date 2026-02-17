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
  import { WifiOff } from '@lucide/svelte';
  import '@xterm/xterm/css/xterm.css';

  let terminalContainer = $state<HTMLDivElement | null>(null);
  let statusMessage = $state(t('terminal.connecting'));
  let isConnected = $state(false);
  // Use originalData (server-confirmed state) so the terminal only shows as
  // enabled after the user has actually saved their settings — formData reflects
  // unsaved local changes and would cause a 403 if connected before saving.
  let isEnabled = $derived($settingsStore.originalData.webServer?.enableTerminal ?? false);

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
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      theme: {
        background: '#1a1b26',
        foreground: '#c0caf5',
        cursor: '#c0caf5',
      },
    });

    fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(terminalContainer);
    fitAddon.fit();

    const wsUrl = getWebSocketUrl();
    ws = new WebSocket(wsUrl);
    ws.binaryType = 'arraybuffer';

    ws.onopen = () => {
      isConnected = true;
      statusMessage = t('terminal.connected');
      fitAddon?.fit();
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
    <div class="flex flex-col items-center justify-center h-full gap-4 text-base-content/60">
      <WifiOff class="size-12" />
      <div class="text-center">
        <p class="text-lg font-medium">{t('terminal.disabled')}</p>
        <p class="text-sm mt-1">{t('terminal.disabledDescription')}</p>
      </div>
    </div>
  {:else}
    <!-- Status bar -->
    <div
      class="flex items-center gap-2 px-3 py-2 bg-base-200 border-b border-base-300 text-sm shrink-0"
    >
      <span class={['size-2 rounded-full', isConnected ? 'bg-success' : 'bg-error'].join(' ')}
      ></span>
      <span class="text-base-content/70">{statusMessage}</span>
    </div>

    <!-- Terminal container -->
    <div
      bind:this={terminalContainer}
      class="flex-1 min-h-0 overflow-hidden bg-[#1a1b26] p-2"
    ></div>
  {/if}
</div>
