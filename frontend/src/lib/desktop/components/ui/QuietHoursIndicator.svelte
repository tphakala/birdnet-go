<!--
  Quiet Hours Indicator Component

  Purpose: Display a header icon indicating when quiet hours is active
  (any source — sound card or stream — is currently suppressed).

  Features:
  - Moon icon shown only when quiet hours is active for any source
  - Polls backend status endpoint at a configurable interval
  - Tooltip showing suppression details on hover
  - Matches NotificationBell styling pattern

  @component
-->
<script lang="ts">
  import { MoonStar } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn';
  import { api } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { t } from '$lib/i18n';

  const logger = loggers.ui;

  /** Polling interval in milliseconds */
  const POLL_INTERVAL_MS = 30000;

  interface QuietHoursStatus {
    anyActive: boolean;
    soundCardSuppressed: boolean;
    suppressedStreams: Record<string, boolean>;
  }

  interface Props {
    className?: string;
  }

  let { className = '' }: Props = $props();

  // State
  let status = $state<QuietHoursStatus | null>(null);

  // Derived: count of suppressed sources for tooltip
  let suppressedCount = $derived.by(() => {
    if (!status) return 0;
    let count = status.soundCardSuppressed ? 1 : 0;
    for (const suppressed of Object.values(status.suppressedStreams)) {
      if (suppressed) count++;
    }
    return count;
  });

  let tooltipText = $derived(t('quietHours.indicator.tooltip', { count: suppressedCount }));

  // Fetch quiet hours status from backend
  async function fetchStatus() {
    try {
      const data = await api.get<QuietHoursStatus>('/api/v2/streams/quiet-hours/status');
      status = data;
    } catch {
      // Silently fail — indicator just won't show
      logger.debug('Failed to fetch quiet hours status', null, {
        component: 'QuietHoursIndicator',
      });
    }
  }

  // Poll on mount + interval, clean up on destroy
  $effect(() => {
    if (typeof window !== 'undefined') {
      fetchStatus();
      const timer = globalThis.setInterval(fetchStatus, POLL_INTERVAL_MS);
      return () => globalThis.clearInterval(timer);
    }
  });
</script>

{#if status?.anyActive}
  <div class={cn('relative', className)}>
    <button
      class="btn btn-ghost btn-sm p-1 relative text-warning"
      aria-label={tooltipText}
      title={tooltipText}
    >
      <MoonStar class="size-6" />
    </button>
  </div>
{/if}
