<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import { parseLocalDateString } from '$lib/utils/date';

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

  function formatDate(dateString: string): string {
    if (!dateString) return '';
    const date = parseLocalDateString(dateString);
    if (!date) return '';
    return date.toLocaleDateString();
  }

  function handleImageError(event: Event) {
    const target = event.target as globalThis.HTMLImageElement;
    target.style.display = 'none';
    if (target.parentElement) {
      target.parentElement.innerHTML = '<div class="rounded-xl h-40 w-full bg-base-300"></div>';
    }
  }
</script>

<div class={cn('card bg-base-200', className)}>
  <figure class="px-4 pt-4">
    {#if species.thumbnail_url}
      <img
        src={species.thumbnail_url}
        alt={species.common_name}
        class="rounded-xl h-40 w-full object-cover"
        onerror={handleImageError}
      />
    {:else}
      <div class="rounded-xl h-40 w-full bg-base-300"></div>
    {/if}
  </figure>
  <div class="card-body p-4">
    <h3 class="card-title text-base">{species.common_name}</h3>
    <p class="text-sm text-base-content/60 italic">{species.scientific_name}</p>
    <div class="text-sm space-y-1 mt-2">
      <div class="flex justify-between">
        <span class="text-base-content/60">{t('analytics.species.card.detections')}</span>
        <span class="font-semibold">{species.count}</span>
      </div>
      <div class="flex justify-between">
        <span class="text-base-content/60">{t('analytics.species.card.confidence')}</span>
        <span class="font-semibold">{formatPercentage(species.avg_confidence)}</span>
      </div>
      {#if species.first_heard}
        <div class="flex justify-between">
          <span class="text-base-content/60">{t('analytics.species.card.first')}</span>
          <span class="text-xs">{formatDate(species.first_heard)}</span>
        </div>
      {/if}
    </div>
  </div>
</div>
