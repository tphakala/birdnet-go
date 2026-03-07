<!--
CurrentlyHearingCard.svelte - Real-time pending detection display

Purpose:
- Shows species currently being detected by BirdNET in real-time
- Provides visual feedback when detections are approved or rejected
- Hidden entirely when no pending detections exist

Props:
- detections: PendingDetection[] - Current pending detection snapshot from SSE
- className?: string - Additional CSS classes (default: '')
-->
<script lang="ts">
  import { Radio, Check, X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { PendingDetection } from '$lib/types/pending.types';

  interface Props {
    detections: PendingDetection[];
    className?: string;
  }

  let { detections = [], className = '' }: Props = $props();

  // Compute relative time string from Unix timestamp
  function getElapsedText(firstDetected: number): string {
    const elapsed = Math.max(0, Math.floor(Date.now() / 1000 - firstDetected));
    if (elapsed < 60) return `${elapsed}s`;
    const minutes = Math.floor(elapsed / 60);
    if (minutes < 60) return `${minutes}m`;
    const hours = Math.floor(minutes / 60);
    return `${hours}h`;
  }

  // Refresh elapsed times every second
  let tick = $state(0);
  let hasDetections = $derived(detections.length > 0);
  $effect(() => {
    if (!hasDetections) return;
    const interval = setInterval(() => {
      tick++;
    }, 1000);
    return () => clearInterval(interval);
  });

  // Force re-evaluation of elapsed text when tick changes
  let elapsedTexts = $derived.by(() => {
    // Reference tick to trigger reactivity
    void tick;
    const result: Record<string, string> = {};
    for (const d of detections) {
      result[d.source + d.scientificName] = getElapsedText(d.firstDetected);
    }
    return result;
  });

  // Show source column only when multiple sources are present
  let hasMultipleSources = $derived(new Set(detections.map(d => d.source)).size > 1);
</script>

{#if hasDetections}
  <div
    class="mt-4 rounded-xl border border-[var(--color-border)] bg-[var(--color-base-200)] p-4 {className}"
  >
    <div class="mb-3 flex items-center gap-2">
      <Radio class="h-4 w-4 animate-pulse text-[var(--color-success)]" />
      <h3 class="text-sm font-semibold text-[var(--color-base-content)]">
        {t('dashboard.currentlyHearing')}
      </h3>
    </div>

    <div class="flex flex-wrap gap-3">
      {#each detections as detection (detection.source + detection.scientificName)}
        {@const key = detection.source + detection.scientificName}
        <div
          class="flex items-center gap-2 rounded-lg px-3 py-2 transition-all duration-300
            {detection.status === 'approved'
            ? 'border border-[var(--color-success)]/30 bg-[var(--color-success)]/15'
            : detection.status === 'rejected'
              ? 'border border-[var(--color-error)]/30 bg-[var(--color-error)]/15 opacity-60'
              : 'border border-transparent bg-[var(--color-base-300)]'}"
        >
          <!-- Thumbnail -->
          {#if detection.thumbnail}
            <img
              src={detection.thumbnail}
              alt={detection.species}
              class="h-8 w-8 rounded-full object-cover"
            />
          {:else}
            <div
              class="flex h-8 w-8 items-center justify-center rounded-full bg-[var(--color-base-content)]/10"
            >
              <Radio class="h-4 w-4 text-[var(--color-base-content)]/50" />
            </div>
          {/if}

          <!-- Species info -->
          <div class="flex flex-col">
            <span class="text-sm font-medium leading-tight text-[var(--color-base-content)]">
              {detection.species}
            </span>
            <span class="text-xs text-[var(--color-base-content)]/60">
              {elapsedTexts[key] ?? ''}
              {#if hasMultipleSources}
                · {detection.source}
              {/if}
            </span>
          </div>

          <!-- Status indicator -->
          {#if detection.status === 'approved'}
            <Check
              aria-label={t('dashboard.approved')}
              class="ml-1 h-4 w-4 text-[var(--color-success)]"
            />
          {:else if detection.status === 'rejected'}
            <X
              aria-label={t('dashboard.rejected')}
              class="ml-1 h-4 w-4 text-[var(--color-error)]"
            />
          {/if}
        </div>
      {/each}
    </div>
  </div>
{/if}
