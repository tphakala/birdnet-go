<!--
  Stream Manager Component

  Purpose: Manage multiple audio streams with health status monitoring,
  add/edit/delete functionality, and visual status summary.

  Features:
  - Display stream cards with health indicators
  - Add new streams with name, type, and protocol settings
  - Real-time health status via SSE
  - Summary bar showing healthy/unhealthy counts
  - Empty state with guidance
  - Auto-detect stream type from URL

  @component
-->
<script lang="ts">
  import { onMount, onDestroy, setContext } from 'svelte';
  import { Plus, Radio, RefreshCw } from '@lucide/svelte';
  import ReconnectingEventSource from 'reconnecting-eventsource';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { api } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { validateProtocolURL, sanitizeUrlForComparison } from '$lib/utils/security';
  import { toastActions } from '$lib/stores/toast';
  import StreamCard, { type StreamStatus } from './StreamCard.svelte';
  import StatusPill from '$lib/desktop/components/ui/StatusPill.svelte';
  import EmptyState from '$lib/desktop/features/settings/components/EmptyState.svelte';
  import SelectDropdown from './SelectDropdown.svelte';
  import type { StreamConfig, StreamType } from '$lib/stores/settings';

  const logger = loggers.audio;

  // Maximum allowed URL length for stream configuration
  const MAX_STREAM_URL_LENGTH = 2048;

  // Error context from FFmpeg error parsing (matching backend ErrorContextResponse)
  export interface ErrorContext {
    error_type: string;
    primary_message: string;
    user_facing_msg: string;
    troubleshooting_steps?: string[];
    timestamp: string;
    // Technical details (optional)
    target_host?: string;
    target_port?: number;
    timeout_duration?: string;
    http_status?: number;
    rtsp_method?: string;
    // Action recommendations
    should_open_circuit: boolean;
    should_restart: boolean;
  }

  // State transition event (matching backend StateTransitionResponse)
  export interface StateTransition {
    from_state: string;
    to_state: string;
    timestamp: string;
    reason?: string;
  }

  // Stream health response types (matching backend API)
  export interface StreamHealthResponse {
    name?: string; // Stream name for unambiguous matching
    type?: string; // Stream type from config
    url: string;
    is_healthy: boolean;
    process_state: string;
    last_data_received: string | null;
    time_since_data_seconds?: number;
    restart_count: number;
    error?: string;
    total_bytes_received: number;
    bytes_per_second: number;
    is_receiving_data: boolean;
    // Error diagnostics
    last_error_context?: ErrorContext | null;
    error_history?: ErrorContext[];
    // State history for debugging
    state_history?: StateTransition[];
  }

  interface Props {
    streams: StreamConfig[];
    disabled?: boolean;
    onUpdateStreams: (_streams: StreamConfig[]) => void;
  }

  let { streams = [], disabled = false, onUpdateStreams }: Props = $props();

  // Stream health state - using $state object passed via context for reactivity
  // In Svelte 5, passing a $state object to context allows children to access the same reactive proxy
  // IMPORTANT: Pass the object directly, not a getter. Never reassign - only mutate properties.
  let streamHealth = $state<Record<string, StreamHealthResponse>>({});

  // Provide the state object via context so children can access it reactively
  // Pass the object directly - children will see mutations to its properties
  setContext('streamHealth', streamHealth);

  let healthLoading = $state(true);

  // Add new stream state
  let showAddForm = $state(false);
  let newName = $state('');
  let newUrl = $state('');
  let newTransport = $state<'tcp' | 'udp'>('tcp');
  let newStreamType = $state<StreamType>('rtsp');
  let nameError = $state<string | null>(null);
  let urlError = $state<string | null>(null);

  // SSE connection for real-time health updates
  let eventSource: ReconnectingEventSource | null = null;

  // Stream type options (all 5 types)
  const streamTypeOptions = [
    { value: 'rtsp', label: 'RTSP' },
    { value: 'http', label: 'HTTP' },
    { value: 'hls', label: 'HLS' },
    { value: 'rtmp', label: 'RTMP' },
    { value: 'udp', label: 'UDP/RTP' },
  ];

  // Transport protocol options
  const transportOptions = [
    { value: 'tcp', label: 'TCP' },
    { value: 'udp', label: 'UDP' },
  ];

  // Show transport dropdown only for RTSP and RTMP
  let showTransportInAdd = $derived(newStreamType === 'rtsp' || newStreamType === 'rtmp');

  // Summary stats
  let healthySummary = $derived.by(() => {
    let healthy = 0;
    let unhealthy = 0;
    let unknown = 0;

    streams.forEach(stream => {
      const health = streamHealth[stream.url];
      if (!health) {
        unknown++;
      } else if (health.is_healthy && health.is_receiving_data) {
        healthy++;
      } else {
        unhealthy++;
      }
    });

    return { healthy, unhealthy, unknown, total: streams.length };
  });

  // Convert backend process state to UI status
  function getStreamStatus(url: string): StreamStatus {
    // eslint-disable-next-line security/detect-object-injection -- URL from validated stream config, not user input
    const health = streamHealth[url];
    if (!health) return 'unknown';

    const state = health.process_state.toLowerCase();

    if (state === 'running' && health.is_healthy && health.is_receiving_data) {
      return 'connected';
    }
    if (state === 'starting' || state === 'connecting') {
      return 'connecting';
    }
    if (!health.is_healthy || health.error) {
      return 'error';
    }
    if (state === 'idle' || state === 'stopped') {
      return 'idle';
    }

    return 'unknown';
  }

  // Find matching stream by name (preferred) or sanitized URL (fallback)
  function findMatchingStream(health: StreamHealthResponse): StreamConfig | undefined {
    // Prefer name-based matching for unambiguous identification
    if (health.name) {
      const byName = streams.find(s => s.name === health.name);
      if (byName) return byName;
    }
    // Fallback to URL-based matching (handles legacy responses or missing names)
    return streams.find(
      s => sanitizeUrlForComparison(s.url) === health.url || s.url === health.url
    );
  }

  // Load initial health status
  async function loadHealthStatus() {
    healthLoading = true;

    try {
      const response = await api.get<StreamHealthResponse[]>('/api/v2/streams/health');

      // Clear existing entries first (mutate, don't reassign)
      for (const key of Object.keys(streamHealth)) {
        // eslint-disable-next-line security/detect-object-injection -- Key from Object.keys on internal state object
        delete streamHealth[key];
      }

      // Add new entries by mutation
      if (Array.isArray(response)) {
        response.forEach(health => {
          const matchingStream = findMatchingStream(health);
          if (matchingStream) {
            streamHealth[matchingStream.url] = health;
          }
        });
      }
    } catch (error) {
      logger.warn('Failed to load stream health', error, {
        component: 'StreamManager',
        action: 'loadHealthStatus',
      });
      // Health loading failure is non-critical, stream cards will show "unknown" status
    } finally {
      healthLoading = false;
    }
  }

  // Connect to SSE for real-time updates using ReconnectingEventSource
  function connectSSE() {
    if (eventSource) {
      eventSource.close();
    }

    try {
      // ReconnectingEventSource with configuration for automatic reconnection
      eventSource = new ReconnectingEventSource(buildAppUrl('/api/v2/streams/health/stream'), {
        withCredentials: false,
        max_retry_time: 30000,
      });

      eventSource.addEventListener('stream_health', (event: Event) => {
        try {
          // Cast to access data property from SSE event
          const eventData = (event as unknown as { data: string }).data;
          const data = JSON.parse(eventData) as StreamHealthResponse & {
            event_type: string;
          };

          const matchingStream = findMatchingStream(data);

          if (matchingStream) {
            // Update the state - mutate the object to trigger reactivity
            streamHealth[matchingStream.url] = data;
          }
        } catch (e) {
          logger.warn('Failed to parse stream health event', e, {
            component: 'StreamManager',
          });
        }
      });

      eventSource.onerror = () => {
        logger.warn('Stream health SSE connection error', null, {
          component: 'StreamManager',
        });
        // ReconnectingEventSource handles reconnection automatically
      };
    } catch (error) {
      logger.error('Failed to create ReconnectingEventSource:', error);
    }
  }

  // Auto-detect stream type from URL
  function detectStreamType(url: string): StreamType {
    const urlLower = url.toLowerCase();
    if (urlLower.startsWith('rtsp://') || urlLower.startsWith('rtsps://')) return 'rtsp';
    if (urlLower.startsWith('rtmp://') || urlLower.startsWith('rtmps://')) return 'rtmp';
    if (urlLower.startsWith('udp://') || urlLower.startsWith('rtp://')) return 'udp';
    if (urlLower.includes('.m3u8')) return 'hls';
    if (urlLower.startsWith('http://') || urlLower.startsWith('https://')) return 'http';
    return 'rtsp'; // Default
  }

  // Handle URL input with auto-detection
  function handleUrlInput(event: Event) {
    const target = event.target as HTMLInputElement;
    newUrl = target.value;
    if (newUrl.includes('://')) {
      newStreamType = detectStreamType(newUrl);
    }
  }

  // Validate URL based on stream type
  function validateUrl(url: string, streamType: StreamType): boolean {
    // Define allowed protocols for each stream type
    const getProtocols = (type: StreamType): string[] => {
      switch (type) {
        case 'rtsp':
          return ['rtsp', 'rtsps'];
        case 'rtmp':
          return ['rtmp', 'rtmps'];
        case 'http':
        case 'hls':
          return ['http', 'https'];
        case 'udp':
          return ['udp', 'rtp'];
        default:
          return ['rtsp', 'rtsps'];
      }
    };
    return validateProtocolURL(url, getProtocols(streamType), MAX_STREAM_URL_LENGTH);
  }

  // Clear form errors
  function clearErrors() {
    nameError = null;
    urlError = null;
  }

  // Add new stream
  function addStream() {
    clearErrors();

    const trimmedName = newName.trim();
    const trimmedUrl = newUrl.trim();

    // Validate name
    if (!trimmedName) {
      nameError = t('settings.audio.streams.errors.nameRequired');
      return;
    }

    // Check for duplicate name (case-insensitive)
    const nameLower = trimmedName.toLowerCase();
    if (streams.some(s => s.name.toLowerCase() === nameLower)) {
      nameError = t('settings.audio.streams.errors.duplicateName');
      return;
    }

    // Validate URL
    if (!trimmedUrl) {
      urlError = t('settings.audio.streams.errors.urlRequired');
      return;
    }

    if (!validateUrl(trimmedUrl, newStreamType)) {
      urlError = t('settings.audio.streams.errors.invalidUrl');
      return;
    }

    // Check for duplicate URL
    if (streams.some(s => s.url === trimmedUrl)) {
      urlError = t('settings.audio.streams.errors.duplicate');
      return;
    }

    // Create new stream config - only include transport for RTSP/RTMP types
    const newStream: StreamConfig = {
      name: trimmedName,
      url: trimmedUrl,
      type: newStreamType,
      ...(showTransportInAdd ? { transport: newTransport } : {}),
    } as StreamConfig;

    // Add the new stream
    const updatedStreams = [...streams, newStream];
    onUpdateStreams(updatedStreams);

    // Reset form
    newName = '';
    newUrl = '';
    newTransport = 'tcp';
    newStreamType = 'rtsp';
    clearErrors();
    showAddForm = false;

    // Refresh health status
    setTimeout(() => loadHealthStatus(), 1000);
  }

  // Update stream - returns boolean indicating success
  function updateStream(index: number, updatedStream: StreamConfig): boolean {
    const updatedStreams = [...streams];
    if (index >= 0 && index < updatedStreams.length) {
      const oldStream = updatedStreams.at(index);

      // Check for duplicate name (excluding current stream, case-insensitive)
      const nameLower = updatedStream.name.toLowerCase();
      if (updatedStreams.some((s, i) => i !== index && s.name.toLowerCase() === nameLower)) {
        logger.warn('Attempted to update stream to a duplicate name', null, {
          component: 'StreamManager',
          action: 'updateStream',
        });
        toastActions.error(t('settings.audio.streams.errors.duplicateName'));
        return false;
      }

      // Check for duplicate URL (excluding current stream)
      if (
        updatedStream.url !== oldStream?.url &&
        updatedStreams.some((s, i) => i !== index && s.url === updatedStream.url)
      ) {
        logger.warn('Attempted to update stream to a duplicate URL', null, {
          component: 'StreamManager',
          action: 'updateStream',
        });
        toastActions.error(t('settings.audio.streams.errors.duplicate'));
        return false;
      }

      // Update health state if URL changed
      if (oldStream && oldStream.url !== updatedStream.url) {
        delete streamHealth[oldStream.url];
      }

      updatedStreams.splice(index, 1, updatedStream);
      onUpdateStreams(updatedStreams);
      return true;
    }
    return false;
  }

  // Delete stream
  function deleteStream(index: number) {
    const streamToDelete = streams.at(index);
    const updatedStreams = streams.filter((_, i) => i !== index);
    onUpdateStreams(updatedStreams);

    // Clean up health data from state
    if (streamToDelete) {
      delete streamHealth[streamToDelete.url];
    }
  }

  // Handle keydown in add form
  function handleAddKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      addStream();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      showAddForm = false;
      newName = '';
      newUrl = '';
      clearErrors();
    }
  }

  onMount(() => {
    loadHealthStatus();
    if (streams.length > 0) {
      connectSSE();
    }
  });

  onDestroy(() => {
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
  });

  // Reconnect SSE when streams change
  $effect(() => {
    if (streams.length > 0 && !eventSource) {
      connectSSE();
    } else if (streams.length === 0 && eventSource) {
      eventSource.close();
      eventSource = null;
    }
  });
</script>

<div class="space-y-4">
  <!-- Status Summary Bar -->
  {#if streams.length > 0}
    <div class="flex items-center justify-between p-3 bg-[var(--color-base-200)] rounded-lg">
      <div class="flex items-center gap-4">
        <div class="flex items-center gap-2">
          <Radio class="size-4 text-[var(--color-base-content)]/70" />
          <span class="text-sm font-medium">
            {t('settings.audio.streams.summary', { count: streams.length })}
          </span>
        </div>

        {#if !healthLoading}
          <div class="flex items-center gap-2">
            {#if healthySummary.healthy > 0}
              <StatusPill
                variant="success"
                label="{healthySummary.healthy} {t('settings.audio.streams.healthy')}"
                size="sm"
              />
            {/if}
            {#if healthySummary.unhealthy > 0}
              <StatusPill
                variant="error"
                label="{healthySummary.unhealthy} {t('settings.audio.streams.unhealthy')}"
                size="sm"
              />
            {/if}
            {#if healthySummary.unknown > 0}
              <StatusPill
                variant="neutral"
                label="{healthySummary.unknown} {t('settings.audio.streams.unknown')}"
                size="sm"
              />
            {/if}
          </div>
        {/if}
      </div>

      <button
        type="button"
        class="inline-flex items-center justify-center gap-1.5 h-8 px-3 text-sm rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 text-[var(--color-base-content)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={loadHealthStatus}
        disabled={healthLoading || disabled}
      >
        <RefreshCw class={cn('size-4', healthLoading && 'animate-spin')} />
        {t('settings.audio.streams.refresh')}
      </button>
    </div>
  {/if}

  <!-- Stream Cards -->
  {#if streams.length === 0 && !showAddForm}
    <EmptyState
      icon={Radio}
      title={t('settings.audio.streams.emptyState.title')}
      description={t('settings.audio.streams.emptyState.description')}
      hints={[
        t('settings.audio.streams.emptyState.hints.rtsp'),
        t('settings.audio.streams.emptyState.hints.credentials'),
        t('settings.audio.streams.emptyState.hints.protocol'),
      ]}
      hintsTitle={t('settings.audio.streams.emptyState.hintsTitle')}
      primaryAction={{
        label: t('settings.audio.streams.addStream'),
        icon: Plus,
        onclick: () => (showAddForm = true),
      }}
    />
  {:else}
    <div class="space-y-3">
      {#each streams as stream, index (stream.url)}
        <StreamCard
          {stream}
          {index}
          status={getStreamStatus(stream.url)}
          {disabled}
          onUpdate={updatedStream => updateStream(index, updatedStream)}
          onDelete={() => deleteStream(index)}
        />
      {/each}
    </div>

    <!-- Add Stream Section -->
    {#if showAddForm}
      <div
        class="rounded-lg border-2 border-dashed border-[var(--color-primary)]/30 bg-[color-mix(in_srgb,var(--color-primary)_5%,transparent)] p-4"
      >
        <div class="space-y-4">
          <!-- Name Input -->
          <div>
            <label class="block py-1" for="new-stream-name">
              <span class="text-sm font-medium text-[var(--color-base-content)]">
                {t('settings.audio.streams.nameLabel')}
              </span>
            </label>
            <input
              id="new-stream-name"
              type="text"
              bind:value={newName}
              onkeydown={handleAddKeydown}
              class={cn(
                'w-full h-9 px-3 text-sm rounded-lg border bg-[var(--color-base-100)] text-[var(--color-base-content)] placeholder:text-[var(--color-base-content)]/40 focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors',
                nameError ? 'border-[var(--color-error)]' : 'border-[var(--border-200)]'
              )}
              placeholder={t('settings.audio.streams.namePlaceholder')}
              {disabled}
            />
            {#if nameError}
              <span class="text-xs text-[var(--color-error)] mt-1">{nameError}</span>
            {:else}
              <span class="text-xs text-[var(--color-base-content)]/60 mt-1">
                {t('settings.audio.streams.nameHelp')}
              </span>
            {/if}
          </div>

          <!-- URL Input -->
          <div>
            <label class="block py-1" for="new-stream-url">
              <span class="text-sm font-medium text-[var(--color-base-content)]">
                {t('settings.audio.streams.urlLabel')}
              </span>
            </label>
            <input
              id="new-stream-url"
              type="text"
              value={newUrl}
              oninput={handleUrlInput}
              onkeydown={handleAddKeydown}
              class={cn(
                'w-full h-9 px-3 text-sm font-mono rounded-lg border bg-[var(--color-base-100)] text-[var(--color-base-content)] placeholder:text-[var(--color-base-content)]/40 focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors',
                urlError ? 'border-[var(--color-error)]' : 'border-[var(--border-200)]'
              )}
              placeholder="rtsp://user:password@192.168.1.100:554/stream"
              {disabled}
            />
            {#if urlError}
              <span class="text-xs text-[var(--color-error)] mt-1">{urlError}</span>
            {:else}
              <span class="text-xs text-[var(--color-base-content)]/60 mt-1">
                {t('settings.audio.streams.urlHelp')}
              </span>
            {/if}
          </div>

          <!-- Type and Transport Row -->
          <div class="grid grid-cols-2 gap-4">
            <div>
              <SelectDropdown
                value={newStreamType}
                label={t('settings.audio.streams.typeLabel')}
                options={streamTypeOptions}
                {disabled}
                onChange={value => (newStreamType = value as StreamType)}
                groupBy={false}
                menuSize="sm"
              />
            </div>

            {#if showTransportInAdd}
              <div>
                <SelectDropdown
                  value={newTransport}
                  label={t('settings.audio.streams.transportLabel')}
                  options={transportOptions}
                  {disabled}
                  onChange={value => (newTransport = value as 'tcp' | 'udp')}
                  groupBy={false}
                  menuSize="sm"
                />
              </div>
            {/if}
          </div>

          <!-- Action Buttons -->
          <div class="flex justify-end gap-2 pt-2">
            <button
              type="button"
              class="inline-flex items-center justify-center h-8 px-3 text-sm rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 text-[var(--color-base-content)] transition-colors"
              onclick={() => {
                showAddForm = false;
                newName = '';
                newUrl = '';
                clearErrors();
              }}
            >
              {t('common.cancel')}
            </button>
            <button
              type="button"
              class="inline-flex items-center justify-center gap-1.5 h-8 px-3 text-sm rounded-lg bg-[var(--color-primary)] hover:bg-[var(--color-primary)]/80 text-[var(--color-primary-content)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={addStream}
              disabled={!newName.trim() || !newUrl.trim() || disabled}
            >
              <Plus class="size-4" />
              {t('settings.audio.streams.addStream')}
            </button>
          </div>
        </div>
      </div>
    {:else}
      <!-- Add Stream Button -->
      <button
        type="button"
        class="w-full inline-flex items-center justify-center gap-2 h-8 px-3 text-sm rounded-lg border border-dashed border-[var(--border-200)] bg-transparent hover:bg-[var(--color-base-content)]/5 text-[var(--color-base-content)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={() => (showAddForm = true)}
        {disabled}
      >
        <Plus class="size-4" />
        {t('settings.audio.streams.addStream')}
      </button>
    {/if}
  {/if}
</div>
