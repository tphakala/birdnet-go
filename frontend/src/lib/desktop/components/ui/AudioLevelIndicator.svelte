<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import ReconnectingEventSource from 'reconnecting-eventsource';
  import { Mic, CirclePlay, CircleStop, Check } from '@lucide/svelte';
  import { loggers } from '$lib/utils/logger';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import Hls from 'hls.js';
  import type { ErrorData } from 'hls.js';
  import { HLS_AUDIO_CONFIG, BUFFERING_STRATEGY, ERROR_HANDLING } from './hls-config';

  const logger = loggers.audio;

  interface AudioLevelData {
    level: number;
    clipping: boolean;
    name?: string;
  }

  interface AudioLevels {
    [source: string]: AudioLevelData;
  }

  interface Props {
    className?: string;
    securityEnabled?: boolean;
    accessAllowed?: boolean;
  }

  // PERFORMANCE OPTIMIZATION: Cache HLS availability check with $derived
  // Now using imported Hls instead of global window.Hls
  let hlsSupported = $derived(typeof window !== 'undefined' && Hls.isSupported());

  let { className = '', securityEnabled = false, accessAllowed = true }: Props = $props();

  // State
  let levels = $state<AudioLevels>({});
  let selectedSource = $state<string | null>(null);
  let smoothedVolumes = $state<{ [key: string]: number }>({});
  let dropdownOpen = $state(false);
  let isPlaying = $state(false);
  let playingSource = $state<string | null>(null);
  let showStatus = $state(false);
  let statusMessage = $state('');

  // Constants
  const SMOOTHING_FACTOR = 0.4;
  const ZERO_LEVEL_TIMEOUT = 5000;
  const HEARTBEAT_INTERVAL = 20000;

  // Internal state
  let eventSource: ReconnectingEventSource | null = null;
  let audioElement: HTMLAudioElement | null = null;
  let hlsInstance: Hls | null = null;
  let zeroLevelTime: { [key: string]: number } = {};
  let heartbeatTimer: ReturnType<typeof globalThis.setInterval> | null = null;
  let dropdownRef = $state<HTMLDivElement>();
  let buttonRef = $state<HTMLButtonElement>();

  // PERFORMANCE OPTIMIZATION: Cache computed values with $derived
  // Reduces repeated object property access and boolean logic in templates
  const isClipping = $derived(
    // eslint-disable-next-line security/detect-object-injection
    selectedSource && levels[selectedSource] ? levels[selectedSource].clipping : false
  );

  // eslint-disable-next-line security/detect-object-injection
  const smoothedVolume = $derived(selectedSource ? smoothedVolumes[selectedSource] || 0 : 0);

  // PERFORMANCE OPTIMIZATION: Cache audio element creation with $derived.by
  // Prevents repeated DOM element creation and event listener setup
  let audioElementRef: HTMLAudioElement | null = null;
  let cachedAudioElement = $derived.by(() => {
    if (!audioElementRef && typeof window !== 'undefined') {
      audioElementRef = document.createElement('audio');
      audioElementRef.id = 'hls-audio-player';
      audioElementRef.setAttribute('aria-hidden', 'true');
      audioElementRef.classList.add('sr-only');
      audioElementRef.setAttribute('preload', 'auto');
      audioElementRef.setAttribute('playsinline', '');

      // Setup event listeners once
      audioElementRef.addEventListener('playing', () => {
        isPlaying = true;
        hideStatusMessage();
        setupMediaSession(playingSource || '');
      });

      audioElementRef.addEventListener('pause', () => {
        isPlaying = false;
      });

      audioElementRef.addEventListener('ended', () => {
        isPlaying = false;
      });

      audioElementRef.addEventListener('error', _e => {
        showStatusMessage('Playback error');
        isPlaying = false;
        stopPlayback();
      });

      document.body.appendChild(audioElementRef);
    }
    return audioElementRef;
  });

  // Check if source is inactive
  function isInactive(source: string): boolean {
    // eslint-disable-next-line security/detect-object-injection
    if (!levels[source]) return true;
    // eslint-disable-next-line security/detect-object-injection
    if (levels[source].level > 0) return false;

    // eslint-disable-next-line security/detect-object-injection
    if (!zeroLevelTime[source]) {
      // eslint-disable-next-line security/detect-object-injection
      zeroLevelTime[source] = Date.now();
      return false;
    }

    // eslint-disable-next-line security/detect-object-injection
    return Date.now() - zeroLevelTime[source] > ZERO_LEVEL_TIMEOUT;
  }

  // Get display name for source
  function getSourceDisplayName(source: string): string {
    // eslint-disable-next-line security/detect-object-injection
    return levels[source]?.name || source;
  }

  // Setup EventSource for audio levels using ReconnectingEventSource
  function setupEventSource() {
    if (eventSource) return;

    cleanupEventSource();

    try {
      // ReconnectingEventSource with configuration
      eventSource = new ReconnectingEventSource(buildAppUrl('/api/v2/streams/audio-level'), {
        max_retry_time: 30000, // Max 30 seconds between reconnection attempts
        withCredentials: false, // Set to true if you need CORS credentials
      });

      eventSource.onopen = () => {
        logger.debug('Audio level SSE connection opened');
      };

      eventSource.onmessage = event => {
        try {
          const data = JSON.parse(event.data);
          if (data.type === 'audio-level' && data.levels) {
            // Update levels
            levels = data.levels;

            // Track zero level times
            Object.entries(levels).forEach(([source, levelData]) => {
              const audioData = levelData as AudioLevelData;
              if (audioData.level === 0) {
                // eslint-disable-next-line security/detect-object-injection
                if (!zeroLevelTime[source]) {
                  // eslint-disable-next-line security/detect-object-injection
                  zeroLevelTime[source] = Date.now();
                }
              } else {
                // eslint-disable-next-line security/detect-object-injection
                delete zeroLevelTime[source];
              }
            });

            // Initialize smoothed volumes for new sources
            Object.keys(levels).forEach(source => {
              if (!(source in smoothedVolumes)) {
                // eslint-disable-next-line security/detect-object-injection
                smoothedVolumes[source] = 0;
              }
            });

            // Set first source as selected if none selected
            if (!selectedSource || !(selectedSource in levels)) {
              const sources = Object.keys(levels);
              if (sources.length > 0) {
                selectedSource = sources[0];
              }
            }

            // Update smoothed volumes
            Object.entries(levels).forEach(([source, levelData]) => {
              const audioData = levelData as AudioLevelData;
              // eslint-disable-next-line security/detect-object-injection
              const oldVolume = smoothedVolumes[source] || 0;
              // eslint-disable-next-line security/detect-object-injection
              smoothedVolumes[source] =
                SMOOTHING_FACTOR * audioData.level + (1 - SMOOTHING_FACTOR) * oldVolume;
            });
          }
        } catch (error) {
          logger.error('Failed to parse audio level data:', error);
        }
      };

      eventSource.onerror = (error: Event) => {
        logger.error('Audio level SSE error:', error);
        // ReconnectingEventSource handles reconnection automatically
        // No need for manual reconnection logic
      };
    } catch (error) {
      logger.error('Failed to create ReconnectingEventSource:', error);
      // Try again in 5 seconds if initial setup fails
      globalThis.setTimeout(() => setupEventSource(), 5000);
    }
  }

  // Cleanup EventSource
  function cleanupEventSource() {
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
  }

  // Show status message
  function showStatusMessage(message: string) {
    statusMessage = message;
    showStatus = true;
  }

  // Hide status message
  function hideStatusMessage() {
    showStatus = false;
  }

  // PERFORMANCE OPTIMIZATION: Use cached audio element from $derived
  // Eliminates repeated DOM element creation and conditional logic
  function getAudioElement(): HTMLAudioElement | null {
    return cachedAudioElement;
  }

  // Setup media session API
  function setupMediaSession(source: string) {
    if ('mediaSession' in navigator) {
      const sourceName = source ? getSourceDisplayName(source) : '';
      navigator.mediaSession.metadata = new globalThis.MediaMetadata({
        title: sourceName ? `Bird Audio Stream - ${sourceName}` : 'Bird Audio Stream',
        artist: 'BirdNet-Go',
        album: 'Live Stream',
      });

      navigator.mediaSession.setActionHandler('play', () => {
        audioElement?.play();
      });

      navigator.mediaSession.setActionHandler('pause', () => {
        audioElement?.pause();
      });

      navigator.mediaSession.playbackState = 'playing';
    }
  }

  // Start heartbeat
  function startHeartbeat() {
    stopHeartbeat();

    const sendHeartbeat = async () => {
      if (!isPlaying || !playingSource) return;

      try {
        await fetchWithCSRF('/api/v2/streams/hls/heartbeat', {
          method: 'POST',
          body: { source_id: playingSource },
        });
      } catch {
        // Failed to send heartbeat - ignore
      }
    };

    sendHeartbeat();
    heartbeatTimer = globalThis.setInterval(sendHeartbeat, HEARTBEAT_INTERVAL);
  }

  // Stop heartbeat
  function stopHeartbeat() {
    if (heartbeatTimer) {
      globalThis.clearInterval(heartbeatTimer);
      heartbeatTimer = null;
    }

    // Send disconnect notification
    if (playingSource) {
      fetchWithCSRF('/api/v2/streams/hls/heartbeat?disconnect=true', {
        method: 'POST',
        body: { source_id: playingSource },
      }).catch(() => {
        // Ignore errors during disconnect
      });
    }
  }

  // Setup HLS streaming
  async function setupHLSStream(hlsUrl: string, _sourceId: string) {
    const audio = getAudioElement();
    if (!audio) return;

    // Check if HLS.js is supported
    if (hlsSupported) {
      // Clean up existing instance
      if (hlsInstance) {
        hlsInstance.destroy();
        hlsInstance = null;
      }

      // Create new HLS instance with audio-optimized configuration
      hlsInstance = new Hls(HLS_AUDIO_CONFIG);

      // Track if we've attempted to play
      let playbackAttempted = false;
      let fragmentsBuffered = 0;

      // Setup error handling with proper types
      hlsInstance.on(Hls.Events.ERROR, (_event: string, data: ErrorData) => {
        logger.error('HLS error:', {
          type: data.type,
          details: data.details,
          fatal: data.fatal,
          error: data.error.message,
        });

        if (data.fatal) {
          // Handle fatal HLS error
          showStatusMessage('Playback error: ' + data.details);
          stopPlayback();
        } else if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
          // Handle non-fatal media errors with improved categorization
          if (ERROR_HANDLING.EXPECTED_STALL_ERRORS.includes(data.details as any)) {
            // Buffer stalls are expected in low-latency audio streaming
            // HLS.js will automatically handle recovery by buffering more segments
            logger.debug('Buffer stalled (expected for low-latency audio)', {
              details: data.details,
              bufferInfo: data.bufferInfo,
            });
          } else if (ERROR_HANDLING.RECOVERABLE_MEDIA_ERRORS.includes(data.details as any)) {
            // Try to recover from other recoverable media errors
            logger.warn('Attempting to recover from recoverable media error', {
              details: data.details,
            });
            hlsInstance?.recoverMediaError();
          } else {
            // Log unexpected media errors for investigation
            logger.warn('Unexpected non-fatal media error', {
              details: data.details,
              error: data.error.message,
            });
          }
        }
      });

      // Wait for media to be attached
      hlsInstance.on(Hls.Events.MEDIA_ATTACHED, () => {
        logger.debug('HLS.js media attached');
      });

      // Wait for manifest to be parsed
      hlsInstance.on(Hls.Events.MANIFEST_PARSED, () => {
        logger.debug('HLS.js manifest parsed');
      });

      // Wait for first fragment to be buffered before playing
      hlsInstance.on(Hls.Events.FRAG_BUFFERED, () => {
        logger.debug('HLS fragment buffered');
        fragmentsBuffered++;

        // Wait for optimal fragment count before starting playback
        // See hls-config.ts BUFFERING_STRATEGY for rationale
        if (
          !playbackAttempted &&
          fragmentsBuffered >= BUFFERING_STRATEGY.MIN_FRAGMENTS_BEFORE_PLAY
        ) {
          playbackAttempted = true;
          logger.debug('Starting playback with buffered fragments:', {
            fragmentsBuffered,
            minRequired: BUFFERING_STRATEGY.MIN_FRAGMENTS_BEFORE_PLAY,
          });
          audio.play().catch((err: Error) => {
            logger.error('Playback start error:', err);
            if (err.name === 'NotAllowedError') {
              showStatusMessage('Click to play (autoplay blocked)');
            } else {
              showStatusMessage('Playback error');
            }
          });
        }
      });

      // Attach to media element and load source
      hlsInstance.attachMedia(audio);
      hlsInstance.loadSource(hlsUrl);
    } else if (audio?.canPlayType('application/vnd.apple.mpegurl')) {
      // Native HLS support (Safari, iOS)
      audio.src = hlsUrl;
      audio.play().catch((err: Error) => {
        // Handle playback start error
        if (err.name === 'NotAllowedError') {
          showStatusMessage('Click to play (autoplay blocked)');
        }
      });
    } else {
      showStatusMessage('HLS playback not supported in this browser');
      // HLS playback not supported
    }
  }

  // Start playback
  async function startPlayback(sourceId: string) {
    // Starting audio playback for source
    stopPlayback();

    playingSource = sourceId;
    showStatusMessage('Starting audio stream...');

    const encodedSourceId = encodeURIComponent(sourceId);

    try {
      // Use fetchWithCSRF for authenticated HLS endpoint
      await fetchWithCSRF(`/api/v2/streams/hls/${encodedSourceId}/start`, {
        method: 'POST',
      });

      const hlsUrl = `/api/v2/streams/hls/${encodedSourceId}/playlist.m3u8`;
      await setupHLSStream(hlsUrl, sourceId);

      startHeartbeat();
    } catch (error) {
      // Handle audio stream access error
      const message =
        error instanceof Error && error.message.includes('permission')
          ? 'Login required to stream audio'
          : 'Error starting stream';
      showStatusMessage(message);
      globalThis.setTimeout(() => hideStatusMessage(), 3000);
    }
  }

  // Stop playback
  function stopPlayback() {
    // Stopping audio playback
    hideStatusMessage();
    stopHeartbeat();

    if (audioElement) {
      audioElement.pause();
      audioElement.src = '';
      audioElement.load();
    }

    if (hlsInstance) {
      hlsInstance.destroy();
      hlsInstance = null;
    }

    const previousSource = playingSource;
    isPlaying = false;
    playingSource = null;

    if ('mediaSession' in navigator) {
      navigator.mediaSession.playbackState = 'paused';
    }

    // Notify server (use fetchWithCSRF for authenticated endpoint)
    if (previousSource) {
      const encodedSourceId = encodeURIComponent(previousSource);
      fetchWithCSRF(`/api/v2/streams/hls/${encodedSourceId}/stop`, {
        method: 'POST',
      }).catch(_err => {
        // Failed to notify server of playback stop
      });
    }
  }

  // Toggle playback for a source
  function toggleSourcePlayback(source: string) {
    if (isPlaying && playingSource === source) {
      stopPlayback();
    } else {
      if (isPlaying) {
        stopPlayback();
      }
      startPlayback(source);
    }
  }

  // Handle click outside
  function handleClickOutside(event: MouseEvent) {
    if (!dropdownRef || !buttonRef) return;

    const target = event.target as Node;
    if (!dropdownRef.contains(target) && !buttonRef.contains(target)) {
      dropdownOpen = false;
    }
  }

  // PERFORMANCE OPTIMIZATION: Use Svelte 5 $effect instead of legacy onMount
  // $effect provides better reactivity and automatic cleanup management
  $effect(() => {
    if (typeof window !== 'undefined') {
      setupEventSource();

      // Add event listeners
      document.addEventListener('click', handleClickOutside);

      // Handle page visibility
      const handleVisibilityChange = () => {
        if (document.hidden) {
          cleanupEventSource();
        } else {
          setupEventSource();
        }
      };

      document.addEventListener('visibilitychange', handleVisibilityChange);

      // Handle page unload
      const handleUnload = () => {
        if (isPlaying && playingSource) {
          stopHeartbeat();
        }
      };

      window.addEventListener('beforeunload', handleUnload);
      window.addEventListener('unload', handleUnload);

      return () => {
        // PERFORMANCE OPTIMIZATION: Automatic cleanup with Svelte 5 $effect
        // Eliminates need for separate onDestroy lifecycle
        document.removeEventListener('click', handleClickOutside);
        document.removeEventListener('visibilitychange', handleVisibilityChange);
        window.removeEventListener('beforeunload', handleUnload);
        window.removeEventListener('unload', handleUnload);

        cleanupEventSource();
        stopHeartbeat();

        if (audioElementRef) {
          audioElementRef.pause();
          audioElementRef.src = '';
          audioElementRef.remove();
          audioElementRef = null;
        }

        if (hlsInstance) {
          hlsInstance.destroy();
          hlsInstance = null;
        }
      };
    }
  });
</script>

<div class={cn('relative w-10 h-10', className)} role="status">
  <!-- Audio level indicator circle -->
  <button
    bind:this={buttonRef}
    onclick={() => (dropdownOpen = !dropdownOpen)}
    class="w-full h-full relative focus:outline-hidden group"
    aria-expanded={dropdownOpen}
    aria-haspopup="true"
    aria-label={`Audio level for ${selectedSource ? getSourceDisplayName(selectedSource) : 'No source'}`}
  >
    <svg class="w-full h-full" viewBox="0 0 36 36" aria-hidden="true">
      <!-- Background circle -->
      <path
        d="M18 2.0845 a 15.9155 15.9155 0 0 1 0 31.831 a 15.9155 15.9155 0 0 1 0 -31.831"
        fill="none"
        stroke="#E5E7EB"
        stroke-width="3"
        stroke-dasharray="100, 100"
      />
      <!-- Foreground circle (audio level) -->
      <path
        d="M18 33.9155 a 15.9155 15.9155 0 0 1 0 -31.831 a 15.9155 15.9155 0 0 1 0 31.831"
        fill="none"
        stroke={isClipping ? '#EF4444' : '#10B981'}
        stroke-width="3"
        stroke-dasharray={`${smoothedVolume}, 100`}
        stroke-linecap="round"
      />
    </svg>
    <div class="absolute inset-0 flex items-center justify-center">
      <Mic class="size-5" />
    </div>
    <!-- Screen reader announcement -->
    <div class="sr-only" aria-live="polite">
      Current audio level: {Math.round(smoothedVolume)} percent{isClipping
        ? ', clipping detected'
        : ''}
    </div>
  </button>

  {#if selectedSource && !dropdownOpen}
    <!-- Tooltip -->
    <div
      class="invisible group-hover:visible absolute left-1/2 transform -translate-x-1/2 -translate-y-full mt-2 px-2 py-1 bg-gray-900 text-gray-50 text-sm rounded-sm whitespace-nowrap z-50"
      style:top="-5px"
      aria-hidden="true"
    >
      {getSourceDisplayName(selectedSource)}
    </div>
  {/if}

  {#if !securityEnabled || accessAllowed}
    <!-- Dropdown menu -->
    {#if dropdownOpen}
      <div
        bind:this={dropdownRef}
        role="menu"
        aria-label="Audio Source Selection"
        class="audio-dropdown absolute top-full mt-2 w-72 sm:w-80 max-w-[calc(100vw-2rem)] bg-base-100 rounded-lg shadow-xl border border-base-300 overflow-hidden flex flex-col z-50"
      >
        <!-- Header -->
        <div class="flex items-center justify-between p-4 border-b border-base-300">
          <h3 class="text-lg font-semibold">Audio Sources</h3>
        </div>

        <!-- Source list -->
        <div class="overflow-y-auto flex-1" role="menu" aria-orientation="vertical">
          {#if Object.keys(levels).length === 0}
            <div class="p-8 text-center text-base-content/60">
              <div class="mx-auto mb-2 opacity-50">
                <Mic class="size-12 mx-auto" />
              </div>
              <p>No audio sources available</p>
            </div>
          {:else}
            {#each Object.entries(levels) as [source, _data] (source)}
              <div
                class={cn(
                  'border-b border-base-300 p-4 hover:bg-base-200 transition-colors',
                  selectedSource === source && 'bg-primary/10 border-l-2 border-l-primary'
                )}
                role="menuitem"
              >
                <div class="flex items-center gap-3">
                  <!-- Status indicator -->
                  <div class="shrink-0">
                    <div
                      class={cn(
                        'w-8 h-8 rounded-full flex items-center justify-center',
                        isInactive(source)
                          ? 'bg-base-300 text-base-content/60'
                          : 'bg-success/20 text-success'
                      )}
                    >
                      <Mic class="size-4" />
                    </div>
                  </div>

                  <!-- Source info (clickable to select) -->
                  <button
                    onclick={() => {
                      selectedSource = source;
                      dropdownOpen = false;
                    }}
                    class="flex-1 text-left min-w-0"
                    role="menuitemradio"
                    aria-checked={selectedSource === source}
                  >
                    <div class="flex items-center gap-2">
                      <span
                        class={cn(
                          'font-medium text-sm truncate',
                          isInactive(source) ? 'text-base-content/60' : 'text-base-content',
                          selectedSource === source && 'text-primary'
                        )}
                      >
                        {getSourceDisplayName(source)}
                      </span>
                      {#if selectedSource === source}
                        <Check class="size-4 text-primary shrink-0" />
                      {/if}
                    </div>
                    {#if isInactive(source)}
                      <span class="text-xs text-base-content/60">(silent)</span>
                    {/if}
                  </button>

                  <!-- Play/Stop controls -->
                  <button
                    onclick={() => {
                      toggleSourcePlayback(source);
                      dropdownOpen = false;
                    }}
                    class={cn(
                      'btn btn-sm btn-circle btn-ghost shrink-0',
                      playingSource === source ? 'text-error' : 'text-success'
                    )}
                    aria-label={playingSource === source
                      ? 'Stop audio playback'
                      : 'Start audio playback'}
                  >
                    {#if playingSource !== source}
                      <CirclePlay class="size-5" />
                    {:else}
                      <CircleStop class="size-5" />
                    {/if}
                  </button>
                </div>
              </div>
            {/each}
          {/if}
        </div>
      </div>
    {/if}
  {/if}

  <!-- Status message -->
  {#if showStatus}
    <div
      class="fixed bottom-4 right-4 bg-primary text-primary-content p-2 rounded-sm shadow-lg z-50"
      role="status"
      aria-live="polite"
    >
      {statusMessage}
    </div>
  {/if}

  <!-- Screen reader announcements -->
  <div aria-live="polite" class="sr-only">
    {#if isPlaying}
      Now playing audio from {selectedSource
        ? getSourceDisplayName(selectedSource)
        : 'unknown source'}
    {:else if !isPlaying && selectedSource}
      Audio playback stopped
    {/if}
  </div>
</div>

<style>
  /* Mobile: fixed positioning centered horizontally to prevent overflow */
  .audio-dropdown {
    position: fixed;
    left: 50%;
    right: auto;
    transform: translateX(-50%);
    top: 4rem; /* Below header */
  }

  /* Desktop (sm+): absolute positioning aligned to button */
  @media (min-width: 640px) {
    .audio-dropdown {
      position: absolute;
      left: auto;
      right: 0;
      transform: none;
      top: 100%;
    }
  }
</style>
