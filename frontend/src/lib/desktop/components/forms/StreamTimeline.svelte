<!--
  Stream Timeline Component

  Purpose: Visualize state transitions and errors over time for an RTSP stream.

  Features:
  - Horizontal timeline with colored nodes
  - Green nodes for healthy states (running, started)
  - Amber nodes for warnings (restarting, backoff)
  - Red nodes for errors
  - Click on node to view details via TimelinePopover

  @component
-->
<script lang="ts">
  import { t, getLocale } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import type { ErrorContext, StateTransition } from './StreamManager.svelte';
  import TimelinePopover from './TimelinePopover.svelte';

  export interface TimelineEvent {
    type: 'state' | 'error';
    timestamp: Date;
    data: StateTransition | ErrorContext;
  }

  interface Props {
    stateHistory?: StateTransition[];
    errorHistory?: ErrorContext[];
  }

  let { stateHistory = [], errorHistory = [] }: Props = $props();

  // Selected event for popover
  let selectedEvent = $state<TimelineEvent | null>(null);
  let selectedNodeEl = $state<HTMLElement | null>(null);

  // Format timestamp for display (24-hour format, using app locale)
  function formatTime(date: Date): string {
    return new Intl.DateTimeFormat(getLocale(), {
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    }).format(date);
  }

  // Safely parse a timestamp, returning null if invalid
  function parseTimestamp(timestamp: string | undefined): Date | null {
    if (!timestamp) return null;
    const date = new Date(timestamp);
    return isNaN(date.getTime()) ? null : date;
  }

  // Merge and sort state and error history into unified timeline
  let timelineEvents = $derived.by(() => {
    const events: TimelineEvent[] = [];

    // Add state transitions (filter invalid timestamps)
    for (const state of stateHistory ?? []) {
      const timestamp = parseTimestamp(state.timestamp);
      if (timestamp) {
        events.push({
          type: 'state',
          timestamp,
          data: state,
        });
      }
    }

    // Add errors (filter invalid timestamps)
    for (const error of errorHistory ?? []) {
      const timestamp = parseTimestamp(error.timestamp);
      if (timestamp) {
        events.push({
          type: 'error',
          timestamp,
          data: error,
        });
      }
    }

    // Sort by timestamp ascending (oldest first)
    events.sort((a, b) => a.timestamp.getTime() - b.timestamp.getTime());

    // Limit to last 10 events
    return events.slice(-10);
  });

  // Get node color based on event type and data
  function getNodeColor(event: TimelineEvent): { bg: string; border: string } {
    if (event.type === 'error') {
      return { bg: 'bg-red-400', border: 'border-red-400' };
    }

    const state = event.data as StateTransition;
    const toState = state.to_state?.toLowerCase() ?? '';

    if (toState === 'running' || toState === 'started' || toState === 'healthy') {
      return { bg: 'bg-green-400', border: 'border-green-400' };
    }
    if (toState === 'restarting' || toState === 'backoff') {
      return { bg: 'bg-amber-400', border: 'border-amber-400' };
    }
    if (toState === 'circuit_open' || toState === 'stopped' || toState === 'failed') {
      return { bg: 'bg-red-400', border: 'border-red-400' };
    }

    return { bg: 'bg-gray-400', border: 'border-gray-400' };
  }

  // Determine if node should be hollow (warning states)
  function isHollow(event: TimelineEvent): boolean {
    if (event.type === 'error') return false;

    const state = event.data as StateTransition;
    const toState = state.to_state?.toLowerCase() ?? '';

    return toState === 'restarting' || toState === 'backoff';
  }

  function handleNodeClick(event: TimelineEvent, nodeEl: HTMLElement) {
    // Compare by timestamp to avoid Svelte 5 proxy equality issues
    if (selectedEvent?.timestamp.getTime() === event.timestamp.getTime()) {
      selectedEvent = null;
      selectedNodeEl = null;
    } else {
      selectedEvent = event;
      selectedNodeEl = nodeEl;
    }
  }

  function handleClosePopover() {
    selectedEvent = null;
    selectedNodeEl = null;
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      handleClosePopover();
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

{#if timelineEvents.length > 0}
  <div class="relative overflow-x-auto">
    <!-- Timeline container with connecting line -->
    <div class="inline-flex items-start min-w-fit relative">
      <!-- Connecting line between nodes -->
      {#if timelineEvents.length > 1}
        <div
          class="absolute top-1.5 h-0.5 bg-base-content/50"
          style:left="28px"
          style:right="28px"
        ></div>
      {/if}

      {#each timelineEvents as event, _idx (event.timestamp.getTime())}
        {@const colors = getNodeColor(event)}
        {@const hollow = isHollow(event)}
        <div class="flex flex-col items-center w-14 flex-shrink-0">
          <!-- Node -->
          <button
            type="button"
            class={cn(
              'relative z-10 w-3 h-3 rounded-full border-2 cursor-pointer',
              'hover:scale-125 transition-transform',
              colors.border,
              hollow ? 'bg-base-100' : colors.bg
            )}
            onclick={e => handleNodeClick(event, e.currentTarget)}
            aria-label={t('settings.audio.streams.timeline.eventAt', {
              time: formatTime(event.timestamp),
            })}
          ></button>

          <!-- Timestamp -->
          <span class="text-xs text-base-content/60 mt-1.5 whitespace-nowrap">
            {formatTime(event.timestamp)}
          </span>

          <!-- State label (abbreviated) -->
          <span class="text-xs text-base-content/60 truncate max-w-[60px]">
            {#if event.type === 'error'}
              {t('settings.audio.streams.timeline.error')}
            {:else}
              {(event.data as StateTransition).to_state ?? ''}
            {/if}
          </span>
        </div>
      {/each}
    </div>

    <!-- Popover -->
    {#if selectedEvent && selectedNodeEl}
      <TimelinePopover
        event={selectedEvent}
        anchorEl={selectedNodeEl}
        onClose={handleClosePopover}
      />
    {/if}
  </div>
{:else}
  <p class="text-xs text-base-content/50">{t('settings.audio.streams.timeline.noHistory')}</p>
{/if}
