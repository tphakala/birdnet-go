<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { t, getLocale } from '$lib/i18n';
  import { formatDate } from '$lib/utils/formatters';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { getAllAboutBirdsUrl, getWikipediaUrl } from '$lib/utils/speciesLinks';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils';
  import { ExternalLink } from '@lucide/svelte';

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
  }

  let { species, className = '' }: Props = $props();

  function formatPercentage(value: number): string {
    return (value * 100).toFixed(1) + '%';
  }

  let displayName = $derived(localizeSpeciesName(species.scientific_name, species.common_name));
  let wikipediaUrl = $derived(
    getWikipediaUrl(displayName, getLocale(), species.common_name)
  );
</script>

<div class={cn('card bg-[var(--color-base-200)]', className)}>
  <figure class="px-4 pt-4">
    <div class="rounded-xl w-full aspect-[4/3] overflow-hidden bg-[var(--color-base-300)]">
      {#if species.thumbnail_url}
        <img
          src={species.thumbnail_url}
          alt={displayName}
          class="h-full w-full object-cover"
          onerror={handleBirdImageError}
        />
      {/if}
    </div>
  </figure>
  <div class="card-body p-4">
    <div class="flex items-center gap-2 min-w-0">
      <h3 class="card-title text-base min-w-0 flex-1 truncate">{displayName}</h3>
      <div class="flex shrink-0 items-center gap-1">
        <a
          href={getAllAboutBirdsUrl(species.common_name)}
          target="_blank"
          rel="noopener noreferrer"
          class="btn btn-ghost btn-sm btn-square"
          aria-label={`${t('analytics.species.openAllAboutBirds')}: ${displayName}`}
          title={t('analytics.species.openAllAboutBirds')}
        >
          <ExternalLink class="size-4" />
        </a>
        <a
          href={wikipediaUrl}
          target="_blank"
          rel="noopener noreferrer"
          class="btn btn-ghost btn-sm btn-square"
          aria-label={`${t('analytics.species.openWikipedia')}: ${displayName}`}
          title={t('analytics.species.openWikipedia')}
        >
          <span class="text-xs font-serif font-bold">W</span>
        </a>
      </div>
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
