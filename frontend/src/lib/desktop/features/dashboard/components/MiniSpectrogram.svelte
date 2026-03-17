<script lang="ts">
  /**
   * MiniSpectrogram — Compact live spectrogram widget for the dashboard
   *
   * Displays a real-time scrolling waterfall spectrogram of the first available
   * audio source. Manages its own HLS.js connection, heartbeat, and access control.
   *
   * Key design:
   * - Uses useSpectrogramAnalyser composable (no inline Web Audio graph)
   * - Source discovery via SSE audio-level stream
   * - Silent by default with speaker toggle
   * - Manual start/stop with localStorage persistence
   * - Respects appState.liveSpectrogram for auto-start
   */

  import Hls from 'hls.js';
  import ReconnectingEventSource from 'reconnecting-eventsource';
  import { onMount } from 'svelte';
  import { Radio, Volume2, VolumeX, Play, Square } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { appState } from '$lib/stores/appState.svelte';
  import { HLS_AUDIO_CONFIG } from '$lib/desktop/components/ui/hls-config';
  import { useSpectrogramAnalyser } from '$lib/utils/useSpectrogramAnalyser.svelte';
  import SpectrogramCanvas from '$lib/desktop/components/media/SpectrogramCanvas.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { loggers } from '$lib/utils/logger';
  import type { ColorMapName } from '$lib/utils/spectrogramColorMaps';

  const logger = loggers.audio;
  const STORAGE_KEY = 'birdnet-spectrogram-active';
  const FFT_SIZE = 1024;
  const HEARTBEAT_INTERVAL = 20000;
  const SOURCE_DISCOVERY_TIMEOUT = 5000;

  // Access control — handled internally, no props from parent
  const hasAudioAccess = $derived(
    !appState.security.enabled ||
      appState.security.accessAllowed ||
      appState.security.publicAccess.liveAudio
  );

  // Local state
  let isActive = $state(false);
  let isConnecting = $state(false);
  let audioOutput = $state(false);
  let colorMap = $state<ColorMapName>('inferno');
  let frequencyRange = $state<[number, number]>([0, 15000]);
  let currentSourceId = $state<string>('');

  // HLS + audio refs
  let hls: Hls | null = null;
  let audioElement: HTMLAudioElement | null = null;
  let heartbeatTimer: ReturnType<typeof globalThis.setInterval> | null = null;

  // Initialize composable during component init (must be at top level for $effect cleanup)
  const spectro = useSpectrogramAnalyser({ fftSize: FFT_SIZE, audioOutput: false });

  function shouldAutoStart(): boolean {
    if (appState.liveSpectrogram) return true;
    try {
      return globalThis.localStorage?.getItem(STORAGE_KEY) === 'true';
    } catch {
      return false;
    }
  }

  function persistToggleState(active: boolean) {
    try {
      if (active) {
        globalThis.localStorage?.setItem(STORAGE_KEY, 'true');
      } else {
        globalThis.localStorage?.removeItem(STORAGE_KEY);
      }
    } catch {
      /* localStorage not available */
    }
  }

  /**
   * Discover the first available audio source via the SSE audio-level stream.
   * Returns the source ID, or null if none found within the timeout.
   */
  async function discoverFirstSource(): Promise<string | null> {
    return new Promise(resolve => {
      const sse = new ReconnectingEventSource(buildAppUrl('/api/v2/streams/audio-level'), {
        max_retry_time: 30000,
        withCredentials: false,
      });

      const timeout = globalThis.setTimeout(() => {
        sse.close();
        resolve(null);
      }, SOURCE_DISCOVERY_TIMEOUT);

      // SSE sends unnamed events (no event: field), so use onmessage
      // The JSON payload has a type field: { type: 'audio-level', levels: {...} }
      sse.onmessage = (event: globalThis.MessageEvent) => {
        try {
          const data = JSON.parse(event.data) as {
            type?: string;
            levels?: Record<string, unknown>;
          };
          if (data.type === 'audio-level' && data.levels) {
            const sourceIds = Object.keys(data.levels);
            if (sourceIds.length > 0) {
              globalThis.clearTimeout(timeout);
              sse.close(); // Close immediately — only needed for discovery
              resolve(sourceIds[0]);
            }
          }
        } catch {
          /* ignore parse errors */
        }
      };

      sse.onerror = () => {
        // ReconnectingEventSource handles reconnection automatically
      };
    });
  }

  async function start() {
    if (isActive || isConnecting) return;
    isConnecting = true;

    try {
      const sourceId = await discoverFirstSource();
      if (!sourceId) {
        logger.warn('MiniSpectrogram: no audio source found');
        isConnecting = false;
        return;
      }

      currentSourceId = sourceId;
      const encodedSourceId = encodeURIComponent(sourceId);

      // Request the backend to start the HLS stream
      await fetchWithCSRF(`/api/v2/streams/hls/${encodedSourceId}/start`, {
        method: 'POST',
      });

      const hlsUrl = buildAppUrl(`/api/v2/streams/hls/${encodedSourceId}/playlist.m3u8`);

      audioElement = new globalThis.Audio();
      audioElement.crossOrigin = 'anonymous';

      if (Hls.isSupported()) {
        hls = new Hls(HLS_AUDIO_CONFIG);
        hls.loadSource(hlsUrl);
        hls.attachMedia(audioElement);

        hls.on(Hls.Events.MANIFEST_PARSED, async () => {
          try {
            await audioElement?.play();
          } catch {
            /* autoplay blocked — spectrogram still renders */
          }
          // Connect composable after audio starts playing
          if (audioElement) {
            await spectro.connect(audioElement);
          }
        });

        hls.on(Hls.Events.ERROR, (_event, data) => {
          if (data.fatal) {
            logger.error('MiniSpectrogram: fatal HLS error', {
              type: data.type,
              details: data.details,
            });
            stop();
          }
        });
      } else if (audioElement.canPlayType('application/vnd.apple.mpegurl')) {
        // Native HLS (Safari / iOS)
        audioElement.src = hlsUrl;
        try {
          await audioElement.play();
        } catch {
          /* autoplay blocked */
        }
        await spectro.connect(audioElement);
      }

      // Guard: stop() may have been called during async setup (e.g., fatal HLS error).
      // If isConnecting was cleared by stop(), don't start heartbeat or transition to active.
      if (!isConnecting) return;

      startHeartbeat(sourceId);
      isActive = true;
      isConnecting = false;
      persistToggleState(true);
    } catch (error) {
      logger.error('MiniSpectrogram: failed to start', error);
      isConnecting = false;
    }
  }

  function startHeartbeat(sourceId: string) {
    stopHeartbeat();
    const sendHeartbeat = async () => {
      try {
        await fetchWithCSRF('/api/v2/streams/hls/heartbeat', {
          method: 'POST',
          body: { source_id: sourceId },
        });
      } catch {
        /* ignore heartbeat failures */
      }
    };
    sendHeartbeat();
    heartbeatTimer = globalThis.setInterval(sendHeartbeat, HEARTBEAT_INTERVAL);
  }

  function stopHeartbeat() {
    if (heartbeatTimer) {
      globalThis.clearInterval(heartbeatTimer);
      heartbeatTimer = null;
    }
  }

  function stop() {
    // Send disconnect heartbeat
    if (currentSourceId) {
      fetchWithCSRF('/api/v2/streams/hls/heartbeat?disconnect=true', {
        method: 'POST',
        body: { source_id: currentSourceId },
      }).catch(() => {});
    }

    stopHeartbeat();
    spectro.disconnect();

    if (hls) {
      hls.destroy();
      hls = null;
    }
    if (audioElement) {
      audioElement.pause();
      audioElement.removeAttribute('src');
      audioElement = null;
    }

    isActive = false;
    isConnecting = false;
    currentSourceId = '';
    persistToggleState(false);
  }

  function toggleAudio() {
    audioOutput = !audioOutput;
    spectro.setAudioOutput(audioOutput);
  }

  onMount(() => {
    if (hasAudioAccess && shouldAutoStart()) {
      start();
    }
    return () => stop();
  });
</script>

{#if hasAudioAccess}
  <div class="mt-4 rounded-2xl border border-border-100 bg-[var(--color-base-100)] p-3 shadow-sm">
    <div class="mb-2 flex items-center justify-between">
      <div class="flex items-center gap-2 text-sm font-medium">
        <Radio class="size-4" />
        <span>{t('spectrogram.dashboard.toggle')}</span>
      </div>
      <div class="flex items-center gap-1">
        {#if isActive}
          <button
            onclick={toggleAudio}
            class="btn btn-ghost btn-xs"
            aria-label={audioOutput
              ? t('spectrogram.controls.mute')
              : t('spectrogram.controls.unmute')}
          >
            {#if audioOutput}
              <Volume2 class="size-3.5" />
            {:else}
              <VolumeX class="size-3.5" />
            {/if}
          </button>
          <button onclick={stop} class="btn btn-ghost btn-xs" aria-label="Stop">
            <Square class="size-3.5" />
          </button>
        {:else}
          <button
            onclick={start}
            class="btn btn-ghost btn-xs"
            disabled={isConnecting}
            aria-label="Start"
          >
            <Play class="size-3.5" />
          </button>
        {/if}
      </div>
    </div>

    {#if (isActive || isConnecting) && spectro.isActive}
      <SpectrogramCanvas
        analyser={spectro.analyser}
        frequencyData={spectro.frequencyData}
        sampleRate={spectro.sampleRate}
        fftSize={FFT_SIZE}
        {frequencyRange}
        {colorMap}
        isActive={spectro.isActive}
        className="h-28 w-full rounded"
      />
    {:else if isActive || isConnecting}
      <div class="flex h-28 items-center justify-center rounded bg-black">
        <div
          class="h-5 w-5 animate-spin rounded-full border-2 border-[var(--color-primary)] border-t-transparent"
        ></div>
      </div>
    {/if}
  </div>
{/if}
