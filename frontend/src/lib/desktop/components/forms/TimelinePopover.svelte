<!--
  Timeline Popover Component

  Purpose: Display detailed information about a timeline event (state transition or error).

  Features:
  - Shows error context with troubleshooting steps
  - Shows state transition details
  - Positioned relative to anchor element
  - Closes on click outside or Escape key

  @component
-->
<script lang="ts">
  import { t, getLocale } from '$lib/i18n';
  import { X } from '@lucide/svelte';
  import type { ErrorContext, StateTransition } from './StreamManager.svelte';
  import type { TimelineEvent } from './StreamTimeline.svelte';

  interface Props {
    event: TimelineEvent;
    anchorEl: HTMLElement;
    onClose: () => void;
  }

  let { event, anchorEl, onClose }: Props = $props();

  // Format timestamp with date and time (using app locale)
  function formatDateTime(date: Date): string {
    return new Intl.DateTimeFormat(getLocale(), {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    }).format(date);
  }

  // Get error data if this is an error event
  let errorData = $derived(event.type === 'error' ? (event.data as ErrorContext) : null);

  // Get state data if this is a state event
  let stateData = $derived(event.type === 'state' ? (event.data as StateTransition) : null);

  // Handle click outside
  function handleClickOutside(e: MouseEvent) {
    const target = e.target as HTMLElement;
    const popover = document.getElementById('timeline-popover');
    // Guard against null anchorEl during component cleanup
    if (popover && !popover.contains(target) && anchorEl && !anchorEl.contains(target)) {
      onClose();
    }
  }
</script>

<svelte:window onclick={handleClickOutside} />

<div
  id="timeline-popover"
  class="absolute z-20 mt-2 left-1/2 -translate-x-1/2 w-80 max-w-[90vw] bg-base-200 border border-base-content/20 rounded-lg p-3 shadow-lg"
  role="dialog"
  aria-labelledby="popover-title"
>
  <!-- Header with close button -->
  <div class="flex items-start justify-between gap-2 mb-2">
    <div>
      {#if errorData}
        <h4 id="popover-title" class="font-medium text-base-content text-sm">
          {errorData.user_facing_msg || t('settings.audio.streams.timeline.error')}
        </h4>
      {:else if stateData}
        <h4 id="popover-title" class="font-medium text-base-content text-sm">
          {t('settings.audio.streams.timeline.stateChange')}: {stateData.to_state}
        </h4>
      {/if}
      <p class="text-xs text-base-content/50 mt-0.5">
        {formatDateTime(event.timestamp)}
      </p>
    </div>
    <button
      type="button"
      class="p-1 rounded hover:bg-base-content/10 transition-colors flex-shrink-0"
      onclick={onClose}
      aria-label={t('common.close')}
    >
      <X class="size-4 text-base-content/60" />
    </button>
  </div>

  {#if errorData}
    <!-- Error details -->
    {#if errorData.primary_message}
      <p class="text-sm text-base-content/70 mt-2">
        {errorData.primary_message}
      </p>
    {/if}

    <!-- Additional context -->
    {#if errorData.target_host}
      <p class="text-xs text-base-content/50 mt-2">
        {t('settings.audio.streams.timeline.host')}: {errorData.target_host}{errorData.target_port
          ? `:${errorData.target_port}`
          : ''}
      </p>
    {/if}

    <!-- Troubleshooting steps -->
    {#if errorData.troubleshooting_steps && errorData.troubleshooting_steps.length > 0}
      <div class="mt-3 pt-3 border-t border-base-content/20">
        <p class="text-xs font-medium text-base-content/70 mb-2">
          {t('settings.audio.streams.timeline.troubleshooting')}:
        </p>
        <ul class="text-xs text-base-content/70 space-y-1.5">
          {#each errorData.troubleshooting_steps as step, i (i)}
            <li class="flex items-start gap-2">
              <span class="text-base-content/50 select-none">•</span>
              <span>{step}</span>
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  {:else if stateData}
    <!-- State transition details -->
    <div class="space-y-2 text-sm">
      {#if stateData.from_state}
        <div class="flex gap-2">
          <span class="text-base-content/50">{t('settings.audio.streams.timeline.from')}:</span>
          <span class="text-base-content">{stateData.from_state}</span>
        </div>
      {/if}
      <div class="flex gap-2">
        <span class="text-base-content/50">{t('settings.audio.streams.timeline.to')}:</span>
        <span class="text-base-content">{stateData.to_state}</span>
      </div>
      {#if stateData.reason}
        <div class="mt-2 pt-2 border-t border-base-content/20">
          <span class="text-base-content/50">{t('settings.audio.streams.timeline.reason')}:</span>
          <p class="text-base-content/70 mt-1">{stateData.reason}</p>
        </div>
      {/if}
    </div>
  {/if}
</div>
