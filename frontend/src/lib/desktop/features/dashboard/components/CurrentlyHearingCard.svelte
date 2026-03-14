<!--
CurrentlyHearingCard.svelte - Real-time pending detection display

Purpose:
- Shows species currently being detected by BirdNET in real-time
- Provides visual feedback when detections are approved or rejected
- Retains terminal (approved/rejected) states for a few seconds before fading out
- Hidden entirely when no pending detections exist

Props:
- detections: PendingDetection[] - Current pending detection snapshot from SSE
- className?: string - Additional CSS classes (default: '')
-->
<script lang="ts">
  import { Check, X } from '@lucide/svelte';
  import { fade } from 'svelte/transition';
  import { t } from '$lib/i18n';
  import type { PendingDetection } from '$lib/types/pending.types';

  interface Props {
    detections: PendingDetection[];
    className?: string;
  }

  let { detections = [], className = '' }: Props = $props();

  // How long terminal (approved/rejected) detections remain visible (ms)
  const TERMINAL_RETENTION_MS = 3000;

  // Retained terminal detections kept visible after backend stops sending them
  let retainedKeys = $state<string[]>([]);
  let retainedData: Record<string, PendingDetection> = {};
  let removalTimers: Record<string, ReturnType<typeof setTimeout>> = {};

  function detectionKey(d: PendingDetection): string {
    return d.source + d.scientificName;
  }

  // Track terminal detections and schedule their removal
  $effect(() => {
    for (const d of detections) {
      const key = detectionKey(d);
      if ((d.status === 'approved' || d.status === 'rejected') && !(key in removalTimers)) {
        retainedData[key] = d;
        removalTimers[key] = setTimeout(() => {
          delete retainedData[key];
          delete removalTimers[key];
          retainedKeys = retainedKeys.filter(k => k !== key);
        }, TERMINAL_RETENTION_MS);
        if (!retainedKeys.includes(key)) {
          retainedKeys = [...retainedKeys, key];
        }
      }
    }
  });

  // Merge incoming detections with retained terminal ones
  let displayDetections = $derived.by(() => {
    // Read retainedKeys to establish reactive dependency
    const retained = retainedKeys;

    const incomingByKey = new Set<string>();
    for (const d of detections) {
      incomingByKey.add(detectionKey(d));
    }

    const result: PendingDetection[] = [...detections];
    for (const key of retained) {
      if (!incomingByKey.has(key)) {
        const data = retainedData[key];
        if (data) {
          result.push(data);
        }
      }
    }

    // Sort newest first so new detections appear on the left
    result.sort((a, b) => b.firstDetected - a.firstDetected);
    return result;
  });

  let hasDisplayDetections = $derived(displayDetections.length > 0);

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
  $effect(() => {
    if (!hasDisplayDetections) return;
    const interval = setInterval(() => {
      tick++;
    }, 1000);
    return () => clearInterval(interval);
  });

  // Force re-evaluation of elapsed text when tick changes
  let elapsedTexts = $derived.by(() => {
    void tick;
    const result: Record<string, string> = {};
    for (const d of displayDetections) {
      result[detectionKey(d)] = getElapsedText(d.firstDetected);
    }
    return result;
  });

  // Show source column only when multiple sources are present
  let hasMultipleSources = $derived(new Set(displayDetections.map(d => d.source)).size > 1);

  // Clean up pending timers on component destroy
  $effect(() => {
    return () => {
      for (const key in removalTimers) {
        clearTimeout(removalTimers[key]);
      }
    };
  });
</script>

<section
  class="card col-span-12 flex h-full flex-col rounded-2xl border border-border-100 bg-[var(--color-base-100)] shadow-sm {className}"
>
  <!-- Card Header -->
  <div class="flex items-center gap-2 border-b border-[var(--color-base-200)] px-6 py-4">
    <div class="flex flex-col">
      <h3 class="font-semibold">{t('dashboard.currentlyHearing.title')}</h3>
      <p class="text-sm text-[var(--color-base-content)]/60">
        {t('dashboard.currentlyHearing.subtitle')}
      </p>
    </div>
  </div>

  <!-- Card Content -->
  {#if hasDisplayDetections}
    <div class="flex flex-wrap gap-3 p-4">
      {#each displayDetections as detection (`${detection.source}_${detection.scientificName}`)}
        {@const key = detection.source + detection.scientificName}
        <div
          class="flex items-center gap-2 rounded-lg px-3 py-2 transition-colors duration-300
            {detection.status === 'approved'
            ? 'border border-[var(--color-success)]/30 bg-[var(--color-success)]/15'
            : detection.status === 'rejected'
              ? 'border border-[var(--color-error)]/30 bg-[var(--color-error)]/15 opacity-60'
              : 'border border-transparent bg-[var(--color-base-200)]'}"
          transition:fade={{ duration: 200 }}
        >
          <!-- Thumbnail -->
          {#if detection.thumbnail}
            <img
              src={detection.thumbnail}
              alt={detection.species}
              class="h-8 aspect-[4/3] rounded-md object-cover"
            />
          {:else}
            <div
              class="flex h-8 aspect-[4/3] items-center justify-center rounded-md bg-[var(--color-base-content)]/10 text-xs font-bold text-[var(--color-base-content)]/50"
            >
              {detection.species.slice(0, 2).toUpperCase()}
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
  {:else}
    <div class="flex flex-1 items-center justify-center px-6 py-8">
      <p class="text-sm text-[var(--color-base-content)]/40">
        {t('dashboard.currentlyHearing.empty')}
      </p>
    </div>
  {/if}
</section>
