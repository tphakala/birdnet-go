<!--
  Stream Card Component

  Purpose: Display and manage a single audio stream with health status indicator,
  URL editing, stream type, and protocol settings.

  Features:
  - Traffic light health indicator (green/yellow/red/gray)
  - Editable stream URL with credential masking
  - Stream type selector (RTSP only for now, prepared for HTTP, HLS, RTMP)
  - Protocol selector (TCP/UDP) per stream
  - Inline editing mode
  - Delete confirmation

  @component
-->
<script lang="ts">
  import { Pencil, Trash2, Check, X, AlertCircle } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';

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

  // Local editing state
  let isEditing = $state(false);
  let editUrl = $state(url);
  let editTransport = $state(transport);
  let editStreamType = $state(streamType);
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

  // Get status indicator color and styles
  function getStatusStyles(s: StreamStatus): { color: string; bgColor: string; ringColor: string } {
    switch (s) {
      case 'connected':
        return {
          color: 'text-success',
          bgColor: 'bg-success',
          ringColor: 'ring-success/30',
        };
      case 'connecting':
        return {
          color: 'text-warning',
          bgColor: 'bg-warning',
          ringColor: 'ring-warning/30',
        };
      case 'error':
        return {
          color: 'text-error',
          bgColor: 'bg-error',
          ringColor: 'ring-error/30',
        };
      case 'idle':
        return {
          color: 'text-base-content/40',
          bgColor: 'bg-base-content/40',
          ringColor: 'ring-base-content/10',
        };
      default:
        return {
          color: 'text-base-content/20',
          bgColor: 'bg-base-content/20',
          ringColor: 'ring-base-content/5',
        };
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

  let statusStyles = $derived(getStatusStyles(status));
  let displayUrl = $derived(maskCredentials(url));

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
    'group relative rounded-lg border transition-all duration-200',
    isEditing
      ? 'border-primary/50 bg-base-100 shadow-md'
      : 'border-base-300 bg-base-200/50 hover:border-base-content/20 hover:bg-base-200',
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
      <div class="flex items-start gap-3">
        <!-- Status Indicator -->
        <div class="flex-shrink-0 pt-1">
          <div
            class={cn(
              'size-3 rounded-full ring-4 transition-all',
              statusStyles.bgColor,
              statusStyles.ringColor,
              status === 'connecting' && 'animate-pulse'
            )}
            title={getStatusText(status)}
          ></div>
        </div>

        <!-- Stream Info -->
        <div class="flex-1 min-w-0">
          <!-- Stream Name/Number -->
          <div class="flex items-center gap-2 mb-1">
            <span class="text-xs font-semibold text-base-content/70 uppercase tracking-wide">
              {t('settings.audio.streams.streamLabel')}
              {index + 1}
            </span>
            <span
              class={cn(
                'text-xs px-1.5 py-0.5 rounded font-medium',
                status === 'connected' && 'bg-success/10 text-success',
                status === 'connecting' && 'bg-warning/10 text-warning',
                status === 'error' && 'bg-error/10 text-error',
                status === 'idle' && 'bg-base-content/10 text-base-content/60',
                status === 'unknown' && 'bg-base-content/5 text-base-content/40'
              )}
            >
              {getStatusText(status)}
            </span>
          </div>

          <!-- URL -->
          <p class="font-mono text-sm text-base-content break-all leading-relaxed">
            {displayUrl}
          </p>

          <!-- Stream Settings Tags -->
          <div class="flex items-center gap-2 mt-2">
            <span class="badge badge-sm badge-outline">
              {streamType.toUpperCase()}
            </span>
            <span class="badge badge-sm badge-outline">
              {transport.toUpperCase()}
            </span>
            {#if statusMessage}
              <span class="text-xs text-base-content/60 truncate" title={statusMessage}>
                {statusMessage}
              </span>
            {/if}
          </div>
        </div>

        <!-- Action Buttons -->
        <div class="flex-shrink-0 flex gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
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
