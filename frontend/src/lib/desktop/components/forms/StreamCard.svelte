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

  // Get icon colors based on stream status - using CSS variables for theme compatibility
  function getIconColors(s: StreamStatus): { bg: string; text: string; border: string } {
    switch (s) {
      case 'connected':
        return {
          bg: 'bg-[color-mix(in_srgb,var(--color-success)_20%,transparent)]',
          text: 'text-[var(--color-success)]',
          border: 'border-[color-mix(in_srgb,var(--color-success)_30%,transparent)]',
        };
      case 'connecting':
        return {
          bg: 'bg-[color-mix(in_srgb,var(--color-warning)_20%,transparent)]',
          text: 'text-[var(--color-warning)]',
          border: 'border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]',
        };
      case 'error':
        return {
          bg: 'bg-[color-mix(in_srgb,var(--color-error)_20%,transparent)]',
          text: 'text-[var(--color-error)]',
          border: 'border-[color-mix(in_srgb,var(--color-error)_30%,transparent)]',
        };
      case 'idle':
      default:
        return {
          bg: 'bg-[var(--color-base-content)]/10',
          text: 'text-[var(--color-base-content)]/50',
          border: 'border-[var(--color-base-content)]/20',
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

  // Get card background styles based on status - using CSS variables for theme compatibility
  function getCardStyles(s: StreamStatus): string {
    switch (s) {
      case 'connected':
        return 'border-[color-mix(in_srgb,var(--color-success)_20%,transparent)] bg-[color-mix(in_srgb,var(--color-success)_5%,transparent)]';
      case 'connecting':
        return 'border-[color-mix(in_srgb,var(--color-warning)_20%,transparent)] bg-[color-mix(in_srgb,var(--color-warning)_5%,transparent)]';
      case 'error':
        return 'border-[color-mix(in_srgb,var(--color-error)_30%,transparent)] bg-[color-mix(in_srgb,var(--color-error)_10%,transparent)]';
      case 'idle':
      default:
        return 'border-[var(--border-200)] bg-[var(--color-base-200)]/50';
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
    isEditing
      ? 'border-[var(--color-primary)]/50 bg-[var(--color-base-100)] shadow-md'
      : cardStyles,
    disabled && 'opacity-60 pointer-events-none'
  )}
>
  {#if showDeleteConfirm}
    <!-- Delete Confirmation Overlay -->
    <div
      class="absolute inset-0 z-10 flex items-center rounded-lg bg-[var(--color-base-300)]/95 backdrop-blur-sm px-4"
    >
      <div class="flex items-center gap-3 w-full">
        <AlertCircle class="size-6 text-[var(--color-error)] flex-shrink-0" />
        <p class="text-sm font-medium text-[var(--color-base-content)] flex-1">
          {t('settings.audio.streams.deleteConfirm')}
        </p>
        <div class="flex gap-2 flex-shrink-0">
          <button
            type="button"
            class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 transition-colors"
            onclick={cancelDelete}
          >
            {t('common.cancel')}
          </button>
          <button
            type="button"
            class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-error)] text-[var(--color-error-content)] hover:opacity-90 transition-colors"
            onclick={executeDelete}
          >
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
        <div>
          <label class="block py-1" for="stream-name-{index}">
            <span class="text-xs font-medium text-[var(--color-base-content)]">
              {t('settings.audio.streams.nameLabel')}
            </span>
          </label>
          <input
            id="stream-name-{index}"
            type="text"
            bind:value={editName}
            onkeydown={handleKeydown}
            class="w-full h-9 px-3 text-sm rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] text-[var(--color-base-content)] placeholder:text-[var(--color-base-content)]/40 focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
            placeholder={t('settings.audio.streams.namePlaceholder')}
          />
        </div>

        <!-- URL Input -->
        <div>
          <label class="block py-1" for="stream-url-{index}">
            <span class="text-xs font-medium text-[var(--color-base-content)]">
              {t('settings.audio.streams.urlLabel')}
            </span>
          </label>
          <input
            id="stream-url-{index}"
            type="text"
            bind:value={editUrl}
            onkeydown={handleKeydown}
            class="w-full h-9 px-3 font-mono text-sm rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] text-[var(--color-base-content)] placeholder:text-[var(--color-base-content)]/40 focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
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
        <div class="flex justify-end gap-2 pt-2 border-t border-[var(--border-200)]">
          <button
            type="button"
            class="inline-flex items-center justify-center gap-1.5 h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 transition-colors"
            onclick={cancelEdit}
          >
            <X class="size-4" />
            {t('common.cancel')}
          </button>
          <button
            type="button"
            class="inline-flex items-center justify-center gap-1.5 h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:opacity-90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
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
            <span class="text-sm font-semibold text-[var(--color-base-content)]">
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
          <p
            class="font-mono text-xs text-[var(--color-base-content)] opacity-70 break-all leading-snug mt-0.5"
          >
            {displayUrl}
          </p>
          {#if status === 'error' && statusMessage}
            <p class="text-xs text-[var(--color-error)] mt-1">{statusMessage}</p>
          {/if}

          <!-- Inline Stats Row -->
          <p class="text-xs text-[var(--color-base-content)]/70 mt-1.5 mb-1">
            Total: {totalData} • Rate: {currentRate} • Last: {lastDataAgo}
          </p>
        </div>

        <!-- Right Side: Protocol Tags + Actions -->
        <div class="flex-shrink-0 flex items-center gap-2">
          <!-- Colored Protocol Tags -->
          <div class="hidden sm:flex items-center gap-1.5">
            <span
              class="px-2 py-0.5 rounded text-xs font-semibold bg-[var(--color-info)]/15 text-[var(--color-info)]"
            >
              {stream.type.toUpperCase()}
            </span>
            {#if showTransport && stream.transport}
              <span
                class="px-2 py-0.5 rounded text-xs font-semibold bg-[var(--color-secondary)]/15 text-[var(--color-secondary)]"
              >
                {stream.transport.toUpperCase()}
              </span>
            {/if}
          </div>

          <!-- Action Buttons -->
          <div class="flex items-center gap-0.5">
            <button
              type="button"
              class="inline-flex items-center justify-center size-8 rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={startEdit}
              {disabled}
              aria-label={t('common.edit')}
            >
              <Settings class="size-4" />
            </button>
            <button
              type="button"
              class="inline-flex items-center justify-center size-8 rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={confirmDelete}
              {disabled}
              aria-label={t('common.delete')}
            >
              <Trash2 class="size-4" />
            </button>
            <button
              type="button"
              class="p-1.5 rounded-md hover:bg-[var(--color-base-content)]/10 transition-colors"
              onclick={() => (expanded = !expanded)}
              aria-expanded={expanded}
              aria-controls="diagnostics-{index}"
              aria-label={t('settings.audio.streams.diagnostics.toggleDiagnostics')}
            >
              <ChevronDown
                class={cn(
                  'size-4 text-[var(--color-base-content)]/60 transition-transform duration-200',
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
          class="border-t border-[var(--color-base-content)]/20 px-3 py-3"
          transition:slide={{ duration: 200 }}
        >
          <!-- Stats Grid -->
          <div class="grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
            <div>
              <span class="text-[var(--color-base-content)]/60 font-medium"
                >{t('settings.audio.streams.diagnostics.processState')}:</span
              >
              <span
                class={cn(
                  'ml-2',
                  health?.process_state === 'circuit_open' || health?.process_state === 'stopped'
                    ? 'text-[var(--color-error)]'
                    : 'text-[var(--color-base-content)]'
                )}
              >
                {health?.process_state ?? t('common.unknown')}
              </span>
            </div>
            <div>
              <span class="text-[var(--color-base-content)]/60 font-medium"
                >{t('settings.audio.streams.diagnostics.lastData')}:</span
              >
              <span class="ml-2 text-[var(--color-base-content)]">{lastDataAgo}</span>
            </div>
            <div>
              <span class="text-[var(--color-base-content)]/60 font-medium"
                >{t('settings.audio.streams.diagnostics.restartCount')}:</span
              >
              <span
                class={cn(
                  'ml-2',
                  (health?.restart_count ?? 0) > 0
                    ? 'text-[var(--color-warning)]'
                    : 'text-[var(--color-base-content)]'
                )}
              >
                {health?.restart_count ?? 0}
              </span>
            </div>
            <div>
              <span class="text-[var(--color-base-content)]/60 font-medium"
                >{t('settings.audio.streams.diagnostics.connection')}:</span
              >
              <span
                class={cn(
                  'ml-2',
                  connectionStatus === 'Stable'
                    ? 'text-[var(--color-success)]'
                    : connectionStatus === 'Degraded'
                      ? 'text-[var(--color-warning)]'
                      : connectionStatus === 'Failed'
                        ? 'text-[var(--color-error)]'
                        : 'text-[var(--color-base-content)]'
                )}
              >
                {t(`settings.audio.streams.connectionStatus.${connectionStatus.toLowerCase()}`)}
              </span>
            </div>
          </div>

          <!-- Timeline Section -->
          {#if health?.state_history?.length || health?.error_history?.length}
            <div class="mt-4 pt-4 border-t border-[var(--color-base-content)]/20">
              <p class="text-xs font-medium text-[var(--color-base-content)]/60 mb-3">
                {t('settings.audio.streams.diagnostics.stateErrorHistory')}
              </p>
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
