<!--
  Stream Card Component

  Purpose: Display and manage a single audio stream with health status indicator,
  URL editing, stream type, and protocol settings.

  Features:
  - Card background styling based on connection state
  - Colored stream icon (auto-assigned based on index)
  - Status pill with semantic coloring
  - Editable stream name and URL with credential masking
  - Stream type selector (RTSP, HTTP, HLS, RTMP, UDP)
  - Protocol selector (TCP/UDP) for RTSP and RTMP streams
  - Inline editing mode
  - Delete confirmation
  - Always-visible action buttons for accessibility

  @component
-->
<script lang="ts">
  import { getContext } from 'svelte';
  import { Settings, Trash2, Check, X, AlertCircle, Radio, ChevronDown } from '@lucide/svelte';
  import { slide } from 'svelte/transition';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { maskUrlCredentials } from '$lib/utils/security';
  import StatusPill, { type StatusVariant } from '$lib/desktop/components/ui/StatusPill.svelte';
  import SelectDropdown from './SelectDropdown.svelte';
  import type { StreamConfig, StreamType } from '$lib/stores/settings';
  import type { StreamHealthResponse } from './StreamManager.svelte';
  import StreamTimeline from './StreamTimeline.svelte';

  // Stream health status type
  export type StreamStatus = 'connected' | 'connecting' | 'error' | 'idle' | 'unknown';

  interface Props {
    stream: StreamConfig;
    index: number;
    status?: StreamStatus;
    disabled?: boolean;
    onUpdate: (_stream: StreamConfig) => boolean;
    onDelete: () => void;
  }

  let { stream, index, status = 'unknown', disabled = false, onUpdate, onDelete }: Props = $props();

  // Get the stream health state from context - the $state object is passed directly
  // Mutations to this object are reactive and will trigger re-renders
  const streamHealthMap = getContext<Record<string, StreamHealthResponse>>('streamHealth');

  // Derive health for this stream - accessing properties on the $state object is reactive
  let health = $derived(streamHealthMap[stream.url] ?? null);

  // Expandable diagnostics state
  let expanded = $state(false);

  // Utility functions for formatting
  function formatBytes(bytes: number): string {
    if (!bytes || bytes === 0) return '--';
    const units = ['B', 'KB', 'MB', 'GB'];
    // Clamp index to valid array bounds (0 to 3) to prevent out-of-bounds access
    const rawIndex = Math.floor(Math.log(bytes) / Math.log(1024));
    const i = Math.min(Math.max(0, rawIndex), units.length - 1);
    // eslint-disable-next-line security/detect-object-injection -- Index clamped to valid range [0, units.length-1]
    return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
  }

  function formatRate(bytesPerSecond: number): string {
    if (!bytesPerSecond || bytesPerSecond === 0) return '--';
    const kbps = bytesPerSecond / 1024;
    return `${kbps.toFixed(1)} KB/s`;
  }

  function formatTimeAgo(seconds: number | undefined): string {
    if (seconds === undefined || seconds === null) return '--';
    if (seconds < 60) return `${Math.floor(seconds)}s ago`;
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
    return `${Math.floor(seconds / 3600)}h ago`;
  }

  // Derive status message from health object
  let statusMessage = $derived.by(() => {
    if (!health) return '';
    if (health.error) return health.error;
    return '';
  });

  // Derive inline stats from health - use $derived.by to ensure proper tracking
  let totalData = $derived.by(() => {
    const bytes = health?.total_bytes_received ?? 0;
    return formatBytes(bytes);
  });

  let currentRate = $derived.by(() => {
    const bps = health?.bytes_per_second ?? 0;
    return formatRate(bps);
  });

  let lastDataAgo = $derived.by(() => {
    const secs = health?.time_since_data_seconds;
    return formatTimeAgo(secs);
  });

  // Derive connection stability status
  let connectionStatus = $derived.by(() => {
    if (!health) return 'Unknown';
    if (health.process_state === 'circuit_open') return 'Failed';
    if (health.process_state === 'backoff' || health.process_state === 'restarting')
      return 'Degraded';
    if (health.is_healthy && health.is_receiving_data) return 'Stable';
    return 'Unknown';
  });

  // Local editing state - initialized with defaults, synced from props in startEdit()
  let isEditing = $state(false);
  let editName = $state('');
  let editUrl = $state('');
  let editTransport = $state<'tcp' | 'udp'>('tcp');
  let editStreamType = $state<StreamType>('rtsp');
  let showDeleteConfirm = $state(false);

  // Stream type options (all supported types)
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

  // Get icon colors based on stream status
  function getIconColors(s: StreamStatus): { bg: string; text: string; border: string } {
    switch (s) {
      case 'connected':
        return { bg: 'bg-success/20', text: 'text-success', border: 'border-success/30' };
      case 'connecting':
        return { bg: 'bg-warning/20', text: 'text-warning', border: 'border-warning/30' };
      case 'error':
        return { bg: 'bg-error/20', text: 'text-error', border: 'border-error/30' };
      case 'idle':
        return {
          bg: 'bg-base-content/10',
          text: 'text-base-content/50',
          border: 'border-base-content/20',
        };
      default:
        return {
          bg: 'bg-base-content/10',
          text: 'text-base-content/50',
          border: 'border-base-content/20',
        };
    }
  }

  // Derive icon color based on current status
  let iconColor = $derived(getIconColors(status));

  // Map stream status to StatusPill variant
  function getStatusVariant(s: StreamStatus): StatusVariant {
    switch (s) {
      case 'connected':
        return 'success';
      case 'connecting':
        return 'warning';
      case 'error':
        return 'error';
      case 'idle':
        return 'neutral';
      default:
        return 'neutral';
    }
  }

  // Get status text
  function getStatusText(s: StreamStatus): string {
    switch (s) {
      case 'connected':
        return t('settings.audio.streams.status.connected');
      case 'connecting':
        return t('settings.audio.streams.status.connecting');
      case 'error':
        return t('settings.audio.streams.status.error');
      case 'idle':
        return t('settings.audio.streams.status.idle');
      default:
        return t('settings.audio.streams.status.unknown');
    }
  }

  // Get card background styles based on status
  function getCardStyles(s: StreamStatus): string {
    switch (s) {
      case 'connected':
        return 'border-success/20 bg-success/5';
      case 'connecting':
        return 'border-warning/20 bg-warning/5';
      case 'error':
        return 'border-error/30 bg-error/10';
      case 'idle':
        return 'border-base-300 bg-base-200/50';
      default:
        return 'border-base-300 bg-base-200/50';
    }
  }

  let displayUrl = $derived(maskUrlCredentials(stream.url));
  let cardStyles = $derived(getCardStyles(status));

  // Show transport only for RTSP and RTMP
  let showTransport = $derived(stream.type === 'rtsp' || stream.type === 'rtmp');
  let showTransportInEdit = $derived(editStreamType === 'rtsp' || editStreamType === 'rtmp');

  function startEdit() {
    editName = stream.name;
    editUrl = stream.url;
    editTransport = stream.transport ?? 'tcp';
    editStreamType = stream.type;
    isEditing = true;
  }

  function cancelEdit() {
    isEditing = false;
    showDeleteConfirm = false;
  }

  function saveEdit() {
    if (editName.trim() && editUrl.trim()) {
      const success = onUpdate({
        name: editName.trim(),
        url: editUrl.trim(),
        type: editStreamType,
        // Use selected transport for RTSP/RTMP, omit for others
        ...(showTransportInEdit ? { transport: editTransport } : {}),
      } as StreamConfig);
      if (success) {
        isEditing = false;
      }
    }
  }

  function confirmDelete() {
    showDeleteConfirm = true;
  }

  function executeDelete() {
    onDelete();
    showDeleteConfirm = false;
  }

  function cancelDelete() {
    showDeleteConfirm = false;
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      saveEdit();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      cancelEdit();
    }
  }
</script>

<div
  class={cn(
    'relative rounded-lg border transition-all duration-200',
    isEditing ? 'border-primary/50 bg-base-100 shadow-md' : cardStyles,
    disabled && 'opacity-60 pointer-events-none'
  )}
>
  {#if showDeleteConfirm}
    <!-- Delete Confirmation Overlay -->
    <div
      class="absolute inset-0 z-10 flex items-center justify-center rounded-lg bg-base-300/95 backdrop-blur-sm"
    >
      <div class="text-center px-4">
        <AlertCircle class="size-8 text-error mx-auto mb-2" />
        <p class="text-sm font-medium mb-3">{t('settings.audio.streams.deleteConfirm')}</p>
        <div class="flex gap-2 justify-center">
          <button type="button" class="btn btn-sm btn-ghost" onclick={cancelDelete}>
            {t('common.cancel')}
          </button>
          <button type="button" class="btn btn-sm btn-error" onclick={executeDelete}>
            {t('common.delete')}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <div class="p-3">
    {#if isEditing}
      <!-- Edit Mode -->
      <div class="space-y-4">
        <!-- Name Input -->
        <div class="form-control">
          <label class="label py-1" for="stream-name-{index}">
            <span class="label-text text-xs font-medium">
              {t('settings.audio.streams.nameLabel')}
            </span>
          </label>
          <input
            id="stream-name-{index}"
            type="text"
            bind:value={editName}
            onkeydown={handleKeydown}
            class="input input-sm w-full text-sm"
            placeholder={t('settings.audio.streams.namePlaceholder')}
          />
        </div>

        <!-- URL Input -->
        <div class="form-control">
          <label class="label py-1" for="stream-url-{index}">
            <span class="label-text text-xs font-medium">
              {t('settings.audio.streams.urlLabel')}
            </span>
          </label>
          <input
            id="stream-url-{index}"
            type="text"
            bind:value={editUrl}
            onkeydown={handleKeydown}
            class="input input-sm w-full font-mono text-sm"
            placeholder="rtsp://user:password@host:port/path"
          />
        </div>

        <!-- Type and Transport Row -->
        <div class="grid grid-cols-2 gap-4">
          <div>
            <SelectDropdown
              value={editStreamType}
              label={t('settings.audio.streams.typeLabel')}
              options={streamTypeOptions}
              onChange={value => (editStreamType = value as StreamType)}
              groupBy={false}
              menuSize="sm"
            />
          </div>

          {#if showTransportInEdit}
            <div>
              <SelectDropdown
                value={editTransport}
                label={t('settings.audio.streams.transportLabel')}
                options={transportOptions}
                onChange={value => (editTransport = value as 'tcp' | 'udp')}
                groupBy={false}
                menuSize="sm"
              />
            </div>
          {/if}
        </div>

        <!-- Action Buttons -->
        <div class="flex justify-end gap-2 pt-2 border-t border-base-300">
          <button type="button" class="btn btn-sm btn-ghost gap-1.5" onclick={cancelEdit}>
            <X class="size-4" />
            {t('common.cancel')}
          </button>
          <button
            type="button"
            class="btn btn-sm btn-primary gap-1.5"
            onclick={saveEdit}
            disabled={!editName.trim() || !editUrl.trim()}
          >
            <Check class="size-4" />
            {t('common.save')}
          </button>
        </div>
      </div>
    {:else}
      <!-- View Mode - Compact Layout -->
      <div class="flex items-center gap-3">
        <!-- Stream Icon -->
        <div
          class={cn(
            'flex-shrink-0 size-10 rounded-lg flex items-center justify-center border',
            iconColor.bg,
            iconColor.text,
            iconColor.border
          )}
        >
          <Radio class="size-5" />
        </div>

        <!-- Stream Info -->
        <div class="flex-1 min-w-0">
          <!-- Stream Name, Status, and URL on same line where possible -->
          <div class="flex items-center gap-2 flex-wrap">
            <span class="text-sm font-semibold text-base-content">
              {stream.name}
            </span>
            <StatusPill
              variant={getStatusVariant(status)}
              label={getStatusText(status)}
              size="sm"
              pulse={status === 'connecting'}
            />
          </div>

          <!-- URL and Error Message -->
          <p class="font-mono text-xs text-base-content opacity-70 break-all leading-snug mt-0.5">
            {displayUrl}
          </p>
          {#if status === 'error' && statusMessage}
            <p class="text-xs text-error mt-1">{statusMessage}</p>
          {/if}

          <!-- Inline Stats Row -->
          <p class="text-xs text-base-content/70 mt-1.5 mb-1">
            Total: {totalData} • Rate: {currentRate} • Last: {lastDataAgo}
          </p>
        </div>

        <!-- Right Side: Protocol Tags + Actions -->
        <div class="flex-shrink-0 flex items-center gap-2">
          <!-- Colored Protocol Tags -->
          <div class="hidden sm:flex items-center gap-1.5">
            <span class="px-2 py-0.5 rounded text-xs font-semibold bg-info/15 text-info">
              {stream.type.toUpperCase()}
            </span>
            {#if showTransport && stream.transport}
              <span
                class="px-2 py-0.5 rounded text-xs font-semibold bg-secondary/15 text-secondary"
              >
                {stream.transport.toUpperCase()}
              </span>
            {/if}
          </div>

          <!-- Action Buttons -->
          <div class="flex items-center gap-0.5">
            <button
              type="button"
              class="btn btn-sm btn-ghost btn-square"
              onclick={startEdit}
              {disabled}
              aria-label={t('common.edit')}
            >
              <Settings class="size-4" />
            </button>
            <button
              type="button"
              class="btn btn-sm btn-ghost btn-square"
              onclick={confirmDelete}
              {disabled}
              aria-label={t('common.delete')}
            >
              <Trash2 class="size-4" />
            </button>
            <button
              type="button"
              class="p-1.5 rounded-md hover:bg-base-content/10 transition-colors"
              onclick={() => (expanded = !expanded)}
              aria-expanded={expanded}
              aria-controls="diagnostics-{index}"
              aria-label="Toggle diagnostics"
            >
              <ChevronDown
                class={cn(
                  'size-4 text-base-content/60 transition-transform duration-200',
                  expanded && 'rotate-180'
                )}
              />
            </button>
          </div>
        </div>
      </div>

      <!-- Expandable Diagnostics Panel -->
      {#if expanded}
        <div
          id="diagnostics-{index}"
          class="border-t border-base-content/20 px-3 py-3"
          transition:slide={{ duration: 200 }}
        >
          <!-- Stats Grid -->
          <div class="grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
            <div>
              <span class="text-base-content/60 font-medium">Process State:</span>
              <span
                class={cn(
                  'ml-2',
                  health?.process_state === 'circuit_open' || health?.process_state === 'stopped'
                    ? 'text-error'
                    : 'text-base-content'
                )}
              >
                {health?.process_state ?? 'Unknown'}
              </span>
            </div>
            <div>
              <span class="text-base-content/60 font-medium">Last Data:</span>
              <span class="ml-2 text-base-content">{lastDataAgo}</span>
            </div>
            <div>
              <span class="text-base-content/60 font-medium">Restart Count:</span>
              <span
                class={cn(
                  'ml-2',
                  (health?.restart_count ?? 0) > 0 ? 'text-warning' : 'text-base-content'
                )}
              >
                {health?.restart_count ?? 0}
              </span>
            </div>
            <div>
              <span class="text-base-content/60 font-medium">Connection:</span>
              <span
                class={cn(
                  'ml-2',
                  connectionStatus === 'Stable'
                    ? 'text-success'
                    : connectionStatus === 'Degraded'
                      ? 'text-warning'
                      : connectionStatus === 'Failed'
                        ? 'text-error'
                        : 'text-base-content'
                )}
              >
                {connectionStatus}
              </span>
            </div>
          </div>

          <!-- Timeline Section -->
          {#if health?.state_history?.length || health?.error_history?.length}
            <div class="mt-4 pt-4 border-t border-base-content/20">
              <p class="text-xs font-medium text-base-content/60 mb-3">State & Error History</p>
              <StreamTimeline
                stateHistory={health?.state_history}
                errorHistory={health?.error_history}
              />
            </div>
          {/if}
        </div>
      {/if}
    {/if}
  </div>
</div>
