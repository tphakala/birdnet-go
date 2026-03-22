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
  import { generateSessionId } from '$lib/utils/session';
  import { loggers } from '$lib/utils/logger';
  import type { ColorMapName } from '$lib/utils/spectrogramColorMaps';
  import { hasLiveAudioAccess } from '$lib/stores/appState.svelte';
  import type { PendingDetection } from '$lib/types/pending.types';
  import type { OverlayLabel, QueuedLabel } from '$lib/utils/detectionOverlay';
  import {
    diffPendingSnapshot,
    shouldDedup,
    promoteFromQueue,
    nextYSlot,
    STALE_DEDUP_PRUNE_SECONDS,
    LABEL_LEAD_IN_SECONDS,
  } from '$lib/utils/detectionOverlay';

  const logger = loggers.audio;
  const FFT_SIZE = 1024;
  const HEARTBEAT_INTERVAL = 20000;
  /** How often (ms) to poll for label promotion and pruning */
  const LABEL_POLL_INTERVAL_MS = 200;
  /** Maximum label age (ms) before pruning from overlay */
  const LABEL_MAX_AGE_MS = 60000;

  const sessionId = generateSessionId();

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

  // Detection overlay state
  let showDetectionLabels = $state(true);
  let debugOverlay = $state(false);
  let overlayLabels = $state<OverlayLabel[]>([]);
  let labelQueue: QueuedLabel[] = [];
  let prevSnapshot: PendingDetection[] = [];
  let lastSeenSpecies = new Map<string, number>();
  let slotCounter = 0;
  let detectionEventSource: ReconnectingEventSource | null = null;
  let currentWallClockAtPlayhead = $state(0);
  const MAX_OVERLAY_SLOTS = 7;

  // Internal state
  let hls: Hls | null = null;
  let audioElement = $state<HTMLAudioElement | null>(null);
  let eventSource: ReconnectingEventSource | null = null;
  let heartbeatTimer: ReturnType<typeof globalThis.setInterval> | null = null;
  let abortController: AbortController | null = null;
  let activeStreamToken: string | null = null;
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

  // Describe the buffered time ranges of an audio element for diagnostics.
  // Returns a string like "0.00-4.12, 6.00-8.50" showing all buffered ranges,
  // which helps identify buffer holes that cause audio gaps.
  function describeBuffered(el: HTMLAudioElement | null): string {
    if (!el?.buffered || el.buffered.length === 0) return 'empty';
    const ranges: string[] = [];
    for (let i = 0; i < el.buffered.length; i++) {
      ranges.push(`${el.buffered.start(i).toFixed(2)}-${el.buffered.end(i).toFixed(2)}`);
    }
    return ranges.join(', ');
  }

  async function startStream() {
    if (!selectedSourceId) return;

    await stopStream();
    isConnecting = true;
    connectionError = null;

    const controller = new AbortController();
    abortController = controller;
    const { signal } = controller;

    // Capture the source being started so stopStream() can clean up
    // even if the request is aborted after the server processes it
    activeSourceId = selectedSourceId;

    try {
      const encodedSourceId = encodeURIComponent(activeSourceId);

      // Start HLS stream on backend
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

      // Create audio element
      audioElement = new globalThis.Audio();
      audioElement.crossOrigin = 'anonymous';

      // Audio element debug listeners for buffer underrun diagnosis
      let hasStalled = false;
      audioElement.addEventListener('waiting', () => {
        hasStalled = true;
        logger.warn('Audio element: waiting (buffer underrun)', {
          currentTime: audioElement?.currentTime?.toFixed(3),
          readyState: audioElement?.readyState,
          networkState: audioElement?.networkState,
          buffered: describeBuffered(audioElement),
        });
      });
      audioElement.addEventListener('stalled', () => {
        hasStalled = true;
        logger.warn('Audio element: stalled (network stall)', {
          currentTime: audioElement?.currentTime?.toFixed(3),
          buffered: describeBuffered(audioElement),
        });
      });
      audioElement.addEventListener('playing', () => {
        if (!hasStalled) return; // Ignore initial play — only log stall recovery
        hasStalled = false;
        logger.warn('Audio element: resumed from stall', {
          currentTime: audioElement?.currentTime?.toFixed(3),
          buffered: describeBuffered(audioElement),
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

        // Fragment load timing: track how long each fragment takes to download.
        // Keyed by string to handle both numeric sn and 'initSegment'.
        const fragLoadStartTimes = new Map<string, number>();

        hls.on(Hls.Events.FRAG_LOADING, (_event, data) => {
          if (signal.aborted) return;
          const snKey = String(data.frag.sn);
          fragLoadStartTimes.set(snKey, globalThis.performance.now());
          // Cap map size to prevent leaks from abandoned loads
          if (fragLoadStartTimes.size > 20) {
            const oldest = fragLoadStartTimes.keys().next().value;
            if (oldest !== undefined) fragLoadStartTimes.delete(oldest);
          }
          logger.debug('HLS frag loading', { sn: data.frag.sn, url: data.frag.relurl });
        });

        hls.on(Hls.Events.FRAG_LOADED, (_event, data) => {
          if (signal.aborted) return;
          const snKey = String(data.frag.sn);
          const startTime = fragLoadStartTimes.get(snKey);
          fragLoadStartTimes.delete(snKey);

          const now = globalThis.performance.now();
          const loadMs = startTime ? now - startTime : 0;
          const loadDurationMs = startTime ? loadMs.toFixed(0) : '?';
          const bytes = data.payload?.byteLength ?? data.frag.stats?.total ?? 0;
          // Warn if fragment download took unusually long (>2s for a ~2s segment)
          if (loadMs > 2000) {
            logger.warn('Slow fragment download (possible network/server I/O stall)', {
              sn: data.frag.sn,
              loadDurationMs,
              fragDuration: data.frag.duration?.toFixed(2),
              bytes,
            });
          } else {
            logger.debug('HLS frag loaded', { sn: data.frag.sn, loadDurationMs, bytes });
          }
        });

        hls.on(Hls.Events.FRAG_BUFFERED, (_event, data) => {
          if (signal.aborted) return;
          fragmentsBuffered++;

          // Buffer diagnostics
          const bufferInfo = describeBuffered(audioElement);
          const currentTime = audioElement?.currentTime ?? 0;
          const buffered = audioElement?.buffered;
          let bufferAhead = 0;
          if (buffered && buffered.length > 0) {
            bufferAhead = buffered.end(buffered.length - 1) - currentTime;
          }

          // Warn if buffer is getting dangerously low
          if (playbackAttempted && bufferAhead < 1.0) {
            logger.warn('HLS buffer running low', {
              sn: data.frag.sn,
              bufferAhead: bufferAhead.toFixed(2) + 's',
              buffered: bufferInfo,
              currentTime: currentTime.toFixed(3),
            });
          } else {
            logger.debug('HLS frag buffered', {
              sn: data.frag.sn,
              total: fragmentsBuffered,
              bufferAhead: bufferAhead.toFixed(2) + 's',
            });
          }

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
          if (signal.aborted) return;
          // Connect the spectrogram analyser once manifest is ready
          if (audioElement) {
            await spectro.connect(audioElement);
          }
          if (signal.aborted) {
            spectro.disconnect();
            return;
          }
          isStreaming = true;
          isConnecting = false;
        });

        hls.on(Hls.Events.ERROR, (_event, data) => {
          if (signal.aborted) return;
          if (data.fatal) {
            connectionError = t('spectrogram.error.connectionFailed');
            logger.error('Fatal HLS error', {
              type: data.type,
              details: data.details,
              buffered: describeBuffered(audioElement),
              currentTime: audioElement?.currentTime?.toFixed(3),
            });
            isStreaming = false;
            isConnecting = false;
          } else {
            // Non-fatal errors — log for debugging buffer issues
            const info: Record<string, unknown> = {
              type: data.type,
              details: data.details,
              buffered: describeBuffered(audioElement),
              currentTime: audioElement?.currentTime?.toFixed(3),
            };
            if ('frag' in data && data.frag) {
              const fragSn = (data.frag as { sn?: number | string }).sn;
              info.fragSn = fragSn;
              // Clean up fragment timing entry on error (FRAG_LOADED won't fire)
              fragLoadStartTimes.delete(String(fragSn));
            }
            // Buffer stall events are key diagnostic signals
            if (
              data.details === 'bufferStalledError' ||
              data.details === 'bufferNudgeOnStall' ||
              data.details === 'bufferSeekOverHole'
            ) {
              logger.warn('HLS buffer stall event', info);
            } else {
              logger.warn('HLS non-fatal error', info);
            }
          }
        });

        // Level/track switching diagnostics
        hls.on(Hls.Events.BUFFER_FLUSHING, () => {
          if (signal.aborted) return;
          logger.warn('HLS buffer flushing', {
            currentTime: audioElement?.currentTime?.toFixed(3),
            buffered: describeBuffered(audioElement),
          });
        });
      } else if (audioElement.canPlayType('application/vnd.apple.mpegurl')) {
        // Native HLS (Safari)
        audioElement.src = hlsUrl;
        try {
          await audioElement.play();
        } catch (e) {
          if ((e as Error).name === 'NotAllowedError') {
            logger.warn('Autoplay blocked by browser');
          }
        }
        if (signal.aborted || !audioElement) return;
        await spectro.connect(audioElement);
        if (signal.aborted) {
          spectro.disconnect();
          return;
        }
        isStreaming = true;
        isConnecting = false;
      } else {
        connectionError = t('spectrogram.error.unsupported');
        isConnecting = false;
        return;
      }

      // Guard: abort may have fired during async setup
      if (signal.aborted) return;

      startHeartbeat(activeStreamToken!);
      if (activeSourceId) {
        connectDetectionStream(activeSourceId);
      }
    } catch (error) {
      if (signal.aborted) return;
      connectionError = t('spectrogram.error.connectionFailed');
      logger.error('Failed to start HLS stream', error);
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

  function connectDetectionStream(sourceId: string) {
    disconnectDetectionStream();
    detectionEventSource = new ReconnectingEventSource(buildAppUrl('/api/v2/detections/stream'), {
      max_retry_time: 30000,
      withCredentials: false,
    });
    detectionEventSource.addEventListener('pending', (event: Event) => {
      try {
        // eslint-disable-next-line no-undef
        const messageEvent = event as MessageEvent;
        const data = JSON.parse(messageEvent.data);
        if (!Array.isArray(data)) return;
        const curr = data as PendingDetection[];
        const newDetections = diffPendingSnapshot(prevSnapshot, curr, sourceId);

        const nowUnix = Date.now() / 1000;

        for (const det of newDetections) {
          if (shouldDedup(det.species, nowUnix, lastSeenSpecies)) continue;
          lastSeenSpecies.set(det.species, nowUnix);
          const { slot, next } = nextYSlot(slotCounter, MAX_OVERLAY_SLOTS);
          slotCounter = next;
          labelQueue.push({
            text: det.species,
            firstDetected: (det.audioCapturedAt ?? det.firstDetected) - LABEL_LEAD_IN_SECONDS,
            ySlot: slot,
          });
        }
        prevSnapshot = curr;
      } catch {
        // Ignore parse errors
      }
    });
  }

  function disconnectDetectionStream() {
    detectionEventSource?.close();
    detectionEventSource = null;
    prevSnapshot = [];
    lastSeenSpecies.clear();
    labelQueue = [];
    overlayLabels = [];
    slotCounter = 0;
  }

  async function stopStream() {
    // Abort in-flight async work first
    abortController?.abort();
    abortController = null;

    // Send explicit stop for the source that actually has an active stream
    // (selectedSourceId may have changed if user switched sources before stop)
    if (activeSourceId) {
      const encodedSourceId = encodeURIComponent(activeSourceId);
      fetchWithCSRF(`/api/v2/streams/hls/${encodedSourceId}/stop`, {
        method: 'POST',
        keepalive: true,
        body: { session_id: sessionId },
      }).catch(() => {});
    }

    // Also send disconnect heartbeat as fallback (keepalive ensures delivery during page unload)
    if (activeStreamToken) {
      fetchWithCSRF('/api/v2/streams/hls/heartbeat?disconnect=true', {
        method: 'POST',
        keepalive: true,
        body: { stream_token: activeStreamToken, session_id: sessionId },
      }).catch(() => {});
    }

    stopHeartbeat();
    disconnectDetectionStream();
    currentWallClockAtPlayhead = 0;
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

    activeStreamToken = null;
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

  // Use onMount (NOT $effect) to avoid reactive dependency loops.
  // See MiniSpectrogram.svelte for detailed explanation.
  onMount(() => {
    if (hasLiveAudioAccess()) {
      connectSSE();
    }

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

  // Toggle debug overlay with D key
  $effect(() => {
    const handler = (e: KeyboardEvent) => {
      // Ignore if any modifier key is held (browser shortcuts like Ctrl+D)
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      if (e.key === 'd' || e.key === 'D') {
        // Ignore if typing in an input/textarea
        const tag = (e.target as HTMLElement)?.tagName;
        if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
        debugOverlay = !debugOverlay;
        logger.info('Debug overlay', { enabled: debugOverlay });
      }
    };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  });

  // No auto-start — browsers block AudioContext and audio.play() without a
  // user gesture. The user must click the play button to start streaming.
  function handleStartClick() {
    if (selectedSourceId) {
      startStream();
    }
  }

  // Periodic buffer health monitor — logs buffer state every 10s to help
  // diagnose intermittent audio gaps. Uses warn level so it appears in production.
  $effect(() => {
    if (!audioElement || !isStreaming) return;

    let lastCurrentTime = audioElement.currentTime;
    let lastCheckTime = globalThis.performance.now();
    let stallCount = 0;

    const interval = globalThis.setInterval(() => {
      if (!audioElement) return;

      const now = globalThis.performance.now();
      const elapsed = (now - lastCheckTime) / 1000;
      const currentTime = audioElement.currentTime;
      const playbackDelta = currentTime - lastCurrentTime;

      // Detect playback stall: if currentTime hasn't advanced in 10s while
      // we're supposed to be streaming, something is wrong.
      // Skip when paused (e.g., browser tab backgrounded) to avoid false positives.
      if (elapsed > 9 && playbackDelta < 0.5 && currentTime > 0 && !audioElement.paused) {
        stallCount++;
        logger.warn('Playback stall detected: currentTime not advancing', {
          currentTime: currentTime.toFixed(3),
          expectedAdvance: elapsed.toFixed(1) + 's',
          actualAdvance: playbackDelta.toFixed(3) + 's',
          stallCount,
          readyState: audioElement.readyState,
          paused: audioElement.paused,
          buffered: describeBuffered(audioElement),
          hlsLatency: hls?.latency?.toFixed(2),
        });
      }

      // Periodic buffer health snapshot (every 10s)
      const buffered = audioElement.buffered;
      let bufferAhead = 0;
      let bufferRangeCount = 0;
      if (buffered && buffered.length > 0) {
        bufferAhead = buffered.end(buffered.length - 1) - currentTime;
        bufferRangeCount = buffered.length;
      }

      const healthInfo = {
        currentTime: currentTime.toFixed(3),
        bufferAhead: bufferAhead.toFixed(2) + 's',
        bufferRanges: bufferRangeCount,
        buffered: describeBuffered(audioElement),
        readyState: audioElement.readyState,
        hlsLatency: hls?.latency?.toFixed(2),
        hlsBandwidth: hls?.bandwidthEstimate
          ? (hls.bandwidthEstimate / 1000).toFixed(0) + 'kbps'
          : undefined,
      };

      // Use warn for unhealthy state (low buffer, multiple ranges = holes),
      // debug for normal operation to avoid spamming production console
      if (bufferAhead < 2.0 || bufferRangeCount > 1) {
        logger.warn('HLS buffer health: degraded', healthInfo);
      } else {
        logger.debug('HLS buffer health', healthInfo);
      }

      lastCurrentTime = currentTime;
      lastCheckTime = now;
    }, 10000);

    return () => globalThis.clearInterval(interval);
  });

  // Promote queued detection labels when playhead catches up.
  // Prefers hls.playingDate (accurate), falls back to seekable-based
  // estimate for native HLS (Safari/iOS).
  $effect(() => {
    if (!audioElement) return;

    const interval = globalThis.setInterval(() => {
      if (!audioElement) return;

      const now = globalThis.performance.now();
      const nowUnix = Date.now() / 1000;

      // Compute wall-clock at playhead: prefer hls.playingDate (accurate),
      // fall back to seekable-based estimate for native HLS (Safari/iOS).
      let wallClockAtPlayhead = 0;
      if (hls?.playingDate) {
        wallClockAtPlayhead = hls.playingDate.getTime() / 1000;
      } else if (audioElement.currentTime > 0 && audioElement.seekable.length > 0) {
        const liveEdge = audioElement.seekable.end(audioElement.seekable.length - 1);
        const liveLagSeconds = Math.max(0, liveEdge - audioElement.currentTime);
        wallClockAtPlayhead = nowUnix - liveLagSeconds;
      }

      // Update reactive state for debug display in SpectrogramCanvas
      if (wallClockAtPlayhead > 0) {
        currentWallClockAtPlayhead = wallClockAtPlayhead;
      }

      // Promote queued labels when playhead is available
      if (wallClockAtPlayhead > 0 && labelQueue.length > 0) {
        const { promoted, remaining } = promoteFromQueue(labelQueue, wallClockAtPlayhead, now);
        if (promoted.length > 0) {
          labelQueue = remaining;
          overlayLabels = [...overlayLabels, ...promoted];
        }
      }

      // Prune labels older than LABEL_MAX_AGE_MS — runs regardless of playingDate
      // availability so labels don't freeze on screen during buffer stalls
      const cutoff = now - LABEL_MAX_AGE_MS;
      overlayLabels = overlayLabels.filter(l => l.birthTime >= cutoff);

      // Prune stale dedup entries using client wall-clock time (consistent
      // with SSE handler which also uses Date.now() for dedup tracking)
      for (const [species, time] of lastSeenSpecies) {
        if (nowUnix - time > STALE_DEDUP_PRUNE_SECONDS) {
          lastSeenSpecies.delete(species);
        }
      }
    }, LABEL_POLL_INTERVAL_MS);

    return () => globalThis.clearInterval(interval);
  });
</script>

{#if hasLiveAudioAccess()}
  <div
    bind:this={cardEl}
    class={isFullscreen
      ? 'flex h-screen w-screen flex-col overflow-hidden bg-[var(--color-base-100)]'
      : 'col-span-12 flex flex-col h-[calc(100dvh-80px)] lg:h-[calc(100dvh-112px)] overflow-hidden rounded-2xl border border-border-100 bg-[var(--color-base-100)] shadow-sm'}
  >
    <!-- Header bar -->
    <div
      class="flex flex-none items-center gap-4 border-b border-[var(--color-base-200)] px-4 py-3"
    >
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
        class="inline-flex items-center justify-center rounded-lg p-1.5 text-[var(--color-base-content)]/70 transition-colors hover:bg-[var(--color-base-200)] hover:text-[var(--color-base-content)]"
        aria-label={isFullscreen
          ? t('spectrogram.page.exitFullscreen')
          : t('spectrogram.page.enterFullscreen')}
        title={isFullscreen
          ? t('spectrogram.page.exitFullscreen')
          : t('spectrogram.page.enterFullscreen')}
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
          overlayLabels={showDetectionLabels ? overlayLabels : []}
          debug={debugOverlay}
          wallClockAtPlayhead={currentWallClockAtPlayhead}
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
        {showDetectionLabels}
        onFrequencyRangeChange={range => (frequencyRange = range)}
        onColorMapChange={map => (colorMap = map)}
        onGainChange={handleGainChange}
        onAudioOutputToggle={handleAudioOutputToggle}
        onDetectionLabelsToggle={() => {
          showDetectionLabels = !showDetectionLabels;
        }}
      />
    </div>
  </div>
{:else}
  <div
    class="col-span-12 flex flex-col items-center justify-center gap-4 rounded-2xl border border-border-100 bg-[var(--color-base-100)] p-12 shadow-sm"
  >
    <AlertCircle class="size-8 text-[var(--color-warning)]" />
    <p class="text-center text-[var(--color-base-content)]/70">
      {t('spectrogram.error.accessDenied')}
    </p>
  </div>
{/if}
