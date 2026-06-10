<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import { formatDate } from '$lib/utils/formatters';
  import { Binoculars } from '@lucide/svelte';

  interface SpeciesData {
    common_name: string;
    scientific_name: string;
    count: number;
    avg_confidence: number;
    max_confidence: number;
    first_heard: string;
    last_heard: string;
    thumbnail_url?: string;
  }

  interface Props {
    species: SpeciesData;
    className?: string;
    /** eBird species-page URL; when set, a link icon is shown next to the name. */
    ebirdUrl?: string | null;
  }

  let { species, className = '', ebirdUrl = null }: Props = $props();

  function formatPercentage(value: number): string {
    return (value * 100).toFixed(1) + '%';
  }

  let imageLoadFailed = $state(false);

  function handleImageError() {
    imageLoadFailed = true;
  }
</script>

<div class={cn('card bg-[var(--color-base-200)]', className)}>
  <figure class="px-4 pt-4">
    <div class="rounded-xl w-full aspect-[4/3] overflow-hidden bg-[var(--color-base-300)]">
      {#if species.thumbnail_url && !imageLoadFailed}
        <img
          src={species.thumbnail_url}
          alt={species.common_name}
          class="h-full w-full object-cover"
          onerror={handleImageError}
        />
      {/if}
    </div>
  </figure>
  <div class="card-body p-4">
    <div class="flex items-center gap-1.5">
      <h3 class="card-title text-base">{species.common_name}</h3>
      {#if ebirdUrl}
        <a
          href={ebirdUrl}
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex shrink-0 items-center justify-center rounded-md p-1 text-[var(--color-base-content)] opacity-50 transition-colors hover:bg-[var(--color-base-300)] hover:text-[var(--color-primary)] hover:opacity-100"
          title={t('analytics.species.viewOnEbird')}
          aria-label={t('analytics.species.viewOnEbirdAria', { species: species.common_name })}
        >
          <Binoculars class="h-4 w-4" />
        </a>
      {/if}
    </div>
    <p class="text-sm text-[var(--color-base-content)] opacity-60 italic">
      {species.scientific_name}
    </p>
    <div class="text-sm space-y-1 mt-2">
      <div class="flex justify-between">
        <span class="text-[var(--color-base-content)] opacity-60"
          >{t('analytics.species.card.detections')}</span
        >
        <span class="font-semibold">{species.count}</span>
      </div>
      <div class="flex justify-between">
        <span class="text-[var(--color-base-content)] opacity-60"
          >{t('analytics.species.card.confidence')}</span
        >
        <span class="font-semibold">{formatPercentage(species.avg_confidence)}</span>
      </div>
      {#if species.first_heard}
        <div class="flex justify-between">
          <span class="text-[var(--color-base-content)] opacity-60"
            >{t('analytics.species.card.first')}</span
          >
          <span class="text-xs">{formatDate(species.first_heard)}</span>
        </div>
      {/if}
    </div>
  </div>
</div>
