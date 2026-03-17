<!--
  LiveStreamPage.svelte — Full-page live audio spectrogram viewer

  Connects to the HLS audio stream via hls.js, feeds it through the
  useSpectrogramAnalyser composable, and renders a scrolling waterfall
  spectrogram. Includes source picker (discovered via SSE audio-level stream),
  spectrogram controls, and heartbeat mechanism to keep the backend stream alive.
-->

<script lang="ts">
  import Hls from 'hls.js';
  import ReconnectingEventSource from 'reconnecting-eventsource';
  import { onMount } from 'svelte';
  import { Radio, AlertCircle, Loader2, Play, Maximize, Minimize, Mic } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { HLS_AUDIO_CONFIG, BUFFERING_STRATEGY } from '$lib/desktop/components/ui/hls-config';
  import { useSpectrogramAnalyser } from '$lib/utils/useSpectrogramAnalyser.svelte';
  import SpectrogramCanvas from '$lib/desktop/components/media/SpectrogramCanvas.svelte';
  import SpectrogramControls from '$lib/desktop/components/media/SpectrogramControls.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { loggers } from '$lib/utils/logger';
  import type { ColorMapName } from '$lib/utils/spectrogramColorMaps';

  const logger = loggers.audio;
  const FFT_SIZE = 1024;
  const HEARTBEAT_INTERVAL = 20000;

  interface AudioLevelData {
    level: number;
    clipping: boolean;
    name?: string;
  }

  interface AudioLevels {
    [source: string]: AudioLevelData;
  }

  // Source discovery
  let sources = $state<Array<{ id: string; name: string }>>([]);
  let selectedSourceId = $state<string>('');

  // Connection state
  let connectionError = $state<string | null>(null);
  let isConnecting = $state(false);
  let isStreaming = $state(false);

  // Spectrogram config
  let frequencyRange = $state<[number, number]>([0, 15000]);
  let colorMap = $state<ColorMapName>('inferno');
  let gainDb = $state(0);
  let audioOutput = $state(true);

  // Internal state
  let hls: Hls | null = null;
  let audioElement: HTMLAudioElement | null = null;
  let eventSource: ReconnectingEventSource | null = null;
  let heartbeatTimer: ReturnType<typeof globalThis.setInterval> | null = null;
  let activeSourceId: string | null = null;

  // Initialize composable during component init (registers cleanup $effect)
  const spectro = useSpectrogramAnalyser({ fftSize: FFT_SIZE, audioOutput: true });

  // Source discovery via SSE
  function connectSSE() {
    if (eventSource) return;

    try {
      eventSource = new ReconnectingEventSource(buildAppUrl('/api/v2/streams/audio-level'), {
        max_retry_time: 30000,
        withCredentials: false,
      });

      eventSource.onmessage = event => {
        try {
          const data = JSON.parse(event.data) as { type?: string; levels?: AudioLevels };
          if (data.type === 'audio-level' && data.levels) {
            const newSources = Object.entries(data.levels).map(
              ([id, level]: [string, AudioLevelData]) => ({
                id,
                name: level.name ?? id,
              })
            );

            if (newSources.length > 0) {
              // Update sources if count changed or this is the first time
              if (sources.length === 0 || newSources.length !== sources.length) {
                sources = newSources;
              }
              // Auto-select first source if none selected
              if (!selectedSourceId && newSources.length > 0) {
                selectedSourceId = newSources[0].id;
              }
            }
          }
        } catch {
          // Ignore parse errors from SSE
        }
      };

      eventSource.onerror = () => {
        logger.debug('Audio level SSE error, will auto-reconnect');
      };
    } catch (error) {
      logger.error('Failed to create audio level SSE', error);
    }
  }

  async function startStream() {
    if (!selectedSourceId || isConnecting) return;

    await stopStream();
    isConnecting = true;
    connectionError = null;

    try {
      const encodedSourceId = encodeURIComponent(selectedSourceId);

      // Start HLS stream on backend
      await fetchWithCSRF(`/api/v2/streams/hls/${encodedSourceId}/start`, {
        method: 'POST',
      });

      const hlsUrl = buildAppUrl(`/api/v2/streams/hls/${encodedSourceId}/playlist.m3u8`);

      // Create audio element
      audioElement = new globalThis.Audio();
      audioElement.crossOrigin = 'anonymous';

      // Audio element debug listeners for buffer underrun diagnosis
      audioElement.addEventListener('waiting', () => {
        logger.warn('Audio element: waiting (buffer underrun)', {
          currentTime: audioElement?.currentTime,
          readyState: audioElement?.readyState,
          networkState: audioElement?.networkState,
        });
      });
      audioElement.addEventListener('stalled', () => {
        logger.warn('Audio element: stalled (network stall)', {
          currentTime: audioElement?.currentTime,
        });
      });
      audioElement.addEventListener('error', () => {
        const err = audioElement?.error;
        logger.error('Audio element: error', {
          code: err?.code,
          message: err?.message,
        });
      });

      if (Hls.isSupported()) {
        hls = new Hls(HLS_AUDIO_CONFIG);
        hls.loadSource(hlsUrl);
        hls.attachMedia(audioElement);

        let fragmentsBuffered = 0;
        let playbackAttempted = false;

        hls.on(Hls.Events.FRAG_BUFFERED, (_event, data) => {
          fragmentsBuffered++;

          // Buffer diagnostics
          const buffered = audioElement?.buffered;
          const currentTime = audioElement?.currentTime ?? 0;
          let bufferAhead = 0;
          if (buffered && buffered.length > 0) {
            bufferAhead = buffered.end(buffered.length - 1) - currentTime;
          }
          logger.debug('HLS frag buffered', {
            sn: data.frag.sn,
            total: fragmentsBuffered,
            bufferAhead: bufferAhead.toFixed(2) + 's',
          });

          if (
            !playbackAttempted &&
            fragmentsBuffered >= BUFFERING_STRATEGY.MIN_FRAGMENTS_BEFORE_PLAY
          ) {
            playbackAttempted = true;
            audioElement?.play().catch((err: Error) => {
              if (err.name === 'NotAllowedError') {
                logger.warn('Autoplay blocked by browser');
              } else {
                logger.warn('Playback start error', err);
              }
            });
          }
        });

        hls.on(Hls.Events.MANIFEST_PARSED, async () => {
          // Connect the spectrogram analyser once manifest is ready
          if (audioElement) {
            await spectro.connect(audioElement);
          }
          isStreaming = true;
          isConnecting = false;
        });

        hls.on(Hls.Events.ERROR, (_event, data) => {
          if (data.fatal) {
            connectionError = t('spectrogram.error.connectionFailed');
            logger.error('Fatal HLS error', { type: data.type, details: data.details });
            isStreaming = false;
            isConnecting = false;
          } else {
            // Non-fatal errors — log for debugging buffer issues
            const info: Record<string, unknown> = {
              type: data.type,
              details: data.details,
            };
            if ('frag' in data && data.frag) {
              info.fragSn = (data.frag as { sn?: number }).sn;
            }
            logger.warn('HLS non-fatal error', info);
          }
        });

        // Fragment loading log for diagnostics
        hls.on(Hls.Events.FRAG_LOADING, (_event, data) => {
          logger.debug('HLS frag loading', { sn: data.frag.sn, url: data.frag.relurl });
        });
      } else if (audioElement.canPlayType('application/vnd.apple.mpegurl')) {
        // Native HLS (Safari)
        audioElement.src = hlsUrl;
        try {
          await audioElement.play();
        } catch (e) {
          logger.warn('Autoplay blocked', e);
        }
        await spectro.connect(audioElement);
        isStreaming = true;
        isConnecting = false;
      } else {
        connectionError = t('spectrogram.error.unsupported');
        isConnecting = false;
        return;
      }

      // Track the active source and start heartbeat
      activeSourceId = selectedSourceId;
      startHeartbeat(selectedSourceId);
    } catch (error) {
      connectionError = t('spectrogram.error.connectionFailed');
      logger.error('Failed to start HLS stream', error);
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
        // Ignore heartbeat errors
      }
    };

    // Send an immediate heartbeat
    sendHeartbeat();
    heartbeatTimer = globalThis.setInterval(sendHeartbeat, HEARTBEAT_INTERVAL);
  }

  function stopHeartbeat() {
    if (heartbeatTimer) {
      globalThis.clearInterval(heartbeatTimer);
      heartbeatTimer = null;
    }
  }

  async function stopStream() {
    // Send disconnect heartbeat for the active source
    if (activeSourceId) {
      fetchWithCSRF('/api/v2/streams/hls/heartbeat?disconnect=true', {
        method: 'POST',
        body: { source_id: activeSourceId },
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

    activeSourceId = null;
    isStreaming = false;
  }

  // Source options for SelectDropdown
  let sourceOptions = $derived<SelectOption[]>(
    sources.map(s => ({ value: s.id, label: s.name, icon: Mic }))
  );

  function handleSourceChange(value: string | string[]) {
    const newId = Array.isArray(value) ? value[0] : value;
    if (newId && newId !== selectedSourceId) {
      selectedSourceId = newId;
      startStream();
    }
  }

  function handleAudioOutputToggle() {
    audioOutput = !audioOutput;
    spectro.setAudioOutput(audioOutput);
  }

  function handleGainChange(db: number) {
    gainDb = db;
    spectro.setGain(db);
  }

  onMount(() => {
    connectSSE();

    return () => {
      stopStream();
      if (eventSource) {
        eventSource.close();
        eventSource = null;
      }
    };
  });

  // Fullscreen support
  let cardEl: HTMLDivElement | undefined = $state();
  let isFullscreen = $state(false);

  function toggleFullscreen() {
    if (!cardEl) return;
    if (document.fullscreenElement) {
      document.exitFullscreen().catch(() => {});
    } else {
      cardEl.requestFullscreen().catch(() => {});
    }
  }

  // Track fullscreen state changes (user can also press Escape)
  $effect(() => {
    const handler = () => {
      isFullscreen = !!document.fullscreenElement;
    };
    document.addEventListener('fullscreenchange', handler);
    return () => document.removeEventListener('fullscreenchange', handler);
  });

  // No auto-start — browsers block AudioContext and audio.play() without a
  // user gesture. The user must click the play button to start streaming.
  function handleStartClick() {
    if (selectedSourceId) {
      startStream();
    }
  }
</script>

<div
  bind:this={cardEl}
  class={isFullscreen
    ? 'flex h-screen w-screen flex-col overflow-hidden bg-[var(--color-base-100)]'
    : 'col-span-12 flex flex-col h-[calc(100dvh-80px)] lg:h-[calc(100dvh-112px)] overflow-hidden rounded-2xl border border-border-100 bg-[var(--color-base-100)] shadow-sm'}
>
  <!-- Header bar -->
  <div class="flex flex-none items-center gap-4 border-b border-[var(--color-base-200)] px-4 py-3">
    <div class="flex items-center gap-2">
      <Radio class="size-5 text-[var(--color-primary)]" />
      <h1 class="text-lg font-semibold">{t('spectrogram.page.title')}</h1>
    </div>

    <!-- Source picker -->
    <div class="flex items-center gap-2">
      <span class="text-sm text-[var(--color-base-content)]/70">
        {t('spectrogram.page.sourceLabel')}
      </span>
      <SelectDropdown
        options={sourceOptions}
        value={selectedSourceId}
        placeholder={sources.length === 0
          ? t('common.loading') + '...'
          : t('spectrogram.page.sourceLabel')}
        variant="select"
        size="sm"
        groupBy={false}
        disabled={sources.length === 0}
        onChange={handleSourceChange}
        className="min-w-48 w-auto"
      />
    </div>

    <!-- Connection status indicator -->
    {#if isConnecting}
      <div class="flex items-center gap-1 text-sm text-[var(--color-base-content)]/60">
        <Loader2 class="size-4 animate-spin" />
        <span>{t('common.loading')}</span>
      </div>
    {:else if isStreaming}
      <div class="flex items-center gap-1 text-sm text-[var(--color-success)]">
        <span class="inline-block size-2 rounded-full bg-[var(--color-success)]"></span>
        <span>{t('spectrogram.page.connected')}</span>
      </div>
    {/if}

    <!-- Spacer -->
    <div class="flex-1"></div>

    <!-- Fullscreen toggle -->
    <button
      onclick={toggleFullscreen}
      class="btn btn-ghost btn-sm btn-square"
      aria-label={isFullscreen ? 'Exit fullscreen' : 'Enter fullscreen'}
    >
      {#if isFullscreen}
        <Minimize class="size-4" />
      {:else}
        <Maximize class="size-4" />
      {/if}
    </button>
  </div>

  <!-- Error alert -->
  {#if connectionError}
    <div
      role="alert"
      class="m-4 flex items-center gap-2 rounded-lg bg-[var(--color-error)]/10 p-3 text-sm text-[var(--color-error)]"
    >
      <AlertCircle class="size-4 shrink-0" />
      <span>{connectionError}</span>
    </div>
  {/if}

  <!-- Spectrogram canvas (fills remaining space) -->
  <div class="min-h-0 flex-1">
    {#if isStreaming || isConnecting}
      <SpectrogramCanvas
        analyser={spectro.analyser}
        frequencyData={spectro.frequencyData}
        sampleRate={spectro.sampleRate}
        fftSize={spectro.fftSize}
        {frequencyRange}
        {colorMap}
        isActive={spectro.isActive}
        className="h-full w-full"
      />
    {:else}
      <!-- Click to start — user gesture required for AudioContext -->
      <button
        onclick={handleStartClick}
        disabled={!selectedSourceId || sources.length === 0}
        class="flex h-full w-full flex-col items-center justify-center gap-3 bg-black text-[var(--color-base-content)]/60 transition-colors hover:text-[var(--color-base-content)]/80 disabled:cursor-not-allowed disabled:opacity-50"
      >
        {#if sources.length === 0}
          <Loader2 class="size-8 animate-spin" />
          <span class="text-sm">{t('common.loading')}...</span>
        {:else}
          <Play class="size-12" />
          <span class="text-sm">{t('spectrogram.page.sourceLabel')}</span>
        {/if}
      </button>
    {/if}
  </div>

  <!-- Controls bar -->
  <div class="flex-none border-t border-[var(--color-base-200)] px-4 py-2">
    <SpectrogramControls
      {frequencyRange}
      {colorMap}
      {gainDb}
      {audioOutput}
      onFrequencyRangeChange={range => (frequencyRange = range)}
      onColorMapChange={map => (colorMap = map)}
      onGainChange={handleGainChange}
      onAudioOutputToggle={handleAudioOutputToggle}
    />
  </div>
</div>
