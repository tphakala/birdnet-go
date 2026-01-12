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
  import { Settings, Trash2, Check, X, AlertCircle, Radio } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import StatusPill, { type StatusVariant } from '$lib/desktop/components/ui/StatusPill.svelte';
  import SelectDropdown from './SelectDropdown.svelte';
  import type { StreamConfig, StreamType } from '$lib/stores/settings';

  // Stream health status type
  export type StreamStatus = 'connected' | 'connecting' | 'error' | 'idle' | 'unknown';

  interface Props {
    stream: StreamConfig;
    index: number;
    status?: StreamStatus;
    statusMessage?: string;
    disabled?: boolean;
    onUpdate: (_stream: StreamConfig) => void;
    onDelete: () => void;
  }

  let {
    stream,
    index,
    status = 'unknown',
    statusMessage = '',
    disabled = false,
    onUpdate,
    onDelete,
  }: Props = $props();

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

  // Color palette for stream icons (cycles through)
  const iconColors = [
    { bg: 'bg-cyan-500/20', text: 'text-cyan-500', border: 'border-cyan-500/30' },
    { bg: 'bg-violet-500/20', text: 'text-violet-500', border: 'border-violet-500/30' },
    { bg: 'bg-emerald-500/20', text: 'text-emerald-500', border: 'border-emerald-500/30' },
    { bg: 'bg-amber-500/20', text: 'text-amber-500', border: 'border-amber-500/30' },
    { bg: 'bg-rose-500/20', text: 'text-rose-500', border: 'border-rose-500/30' },
    { bg: 'bg-blue-500/20', text: 'text-blue-500', border: 'border-blue-500/30' },
  ];

  // Hash function for stable color assignment based on stream name
  function hashString(str: string): number {
    let hash = 0;
    for (let i = 0; i < str.length; i++) {
      hash = (hash << 5) - hash + str.charCodeAt(i);
      hash |= 0; // Convert to 32-bit integer
    }
    return Math.abs(hash);
  }

  // Get color for this stream based on name hash (stable across deletions)
  let iconColor = $derived(iconColors[hashString(stream.name) % iconColors.length]);

  // Mask credentials in URL for display
  function maskCredentials(urlStr: string): string {
    try {
      const urlObj = new URL(urlStr);
      if (urlObj.username || urlObj.password) {
        return urlStr.replace(/(rtsps?:\/\/)[^@]+(@)/, '$1***:***$2');
      }
      return urlStr;
    } catch {
      return urlStr.replace(/(rtsps?:\/\/)[^@]+(@)/, '$1***:***$2');
    }
  }

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

  let displayUrl = $derived(maskCredentials(stream.url));
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
      onUpdate({
        name: editName.trim(),
        url: editUrl.trim(),
        type: editStreamType,
        transport: editTransport,
      });
      isEditing = false;
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
            status === 'error' ? 'bg-error/20 text-error border-error/30' : iconColor.bg,
            status === 'error' ? '' : iconColor.text,
            status === 'error' ? '' : iconColor.border
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
        </div>

        <!-- Right Side: Protocol Tags + Actions -->
        <div class="flex-shrink-0 flex items-center gap-2">
          <!-- Colored Protocol Tags -->
          <div class="hidden sm:flex items-center gap-1.5">
            <span class="px-2 py-0.5 rounded text-xs font-semibold bg-info/15 text-info">
              {stream.type.toUpperCase()}
            </span>
            {#if showTransport}
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
          </div>
        </div>
      </div>
    {/if}
  </div>
</div>
