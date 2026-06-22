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
  import { Check, Mic, X } from '@lucide/svelte';
  import { fade } from 'svelte/transition';
  import { untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import type { PendingDetection } from '$lib/types/pending.types';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { settingsStore, dashboardSettings } from '$lib/stores/settings';
  import SpeciesDetailModal from '$lib/desktop/features/analytics/components/modals/SpeciesDetailModal.svelte';

  interface Props {
    detections: PendingDetection[];
    className?: string;
  }

  let { detections = [], className = '' }: Props = $props();

  // Clicking a "currently hearing" species opens the species guide modal. Only
  // wire it up when the guide feature is enabled, so the modal always has guide
  // content to show (it would otherwise be an empty name + image card).
  let guideEnabled = $derived($dashboardSettings?.speciesGuide?.enabled ?? false);

  // Minimal species shape the guide modal needs. A live ping has no aggregate
  // stats, so those fields are zeroed and the modal is opened with showStats={false}.
  interface GuideSpecies {
    common_name: string;
    scientific_name: string;
    count: number;
    avg_confidence: number;
    max_confidence: number;
    first_heard: string;
    last_heard: string;
    thumbnail_url?: string;
  }

  let guideSpecies = $state<GuideSpecies | null>(null);
  let guideModalOpen = $state(false);

  function openGuide(d: PendingDetection): void {
    guideSpecies = {
      common_name: d.species,
      scientific_name: d.scientificName,
      count: d.hitCount ?? 0,
      avg_confidence: 0,
      max_confidence: 0,
      first_heard: '',
      last_heard: '',
      thumbnail_url: d.thumbnail ? buildAppUrl(d.thumbnail) : undefined,
    };
    guideModalOpen = true;
  }

  function closeGuide(): void {
    guideModalOpen = false;
    guideSpecies = null;
  }

  // How long terminal (approved/rejected) detections remain visible (ms)
  const TERMINAL_RETENTION_MS = 3000;

  // Retained terminal detections kept visible after backend stops sending them
  let retainedKeys = $state<string[]>([]);
  let retainedData: Record<string, PendingDetection> = {};
  let removalTimers: Record<string, ReturnType<typeof setTimeout>> = {};

  function detectionKey(d: PendingDetection): string {
    return d.source + d.scientificName;
  }

  // Track terminal detections and schedule their removal.
  // Use untrack() when reading retainedKeys to avoid a read-write loop
  // (this effect should only re-run when detections changes, not retainedKeys).
  $effect(() => {
    for (const d of detections) {
      const key = detectionKey(d);
      if ((d.status === 'approved' || d.status === 'rejected') && !(key in removalTimers)) {
        /* eslint-disable security/detect-object-injection -- key is derived from detectionKey(), a controlled string */
        retainedData[key] = d;
        removalTimers[key] = setTimeout(() => {
          delete retainedData[key];
          delete removalTimers[key];
          /* eslint-enable security/detect-object-injection */
          retainedKeys = retainedKeys.filter(k => k !== key);
        }, TERMINAL_RETENTION_MS);
        if (!untrack(() => retainedKeys).includes(key)) {
          retainedKeys = [...untrack(() => retainedKeys), key];
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
        // eslint-disable-next-line security/detect-object-injection -- key is from retainedKeys, a controlled string array
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

  function getElapsedForKey(key: string): string {
    // eslint-disable-next-line security/detect-object-injection -- key is a controlled detection key string
    return elapsedTexts[key] ?? '';
  }

  // Show source when the instance has multiple audio sources configured.
  // Source names are private data, so this must stay settings-driven: guests
  // never load settings, which keeps the counts at 0 and the labels hidden
  // for unauthenticated viewers.
  let hasMultipleSources = $derived(
    ($settingsStore?.formData?.realtime?.audio?.sources?.length ?? 0) +
      ($settingsStore?.formData?.realtime?.rtsp?.streams?.filter(s => s.enabled).length ?? 0) >=
      2
  );

  // Clean up pending timers on component destroy
  $effect(() => {
    return () => {
      for (const key in removalTimers) {
        // eslint-disable-next-line security/detect-object-injection -- key is from for-in over own Record
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
        {@const elapsedText = getElapsedForKey(key)}
        <!-- Localized common name in the visitor's UI locale; falls back to the
             server-provided common name, then the scientific name. Keeps the
             "currently hearing" card consistent with the rest of the dashboard. -->
        {@const displayName = localizeSpeciesName(detection.scientificName, detection.species)}
        <svelte:element
          this={guideEnabled ? 'button' : 'div'}
          type={guideEnabled ? 'button' : undefined}
          class="flex items-center gap-2 rounded-lg px-3 py-2 text-left transition-colors duration-300
            {guideEnabled
            ? 'cursor-pointer hover:brightness-95 focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-primary)]'
            : ''}
            {detection.status === 'approved'
            ? 'border border-[var(--color-success)]/30 bg-[var(--color-success)]/15'
            : detection.status === 'rejected'
              ? 'border border-[var(--color-error)]/30 bg-[var(--color-error)]/15 opacity-60'
              : 'border border-transparent bg-[var(--color-base-200)]'}"
          onclick={guideEnabled ? () => openGuide(detection) : undefined}
          aria-label={guideEnabled
            ? `${t('analytics.species.guide.title')}: ${displayName}`
            : undefined}
          transition:fade={{ duration: 200 }}
        >
          <!-- Thumbnail -->
          {#if detection.thumbnail}
            <img
              src={buildAppUrl(detection.thumbnail)}
              alt={displayName}
              class="h-8 aspect-[4/3] rounded-md object-cover"
            />
          {:else}
            <div
              class="flex h-8 aspect-[4/3] items-center justify-center rounded-md bg-[var(--color-base-content)]/10 text-xs font-bold text-[var(--color-base-content)]/50"
            >
              {displayName.slice(0, 2).toUpperCase()}
            </div>
          {/if}

          <!-- Species info -->
          <div class="flex flex-col">
            <span class="text-sm font-medium leading-tight text-[var(--color-base-content)]">
              {displayName}
            </span>
            <span class="text-xs text-[var(--color-base-content)]/60">
              {elapsedText}
              {#if hasMultipleSources && detection.source}
                <span class="inline-flex items-center gap-0.5">
                  · <Mic class="size-3 inline" />{detection.source}
                </span>
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
        </svelte:element>
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

<!-- Species guide for a currently-heard bird. Live pings have no aggregate
     stats, so the stats grid is hidden (showStats={false}). -->
<SpeciesDetailModal
  species={guideSpecies}
  isOpen={guideModalOpen}
  showStats={false}
  onClose={closeGuide}
/>
