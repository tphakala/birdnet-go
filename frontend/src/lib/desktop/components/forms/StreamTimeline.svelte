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
    // Data discriminator (`to_state` for state transitions, `error_type` for
    // errors). Precomputed during derivation so the template and aria-label
    // can access it without repeated union narrowing.
    discriminator: string;
    // Stable per-event key precomputed during derivation. Combines timestamp,
    // type, discriminator, and a per-duplicate ordinal so multiple events
    // with identical data still produce distinct keys without depending on
    // the sliding render index (BIRDNET-GO-1A0).
    key: string;
  }

  interface Props {
    stateHistory?: StateTransition[];
    errorHistory?: ErrorContext[];
  }

  let { stateHistory = [], errorHistory = [] }: Props = $props();

  // Selected event for popover, tracked by its stable key so SSE-driven list
  // changes (slice(-10)) cannot invalidate the selection before the user
  // toggles it off.
  let selectedEvent = $state<TimelineEvent | null>(null);
  let selectedKey = $state<string | null>(null);
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

  // Returns the discriminator string for an event (state name for transitions,
  // error type for errors). Used when building the derived list.
  function eventDiscriminator(
    type: 'state' | 'error',
    data: StateTransition | ErrorContext
  ): string {
    return type === 'error'
      ? ((data as ErrorContext).error_type ?? '')
      : ((data as StateTransition).to_state ?? '');
  }

  // Merge and sort state and error history into unified timeline, assigning
  // a stable composite key and precomputed discriminator to each event. The
  // per-duplicate ordinal is computed over the sorted list (pre-slice) so an
  // event's key does not change when the sliding window drops older events.
  let timelineEvents = $derived.by(() => {
    type RawEvent = {
      type: 'state' | 'error';
      timestamp: Date;
      data: StateTransition | ErrorContext;
    };
    const raw: RawEvent[] = [];

    // Add state transitions (filter invalid timestamps)
    for (const state of stateHistory ?? []) {
      const timestamp = parseTimestamp(state.timestamp);
      if (timestamp) {
        raw.push({
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
        raw.push({
          type: 'error',
          timestamp,
          data: error,
        });
      }
    }

    // Sort by timestamp ascending (oldest first)
    raw.sort((a, b) => a.timestamp.getTime() - b.timestamp.getTime());

    // Assign a deterministic per-(timestamp,type,discriminator) ordinal so
    // true duplicate events still produce unique keys without relying on
    // the sliding-window render index (Gemini / Sentry / CodeRabbit review).
    const occurrence = new Map<string, number>();
    const keyed: TimelineEvent[] = raw.map(event => {
      const discriminator = eventDiscriminator(event.type, event.data);
      const base = `${event.timestamp.getTime()}_${event.type}_${discriminator}`;
      const ordinal = occurrence.get(base) ?? 0;
      occurrence.set(base, ordinal + 1);
      return { ...event, discriminator, key: `${base}_${ordinal}` };
    });

    // Limit to last 10 events; ordinals were assigned pre-slice so surviving
    // events keep their keys when older events drop off.
    return keyed.slice(-10);
  });

  // Get node color based on event type and data
  function getNodeColor(event: TimelineEvent): { bg: string; border: string } {
    if (event.type === 'error') {
      return { bg: 'bg-[var(--color-error)]', border: 'border-[var(--color-error)]' };
    }

    const state = event.data as StateTransition;
    const toState = state.to_state?.toLowerCase() ?? '';

    if (toState === 'running' || toState === 'started' || toState === 'healthy') {
      return { bg: 'bg-[var(--color-success)]', border: 'border-[var(--color-success)]' };
    }
    if (toState === 'restarting' || toState === 'backoff') {
      return { bg: 'bg-[var(--color-warning)]', border: 'border-[var(--color-warning)]' };
    }
    if (toState === 'circuit_open' || toState === 'stopped' || toState === 'failed') {
      return { bg: 'bg-[var(--color-error)]', border: 'border-[var(--color-error)]' };
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
    // Compare by the event's stable key so SSE-driven list updates cannot
    // stale the selection (BIRDNET-GO-1A0).
    if (selectedKey === event.key) {
      selectedEvent = null;
      selectedKey = null;
      selectedNodeEl = null;
    } else {
      selectedEvent = event;
      selectedKey = event.key;
      selectedNodeEl = nodeEl;
    }
  }

  function handleClosePopover() {
    selectedEvent = null;
    selectedKey = null;
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
          class="absolute top-1.5 h-0.5 bg-[var(--color-base-content)]/50"
          style:left="28px"
          style:right="28px"
        ></div>
      {/if}

      {#each timelineEvents as event (event.key)}
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
              hollow ? 'bg-[var(--color-base-100)]' : colors.bg
            )}
            onclick={e => handleNodeClick(event, e.currentTarget)}
            aria-label={[
              t('settings.audio.streams.timeline.eventAt', {
                time: formatTime(event.timestamp),
              }),
              event.type === 'error' ? t('settings.audio.streams.timeline.error') : '',
              event.discriminator,
            ]
              .filter(Boolean)
              .join(' — ')}
          ></button>

          <!-- Timestamp -->
          <span class="text-xs text-[var(--color-base-content)]/60 mt-1.5 whitespace-nowrap">
            {formatTime(event.timestamp)}
          </span>

          <!-- State label (abbreviated) -->
          <span class="text-xs text-[var(--color-base-content)]/60 truncate max-w-[60px]">
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
  <p class="text-xs text-[var(--color-base-content)]/50">
    {t('settings.audio.streams.timeline.noHistory')}
  </p>
{/if}
