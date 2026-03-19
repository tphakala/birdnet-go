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
  import { Volume, Volume1, Volume2, VolumeX, Play, Square } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { appState, hasLiveAudioAccess } from '$lib/stores/appState.svelte';
  import { HLS_AUDIO_CONFIG } from '$lib/desktop/components/ui/hls-config';
  import { useSpectrogramAnalyser } from '$lib/utils/useSpectrogramAnalyser.svelte';
  import SpectrogramCanvas from '$lib/desktop/components/media/SpectrogramCanvas.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { generateSessionId } from '$lib/utils/session';
  import { loggers } from '$lib/utils/logger';
  import type { ColorMapName } from '$lib/utils/spectrogramColorMaps';

  const logger = loggers.audio;
  const STORAGE_KEY = 'birdnet-spectrogram-active';
  const FFT_SIZE = 1024;
  const HEARTBEAT_INTERVAL = 20000;
  const SOURCE_DISCOVERY_TIMEOUT = 5000;

  const sessionId = generateSessionId();

  // Volume/gain presets: muted → 0dB → +6dB → +12dB
  const GAIN_PRESETS = [
    { db: -Infinity, audio: false, labelKey: 'spectrogram.gain.muted' },
    { db: 0, audio: true, labelKey: 'spectrogram.gain.level', value: '0' },
    { db: 6, audio: true, labelKey: 'spectrogram.gain.level', value: '+6' },
    { db: 12, audio: true, labelKey: 'spectrogram.gain.level', value: '+12' },
  ] as const;

  // Local state
  let isActive = $state(false);
  let isConnecting = $state(false);
  let gainPresetIndex = $state(0);

  const gainLabel = $derived.by(() => {
    const preset = GAIN_PRESETS[gainPresetIndex];
    return 'value' in preset ? t(preset.labelKey, { value: preset.value }) : t(preset.labelKey);
  });
  let colorMap = $state<ColorMapName>('inferno');
  let frequencyRange = $state<[number, number]>([0, 15000]);

  // Stream token state
  let activeStreamToken: string | null = null;

  // HLS + audio refs
  let hls: Hls | null = null;
  let audioElement: HTMLAudioElement | null = null;
  let heartbeatTimer: ReturnType<typeof globalThis.setInterval> | null = null;
  let abortController: AbortController | null = null;

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
   * Accepts an AbortSignal so callers can cancel discovery mid-flight.
   */
  async function discoverFirstSource(signal: AbortSignal): Promise<string | null> {
    return new Promise(resolve => {
      if (signal.aborted) {
        resolve(null);
        return;
      }

      const sse = new ReconnectingEventSource(buildAppUrl('/api/v2/streams/audio-level'), {
        max_retry_time: 30000,
        withCredentials: false,
      });

      // Abort listener: clean up SSE and timeout if caller cancels
      const onAbort = () => {
        globalThis.clearTimeout(timeout);
        sse.close();
        resolve(null);
      };

      const timeout = globalThis.setTimeout(() => {
        signal.removeEventListener('abort', onAbort);
        sse.close();
        resolve(null);
      }, SOURCE_DISCOVERY_TIMEOUT);

      signal.addEventListener('abort', onAbort, { once: true });

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
              signal.removeEventListener('abort', onAbort);
              sse.close();
              resolve(sourceIds[0]);
            }
          }
        } catch {
          /* ignore parse errors */
        }
      };

      sse.onerror = () => {
        /* ReconnectingEventSource handles reconnection */
      };
    });
  }

  async function start() {
    if (isActive || isConnecting) return;
    isConnecting = true;

    // Abort any previous in-flight operation
    abortController?.abort();
    const controller = new AbortController();
    abortController = controller;
    const { signal } = controller;

    try {
      const sourceId = await discoverFirstSource(signal);
      if (signal.aborted || !sourceId) {
        if (!signal.aborted) isConnecting = false;
        return;
      }

      const encodedSourceId = encodeURIComponent(sourceId);

      const data = await fetchWithCSRF<{
        status: string;
        stream_token: string;
        playlist_url: string;
        playlist_ready: boolean;
      }>(`/api/v2/streams/hls/${encodedSourceId}/start`, {
        method: 'POST',
        signal,
        body: { session_id: sessionId },
      });

      if (signal.aborted) return;

      activeStreamToken = data.stream_token;
      const hlsUrl = buildAppUrl(data.playlist_url);

      audioElement = new globalThis.Audio();
      audioElement.crossOrigin = 'anonymous';

      if (Hls.isSupported()) {
        hls = new Hls(HLS_AUDIO_CONFIG);
        hls.loadSource(hlsUrl);
        hls.attachMedia(audioElement);

        hls.on(Hls.Events.MANIFEST_PARSED, async () => {
          if (signal.aborted) return;
          try {
            await audioElement?.play();
          } catch {
            /* autoplay blocked — spectrogram still renders */
          }
          if (signal.aborted) return;
          if (audioElement) {
            await spectro.connect(audioElement);
          }
          if (signal.aborted) {
            spectro.disconnect();
            return;
          }
          startHeartbeat(activeStreamToken!);
          isActive = true;
          isConnecting = false;
          persistToggleState(true);
        });

        hls.on(Hls.Events.ERROR, (_event, data) => {
          if (signal.aborted) return;
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
        if (signal.aborted || !audioElement) return;
        await spectro.connect(audioElement);
        if (signal.aborted) {
          spectro.disconnect();
          return;
        }
        startHeartbeat(activeStreamToken!);
        isActive = true;
        isConnecting = false;
        persistToggleState(true);
      } else {
        // Browser supports neither HLS.js nor native HLS — tear down
        logger.warn('MiniSpectrogram: browser does not support HLS');
        stop();
        return;
      }
    } catch (error) {
      if (signal.aborted) return;
      logger.error('MiniSpectrogram: failed to start', error);
      isConnecting = false;
    }
  }

  function startHeartbeat(token: string) {
    stopHeartbeat();
    const sendHeartbeat = async () => {
      try {
        await fetchWithCSRF('/api/v2/streams/hls/heartbeat', {
          method: 'POST',
          body: { stream_token: token, session_id: sessionId },
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
    // Abort any in-flight async work first
    abortController?.abort();
    abortController = null;

    // Send disconnect heartbeat
    if (activeStreamToken) {
      fetchWithCSRF('/api/v2/streams/hls/heartbeat?disconnect=true', {
        method: 'POST',
        keepalive: true,
        body: { stream_token: activeStreamToken, session_id: sessionId },
      }).catch(() => {});
      activeStreamToken = null;
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
    persistToggleState(false);
  }

  function cycleVolume() {
    gainPresetIndex = (gainPresetIndex + 1) % GAIN_PRESETS.length;
    const preset = GAIN_PRESETS[gainPresetIndex];
    spectro.setAudioOutput(preset.audio);
    if (preset.audio) {
      spectro.setGain(preset.db);
    }
  }

  onMount(() => {
    if (hasLiveAudioAccess() && shouldAutoStart()) {
      start();
    }
    return () => stop();
  });
</script>

{#if hasLiveAudioAccess()}
  <div
    class="overflow-hidden rounded-2xl border border-border-100 bg-[var(--color-base-100)] shadow-sm"
  >
    <div
      class="flex items-center justify-between border-b border-[var(--color-base-200)] px-6 py-4"
    >
      <h3 class="font-semibold">{t('spectrogram.dashboard.toggle')}</h3>
      <div class="flex items-center gap-1">
        {#if isActive}
          <button
            onclick={cycleVolume}
            class="rounded p-1 transition-colors hover:bg-[var(--color-base-200)]"
            aria-label={gainLabel}
            title={gainLabel}
          >
            {#if gainPresetIndex === 0}
              <VolumeX class="size-4" />
            {:else if gainPresetIndex === 1}
              <Volume class="size-4" />
            {:else if gainPresetIndex === 2}
              <Volume1 class="size-4" />
            {:else}
              <Volume2 class="size-4" />
            {/if}
          </button>
          <button
            onclick={stop}
            class="rounded p-1 transition-colors hover:bg-[var(--color-base-200)]"
            aria-label={t('media.audio.stop')}
          >
            <Square class="size-4" />
          </button>
        {:else}
          <button
            onclick={start}
            class="rounded p-1 transition-colors hover:bg-[var(--color-base-200)]"
            disabled={isConnecting}
            aria-label={t('media.audio.play')}
          >
            <Play class="size-4" />
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
        className="h-28 w-full"
      />
    {:else if isActive || isConnecting}
      <div class="flex h-28 items-center justify-center bg-black">
        <div
          class="h-5 w-5 animate-spin rounded-full border-2 border-[var(--color-primary)] border-t-transparent"
        ></div>
      </div>
    {/if}
  </div>
{/if}
