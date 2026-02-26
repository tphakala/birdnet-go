<!--
  Quiet Hours Indicator Component

  Purpose: Display a header icon indicating when quiet hours is active
  (any source — sound card or stream — is currently suppressed).

  Features:
  - Moon icon shown only when quiet hours is active for any source
  - Uses shared quiet hours store for status (no duplicate polling)
  - Tooltip showing suppression details on hover
  - Matches NotificationBell styling pattern

  @component
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { MoonStar } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import { quietHoursStore } from '$lib/stores/quietHours.svelte';

  interface Props {
    className?: string;
  }

  let { className = '' }: Props = $props();

  // Derived: count of suppressed sources for tooltip
  let suppressedCount = $derived.by(() => {
    const status = quietHoursStore.status;
    if (!status) return 0;
    let count = status.soundCardSuppressed ? 1 : 0;
    for (const suppressed of Object.values(status.suppressedStreams)) {
      if (suppressed) count++;
    }
    return count;
  });

  let tooltipText = $derived(t('quietHours.indicator.tooltip', { count: suppressedCount }));

  onMount(() => {
    quietHoursStore.startPolling();
  });

  onDestroy(() => {
    quietHoursStore.stopPolling();
  });
</script>

{#if quietHoursStore.status?.anyActive}
  <div class={cn('relative', className)}>
    <button
      type="button"
      class="btn btn-ghost btn-sm p-1 relative text-warning"
      aria-label={tooltipText}
      title={tooltipText}
    >
      <MoonStar class="size-6" />
    </button>
  </div>
{/if}
