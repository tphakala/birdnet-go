<!--
  Stream Card Component

  Purpose: Display and manage a single audio stream with health status indicator,
  URL editing, stream type, and protocol settings.

  Features:
  - Card background styling based on connection state
  - Colored stream icon (auto-assigned based on index)
  - Status pill with semantic coloring
  - Editable stream URL with credential masking
  - Stream type selector (RTSP only for now, prepared for HTTP, HLS, RTMP)
  - Protocol selector (TCP/UDP) per stream
  - Inline editing mode
  - Delete confirmation
  - Always-visible action buttons for accessibility

  @component
-->
<script lang="ts">
  import { Pencil, Trash2, Check, X, AlertCircle, Radio } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import StatusPill, { type StatusVariant } from '$lib/desktop/components/ui/StatusPill.svelte';

  // Stream health status type
  export type StreamStatus = 'connected' | 'connecting' | 'error' | 'idle' | 'unknown';

  interface Props {
    url: string;
    index: number;
    transport: string;
    streamType?: string;
    status?: StreamStatus;
    statusMessage?: string;
    disabled?: boolean;
    onUpdate: (_url: string, _transport: string, _streamType: string) => void;
    onDelete: () => void;
  }

  let {
    url,
    index,
    transport = 'tcp',
    streamType = 'rtsp',
    status = 'unknown',
    statusMessage = '',
    disabled = false,
    onUpdate,
    onDelete,
  }: Props = $props();

  // Local editing state - initialized with defaults, synced from props in startEdit()
  let isEditing = $state(false);
  let editUrl = $state('');
  let editTransport = $state('tcp');
  let editStreamType = $state('rtsp');
  let showDeleteConfirm = $state(false);

  // Stream type options (RTSP only for now)
  const streamTypeOptions = [
    { value: 'rtsp', label: 'RTSP' },
    // Future options - disabled for now
    // { value: 'http', label: 'HTTP', disabled: true },
    // { value: 'hls', label: 'HLS', disabled: true },
    // { value: 'rtmp', label: 'RTMP', disabled: true },
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

  // Get color for this stream based on index
  let iconColor = $derived(iconColors[index % iconColors.length]);

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

  let displayUrl = $derived(maskCredentials(url));
  let cardStyles = $derived(getCardStyles(status));

  function startEdit() {
    editUrl = url;
    editTransport = transport;
    editStreamType = streamType;
    isEditing = true;
  }

  function cancelEdit() {
    isEditing = false;
    showDeleteConfirm = false;
  }

  function saveEdit() {
    if (editUrl.trim()) {
      onUpdate(editUrl.trim(), editTransport, editStreamType);
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
            {t('common.actions.cancel')}
          </button>
          <button type="button" class="btn btn-sm btn-error" onclick={executeDelete}>
            {t('common.actions.delete')}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <div class="p-4">
    {#if isEditing}
      <!-- Edit Mode -->
      <div class="space-y-4">
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
          <div class="form-control">
            <label class="label py-1" for="stream-type-{index}">
              <span class="label-text text-xs font-medium">
                {t('settings.audio.streams.typeLabel')}
              </span>
            </label>
            <select
              id="stream-type-{index}"
              bind:value={editStreamType}
              class="select select-sm w-full"
              disabled
            >
              {#each streamTypeOptions as opt (opt.value)}
                <option value={opt.value}>{opt.label}</option>
              {/each}
            </select>
            <span class="text-xs text-base-content/50 mt-1">
              {t('settings.audio.streams.typeLockedNote')}
            </span>
          </div>

          <div class="form-control">
            <label class="label py-1" for="stream-transport-{index}">
              <span class="label-text text-xs font-medium">
                {t('settings.audio.streams.transportLabel')}
              </span>
            </label>
            <select
              id="stream-transport-{index}"
              bind:value={editTransport}
              class="select select-sm w-full"
            >
              {#each transportOptions as opt (opt.value)}
                <option value={opt.value}>{opt.label}</option>
              {/each}
            </select>
          </div>
        </div>

        <!-- Action Buttons -->
        <div class="flex justify-end gap-2 pt-2 border-t border-base-300">
          <button type="button" class="btn btn-sm btn-ghost gap-1.5" onclick={cancelEdit}>
            <X class="size-4" />
            {t('common.actions.cancel')}
          </button>
          <button
            type="button"
            class="btn btn-sm btn-primary gap-1.5"
            onclick={saveEdit}
            disabled={!editUrl.trim()}
          >
            <Check class="size-4" />
            {t('common.actions.save')}
          </button>
        </div>
      </div>
    {:else}
      <!-- View Mode -->
      <div class="flex items-start gap-4">
        <!-- Stream Icon -->
        <div
          class={cn(
            'flex-shrink-0 size-12 rounded-xl flex items-center justify-center border',
            status === 'error' ? 'bg-error/20 text-error border-error/30' : iconColor.bg,
            status === 'error' ? '' : iconColor.text,
            status === 'error' ? '' : iconColor.border
          )}
        >
          <Radio class="size-6" />
        </div>

        <!-- Stream Info -->
        <div class="flex-1 min-w-0">
          <!-- Stream Name and Status Row -->
          <div class="flex items-center gap-2 mb-1">
            <span class="text-sm font-semibold text-base-content">
              {t('settings.audio.streams.streamLabel')}
              {index + 1}
            </span>
            <StatusPill
              variant={getStatusVariant(status)}
              label={getStatusText(status)}
              size="xs"
              pulse={status === 'connecting'}
            />
          </div>

          <!-- URL -->
          <p class="font-mono text-sm text-base-content/80 break-all leading-relaxed">
            {displayUrl}
          </p>

          <!-- Error Message (if present) -->
          {#if status === 'error' && statusMessage}
            <p class="text-xs text-error mt-1.5">{statusMessage}</p>
          {/if}

          <!-- Stream Settings Tags -->
          <div class="flex items-center gap-2 mt-2">
            <span class="badge badge-sm badge-outline font-medium">
              {streamType.toUpperCase()}
            </span>
            <span class="badge badge-sm badge-outline font-medium">
              {transport.toUpperCase()}
            </span>
            {#if statusMessage && status !== 'error'}
              <span class="text-xs text-base-content/60 truncate" title={statusMessage}>
                {statusMessage}
              </span>
            {/if}
          </div>
        </div>

        <!-- Action Buttons - Always Visible for Accessibility -->
        <div class="flex-shrink-0 flex gap-1">
          <button
            type="button"
            class="btn btn-sm btn-ghost btn-square"
            onclick={startEdit}
            {disabled}
            aria-label={t('common.actions.edit')}
          >
            <Pencil class="size-4" />
          </button>
          <button
            type="button"
            class="btn btn-sm btn-ghost btn-square text-error hover:bg-error/10"
            onclick={confirmDelete}
            {disabled}
            aria-label={t('common.actions.delete')}
          >
            <Trash2 class="size-4" />
          </button>
        </div>
      </div>
    {/if}
  </div>
</div>
