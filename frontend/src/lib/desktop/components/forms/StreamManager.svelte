<!--
  Stream Manager Component

  Purpose: Manage multiple audio streams with health status monitoring,
  add/edit/delete functionality, and visual status summary.

  Features:
  - Display stream cards with health indicators
  - Add new streams with type and protocol settings
  - Real-time health status via SSE
  - Summary bar showing healthy/unhealthy counts
  - Empty state with guidance

  @component
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { SvelteMap } from 'svelte/reactivity';
  import { Plus, Radio, RefreshCw } from '@lucide/svelte';
  import ReconnectingEventSource from 'reconnecting-eventsource';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { api } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { validateProtocolURL } from '$lib/utils/security';
  import StreamCard, { type StreamStatus } from './StreamCard.svelte';
  import StatusPill from '$lib/desktop/components/ui/StatusPill.svelte';
  import EmptyState from '$lib/desktop/features/settings/components/EmptyState.svelte';
  import SelectDropdown from './SelectDropdown.svelte';
  import type { RTSPStream } from '$lib/stores/settings';

  const logger = loggers.audio;

  // Stream health response types (matching backend API)
  interface StreamHealthResponse {
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
  }

  interface Props {
    streams: RTSPStream[];
    transport: string;
    disabled?: boolean;
    onUpdateStreams: (_streams: RTSPStream[]) => void;
    onUpdateTransport: (_transport: string) => void;
  }

  let {
    streams = [],
    transport = 'tcp',
    disabled = false,
    onUpdateStreams,
    // eslint-disable-next-line no-unused-vars -- Reserved for future per-stream transport support
    onUpdateTransport: _onUpdateTransport,
  }: Props = $props();

  // Stream health state - using SvelteMap for automatic reactivity
  let streamHealth = $state(new SvelteMap<string, StreamHealthResponse>());
  let healthLoading = $state(true);

  // Add new stream state
  let showAddForm = $state(false);
  let newUrl = $state('');
  let newLabel = $state('');
  let newTransport = $state('tcp');
  let newStreamType = $state('rtsp');
  let urlError = $state<string | null>(null);

  // SSE connection for real-time health updates
  let eventSource: ReconnectingEventSource | null = null;

  // Per-stream transport settings (local state until backend supports it)
  // For now, we use the global transport but store per-stream for future
  // Using SvelteMap for automatic reactivity
  let streamTransports = $state(new SvelteMap<string, string>());

  // Stream type options (RTSP only for now)
  const streamTypeOptions = [{ value: 'rtsp', label: 'RTSP' }];

  // Transport protocol options
  const transportOptions = [
    { value: 'tcp', label: 'TCP' },
    { value: 'udp', label: 'UDP' },
  ];

  // Summary stats
  let healthySummary = $derived.by(() => {
    let healthy = 0;
    let unhealthy = 0;
    let unknown = 0;

    streams.forEach(stream => {
      const health = streamHealth.get(stream.url);
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
    const health = streamHealth.get(url);
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

  // Get status message for a stream
  function getStatusMessage(url: string): string {
    const health = streamHealth.get(url);
    if (!health) return '';

    if (health.error) {
      return health.error;
    }
    if (health.is_receiving_data && health.bytes_per_second > 0) {
      const kbps = (health.bytes_per_second / 1024).toFixed(1);
      return `${kbps} KB/s`;
    }
    if (health.restart_count > 0) {
      return t('settings.audio.streams.restartCount', { count: health.restart_count });
    }

    return '';
  }

  // Load initial health status
  async function loadHealthStatus() {
    healthLoading = true;

    try {
      const response = await api.get<StreamHealthResponse[]>('/api/v2/streams/health');
      const newHealthMap = new SvelteMap<string, StreamHealthResponse>();

      if (Array.isArray(response)) {
        response.forEach(health => {
          // Match sanitized URL back to original URL
          const matchingStream = streams.find(
            s => sanitizeUrl(s.url) === health.url || s.url === health.url
          );
          if (matchingStream) {
            newHealthMap.set(matchingStream.url, health);
          }
        });
      }

      streamHealth = newHealthMap;
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

  // Sanitize URL for comparison (mask credentials)
  function sanitizeUrl(urlStr: string): string {
    try {
      const urlObj = new URL(urlStr);
      if (urlObj.username || urlObj.password) {
        urlObj.username = '***';
        urlObj.password = '***';
      }
      return urlObj.toString();
    } catch {
      return urlStr.replace(/(rtsps?:\/\/)[^@]+(@)/, '$1***:***$2');
    }
  }

  // Connect to SSE for real-time updates using ReconnectingEventSource
  function connectSSE() {
    if (eventSource) {
      eventSource.close();
    }

    try {
      // ReconnectingEventSource with configuration for automatic reconnection
      eventSource = new ReconnectingEventSource('/api/v2/streams/health/stream', {
        withCredentials: false,
        max_retry_time: 30000,
      });

      eventSource.addEventListener('stream_health', (event: Event) => {
        try {
          // eslint-disable-next-line no-undef -- MessageEvent is a browser global
          const messageEvent = event as MessageEvent;
          const data = JSON.parse(messageEvent.data) as StreamHealthResponse & {
            event_type: string;
          };
          const matchingStream = streams.find(
            s => sanitizeUrl(s.url) === data.url || s.url === data.url
          );

          if (matchingStream) {
            // SvelteMap automatically triggers reactivity on set()
            streamHealth.set(matchingStream.url, data);
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

  // Validate RTSP URL
  function validateUrl(url: string): boolean {
    return validateProtocolURL(url, ['rtsp', 'rtsps'], 2048);
  }

  // Add new stream
  function addStream() {
    const trimmedUrl = newUrl.trim();

    if (!trimmedUrl) {
      urlError = t('settings.audio.streams.errors.urlRequired');
      return;
    }

    if (!validateUrl(trimmedUrl)) {
      urlError = t('settings.audio.streams.errors.invalidUrl');
      return;
    }

    if (streams.some(s => s.url === trimmedUrl)) {
      urlError = t('settings.audio.streams.errors.duplicate');
      return;
    }

    // Add the new stream with label
    const newStream: RTSPStream = {
      url: trimmedUrl,
      label: newLabel.trim(),
    };
    const updatedStreams = [...streams, newStream];
    onUpdateStreams(updatedStreams);

    // Store per-stream transport (for future use)
    streamTransports.set(trimmedUrl, newTransport);

    // Reset form
    newUrl = '';
    newLabel = '';
    newTransport = transport; // Reset to global transport
    newStreamType = 'rtsp';
    urlError = null;
    showAddForm = false;

    // Refresh health status
    setTimeout(() => loadHealthStatus(), 1000);
  }

  // Update stream - streamType reserved for future use when multiple stream types are supported
  function updateStream(
    index: number,
    url: string,
    label: string,
    streamTransport: string,
    _streamType: string
  ) {
    const updatedStreams = [...streams];
    if (index >= 0 && index < updatedStreams.length) {
      const oldStream = updatedStreams.at(index);

      // Prevent duplicate URLs (skip check if URL unchanged)
      if (
        url !== oldStream?.url &&
        updatedStreams.some((existing, i) => i !== index && existing.url === url)
      ) {
        logger.warn('Attempted to update stream to a duplicate URL', null, {
          component: 'StreamManager',
          action: 'updateStream',
        });
        return;
      }

      updatedStreams.splice(index, 1, { url, label });
      onUpdateStreams(updatedStreams);

      // Update per-stream transport
      if (oldStream) {
        streamTransports.delete(oldStream.url);
      }
      streamTransports.set(url, streamTransport);
    }
  }

  // Delete stream
  function deleteStream(index: number) {
    const streamToDelete = streams.at(index);
    const updatedStreams = streams.filter((_, i) => i !== index);
    onUpdateStreams(updatedStreams);

    // Clean up per-stream data - SvelteMap handles reactivity automatically
    if (streamToDelete) {
      streamTransports.delete(streamToDelete.url);
      streamHealth.delete(streamToDelete.url);
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
      newUrl = '';
      newLabel = '';
      urlError = null;
    }
  }

  // Get transport for a specific stream (falls back to global)
  function getStreamTransport(url: string): string {
    return streamTransports.get(url) || transport;
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
    <div class="flex items-center justify-between p-3 bg-base-200 rounded-lg">
      <div class="flex items-center gap-4">
        <div class="flex items-center gap-2">
          <Radio class="size-4 text-base-content opacity-70" />
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
        class="btn btn-ghost btn-sm gap-1.5"
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
          url={stream.url}
          label={stream.label}
          {index}
          transport={getStreamTransport(stream.url)}
          streamType="rtsp"
          status={getStreamStatus(stream.url)}
          statusMessage={getStatusMessage(stream.url)}
          {disabled}
          onUpdate={(newUrl, newLabel, newTransport, newType) =>
            updateStream(index, newUrl, newLabel, newTransport, newType)}
          onDelete={() => deleteStream(index)}
        />
      {/each}
    </div>

    <!-- Add Stream Section -->
    {#if showAddForm}
      <div class="rounded-lg border-2 border-dashed border-primary/30 bg-primary/5 p-4">
        <div class="space-y-4">
          <!-- Label Input -->
          <div class="form-control">
            <label class="label py-1" for="new-stream-label">
              <span class="label-text text-sm font-medium">
                {t('settings.audio.streams.labelField')}
              </span>
            </label>
            <input
              id="new-stream-label"
              type="text"
              bind:value={newLabel}
              onkeydown={handleAddKeydown}
              class="input input-sm w-full"
              placeholder={t('settings.audio.streams.labelPlaceholder')}
              aria-describedby="new-stream-label-help"
              {disabled}
            />
            <span id="new-stream-label-help" class="text-xs text-base-content opacity-60 mt-1">
              {t('settings.audio.streams.labelHelp')}
            </span>
          </div>

          <!-- URL Input -->
          <div class="form-control">
            <label class="label py-1" for="new-stream-url">
              <span class="label-text text-sm font-medium">
                {t('settings.audio.streams.urlLabel')}
              </span>
            </label>
            <input
              id="new-stream-url"
              type="text"
              bind:value={newUrl}
              onkeydown={handleAddKeydown}
              class={cn('input input-sm w-full font-mono', urlError && 'input-error')}
              placeholder="rtsp://user:password@192.168.1.100:554/stream"
              {disabled}
            />
            {#if urlError}
              <span class="text-xs text-error mt-1">{urlError}</span>
            {:else}
              <span class="text-xs text-base-content opacity-60 mt-1">
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
                helpText={t('settings.audio.streams.typeLockedNote')}
                options={streamTypeOptions}
                disabled={true}
                groupBy={false}
                menuSize="sm"
              />
            </div>

            <div>
              <SelectDropdown
                value={newTransport}
                label={t('settings.audio.streams.transportLabel')}
                options={transportOptions}
                {disabled}
                onChange={value => (newTransport = value as string)}
                groupBy={false}
                menuSize="sm"
              />
            </div>
          </div>

          <!-- Action Buttons -->
          <div class="flex justify-end gap-2 pt-2">
            <button
              type="button"
              class="btn btn-sm btn-ghost"
              onclick={() => {
                showAddForm = false;
                newUrl = '';
                newLabel = '';
                urlError = null;
              }}
            >
              {t('common.cancel')}
            </button>
            <button
              type="button"
              class="btn btn-sm btn-primary gap-1.5"
              onclick={addStream}
              disabled={!newUrl.trim() || disabled}
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
        class="w-full btn btn-outline btn-sm gap-2 border-dashed"
        onclick={() => (showAddForm = true)}
        {disabled}
      >
        <Plus class="size-4" />
        {t('settings.audio.streams.addStream')}
      </button>
    {/if}
  {/if}
</div>
