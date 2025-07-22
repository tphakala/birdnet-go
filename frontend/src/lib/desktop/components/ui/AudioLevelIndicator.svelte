<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { cn } from '$lib/utils/cn';
  import ReconnectingEventSource from 'reconnecting-eventsource';
  import { mediaIcons } from '$lib/utils/icons';

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

  // HLS.js type definitions
  interface HLSConfig {
    debug?: boolean;
    enableWorker?: boolean;
    lowLatencyMode?: boolean;
    backBufferLength?: number;
    liveSyncDurationCount?: number;
    liveMaxLatencyDurationCount?: number;
  }

  interface HLSErrorData {
    type: string;
    details: string;
    fatal: boolean;
    reason?: string;
  }

  interface HLSInstance {
    attachMedia(mediaElement: HTMLMediaElement): void;
    loadSource(source: string): void;
    destroy(): void;
    on(event: string, callback: (event: string, data: any) => void): void;
  }

  interface HLSConstructor {
    new (config?: HLSConfig): HLSInstance;
    isSupported(): boolean;
    Events: {
      ERROR: string;
      MANIFEST_PARSED: string;
    };
  }

  // Type guard for HLS global availability
  function isHLSAvailable(): boolean {
    return typeof window !== 'undefined' && 
           'Hls' in window && 
           typeof (window as any).Hls === 'function' &&
           typeof (window as any).Hls.isSupported === 'function';
  }

  // Get HLS constructor with proper typing
  function getHLSConstructor(): HLSConstructor | null {
    if (!isHLSAvailable()) return null;
    return (window as any).Hls as HLSConstructor;
  }

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
  let hlsInstance: HLSInstance | null = null;
  let zeroLevelTime: { [key: string]: number } = {};
  let heartbeatTimer: ReturnType<typeof globalThis.setInterval> | null = null;
  let dropdownRef = $state<HTMLDivElement>();
  let buttonRef = $state<HTMLButtonElement>();

  // Computed values
  // Removed unused currentLevel

  const isClipping = $derived(
    selectedSource && levels[selectedSource] ? levels[selectedSource].clipping : false
  );

  const smoothedVolume = $derived(selectedSource ? smoothedVolumes[selectedSource] || 0 : 0);

  // Check if source is inactive
  function isInactive(source: string): boolean {
    if (!levels[source]) return true;
    if (levels[source].level > 0) return false;

    if (!zeroLevelTime[source]) {
      zeroLevelTime[source] = Date.now();
      return false;
    }

    return Date.now() - zeroLevelTime[source] > ZERO_LEVEL_TIMEOUT;
  }

  // Get display name for source
  function getSourceDisplayName(source: string): string {
    return levels[source]?.name || source;
  }

  // Setup EventSource for audio levels using ReconnectingEventSource
  function setupEventSource() {
    if (eventSource) return;

    cleanupEventSource();

    try {
      // TODO: Update to v2 API when available
      // ReconnectingEventSource with configuration
      eventSource = new ReconnectingEventSource('/api/v1/audio-level', {
        max_retry_time: 30000, // Max 30 seconds between reconnection attempts
        withCredentials: false, // Set to true if you need CORS credentials
      });

      eventSource.onopen = () => {
        console.log('Audio level SSE connection opened');
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
                if (!zeroLevelTime[source]) {
                  zeroLevelTime[source] = Date.now();
                }
              } else {
                delete zeroLevelTime[source];
              }
            });

            // Initialize smoothed volumes for new sources
            Object.keys(levels).forEach(source => {
              if (!(source in smoothedVolumes)) {
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
              const oldVolume = smoothedVolumes[source] || 0;
              smoothedVolumes[source] =
                SMOOTHING_FACTOR * audioData.level + (1 - SMOOTHING_FACTOR) * oldVolume;
            });
          }
        } catch (error) {
          console.error('Failed to parse audio level data:', error);
        }
      };

      eventSource.onerror = (error: Event) => {
        console.error('Audio level SSE error:', error);
        // ReconnectingEventSource handles reconnection automatically
        // No need for manual reconnection logic
      };
    } catch (error) {
      console.error('Failed to create ReconnectingEventSource:', error);
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

  // Get or create audio element
  function getAudioElement(): HTMLAudioElement {
    if (!audioElement) {
      audioElement = document.createElement('audio');
      audioElement.id = 'hls-audio-player';
      audioElement.setAttribute('aria-hidden', 'true');
      audioElement.classList.add('sr-only');
      audioElement.setAttribute('preload', 'auto');
      audioElement.setAttribute('playsinline', '');

      audioElement.addEventListener('playing', () => {
        isPlaying = true;
        hideStatusMessage();
        setupMediaSession(playingSource || '');
      });

      audioElement.addEventListener('pause', () => {
        isPlaying = false;
      });

      audioElement.addEventListener('ended', () => {
        isPlaying = false;
      });

      audioElement.addEventListener('error', _e => {
        // Handle audio playback error
        showStatusMessage('Playback error');
        isPlaying = false;
        stopPlayback();
      });

      document.body.appendChild(audioElement);
    }

    return audioElement;
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
        // TODO: Update to v2 API when available
        const response = await fetch('/api/v1/audio-stream-hls/heartbeat', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ source_id: playingSource }),
        });

        if (!response.ok) {
          // Heartbeat failed
        }
      } catch {
        // Failed to send heartbeat
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
      // TODO: Update to v2 API when available
      fetch('/api/v1/audio-stream-hls/heartbeat?disconnect=true', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source_id: playingSource }),
      }).catch(() => {
        // Ignore errors during disconnect
      });
    }
  }

  // Setup HLS streaming
  async function setupHLSStream(hlsUrl: string, _sourceId: string) {
    const audio = getAudioElement();

    // Check if HLS.js is available and supported
    const HLS = getHLSConstructor();
    if (HLS && HLS.isSupported()) {
      // Clean up existing instance
      if (hlsInstance) {
        hlsInstance.destroy();
        hlsInstance = null;
      }

      // Create new HLS instance with proper typing
      hlsInstance = new HLS({
        debug: false,
        enableWorker: true,
        lowLatencyMode: false,
        backBufferLength: 10,
        liveSyncDurationCount: 5,
        liveMaxLatencyDurationCount: 30,
      });

      // Setup error handling with proper typing
      hlsInstance.on(HLS.Events.ERROR, (_event: string, data: HLSErrorData) => {
        if (data.fatal) {
          // Handle fatal HLS error
          showStatusMessage('Playback error: ' + data.details);
          stopPlayback();
        }
      });

      // Start playing when manifest is parsed
      hlsInstance.on(HLS.Events.MANIFEST_PARSED, () => {
        audio.play().catch((err: Error) => {
          // Handle playback start error
          if (err.name === 'NotAllowedError') {
            showStatusMessage('Click to play (autoplay blocked)');
          }
        });
      });

      // Attach to media element and load source
      hlsInstance.attachMedia(audio);
      hlsInstance.loadSource(hlsUrl);
    } else if (audio.canPlayType('application/vnd.apple.mpegurl')) {
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
      // TODO: Update to v2 API when available
      const response = await fetch(`/api/v1/audio-stream-hls/${encodedSourceId}/start`, {
        method: 'POST',
      });

      if (!response.ok) {
        throw new Error(`Failed to start stream: ${response.status} ${response.statusText}`);
      }

      const hlsUrl = `/api/v1/audio-stream-hls/${encodedSourceId}/playlist.m3u8`;
      await setupHLSStream(hlsUrl, sourceId);

      startHeartbeat();
    } catch {
      // Handle audio stream access error
      showStatusMessage('Error starting stream');
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

    // Notify server
    if (previousSource) {
      const encodedSourceId = encodeURIComponent(previousSource);
      // TODO: Update to v2 API when available
      fetch(`/api/v1/audio-stream-hls/${encodedSourceId}/stop`, {
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

  onMount(() => {
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
      document.removeEventListener('click', handleClickOutside);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
      window.removeEventListener('beforeunload', handleUnload);
      window.removeEventListener('unload', handleUnload);
    };
  });

  onDestroy(() => {
    cleanupEventSource();
    stopHeartbeat();

    if (audioElement) {
      audioElement.pause();
      audioElement.src = '';
      audioElement.remove();
      audioElement = null;
    }

    if (hlsInstance) {
      hlsInstance.destroy();
      hlsInstance = null;
    }
  });
</script>

<div class={cn('relative w-10 h-10', className)} role="status">
  <!-- Audio level indicator circle -->
  <button
    bind:this={buttonRef}
    onclick={() => (dropdownOpen = !dropdownOpen)}
    class="w-full h-full relative focus:outline-none group"
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
      {@html mediaIcons.microphone}
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
      class="invisible group-hover:visible absolute left-1/2 transform -translate-x-1/2 -translate-y-full mt-2 px-2 py-1 bg-gray-900 text-gray-50 text-sm rounded whitespace-nowrap z-50"
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
        role="dialog"
        aria-modal="true"
        aria-label="Audio Source Selection"
        class="absolute p-1 right-0 mt-2 w-auto min-w-[16rem] max-w-[90vw] overflow-hidden rounded-md shadow-lg bg-base-100 ring-1 ring-black ring-opacity-5 z-50"
      >
        <div class="py-1" role="menu" aria-orientation="vertical">
          {#if Object.keys(levels).length === 0}
            <div class="px-4 py-2 text-sm text-base-content/60" role="menuitem">
              No audio sources available
            </div>
          {:else}
            {#each Object.entries(levels) as [source, _data]}
              <div
                class={cn(
                  'flex flex-row items-center w-full p-2 text-sm hover:bg-base-200 rounded-md',
                  selectedSource === source && 'bg-base-200',
                  isInactive(source) ? 'text-base-content/50' : 'text-base-content'
                )}
                role="menuitem"
              >
                <!-- Source name (clickable to select) -->
                <button
                  onclick={() => {
                    selectedSource = source;
                    dropdownOpen = false;
                  }}
                  class="flex-1 text-left flex items-center justify-between"
                  role="menuitemradio"
                  aria-checked={selectedSource === source}
                >
                  <span class="flex-1 whitespace-nowrap">{getSourceDisplayName(source)}</span>
                  {#if isInactive(source)}
                    <span class="text-xs text-base-content/50 shrink-0 ml-2" aria-hidden="true">
                      (silent)
                    </span>
                  {/if}
                </button>

                <!-- Play/Stop controls -->
                <button
                  onclick={() => {
                    toggleSourcePlayback(source);
                    dropdownOpen = false;
                  }}
                  class={cn(
                    'btn btn-xs btn-circle btn-ghost ml-2',
                    playingSource === source ? 'text-error' : 'text-success'
                  )}
                  aria-label={playingSource === source
                    ? 'Stop audio playback'
                    : 'Start audio playback'}
                >
                  {#if playingSource !== source}
                    <!-- Play icon -->
                    {@html mediaIcons.playCircle}
                  {:else}
                    <!-- Stop icon -->
                    {@html mediaIcons.stopCircle}
                  {/if}
                </button>
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
      class="fixed bottom-4 right-4 bg-primary text-primary-content p-2 rounded shadow-lg z-50"
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
