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
    onClick?: (_species: SpeciesData) => void;
    variant?: 'card' | 'compact' | 'list';
    className?: string;
  }

  let { species, onClick, variant = 'card', className = '' }: Props = $props();

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
      target.parentElement.classList.add('bg-base-300');
    }
  }

  function handleClick() {
    if (onClick) {
      onClick(species);
    }
  }
</script>

{#if variant === 'card'}
  <!-- Full Card Variant - Desktop/Tablet -->
  <button
    onclick={handleClick}
    class={cn(
      'card bg-base-200 hover:shadow-lg transition-shadow cursor-pointer text-left',
      className
    )}
  >
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
      <p class="text-sm text-base-content opacity-60 italic">{species.scientific_name}</p>
      <div class="text-sm space-y-1 mt-2">
        <div class="flex justify-between">
          <span class="text-base-content opacity-60">{t('analytics.species.card.detections')}</span>
          <span class="font-semibold">{species.count}</span>
        </div>
        <div class="flex justify-between">
          <span class="text-base-content opacity-60">{t('analytics.species.card.confidence')}</span>
          <span class="font-semibold">{formatPercentage(species.avg_confidence)}</span>
        </div>
        {#if species.first_heard}
          <div class="flex justify-between">
            <span class="text-base-content opacity-60">{t('analytics.species.card.first')}</span>
            <span class="text-xs">{formatDate(species.first_heard)}</span>
          </div>
        {/if}
      </div>
    </div>
  </button>
{:else if variant === 'compact'}
  <!-- Compact Mobile Variant -->
  <button
    onclick={handleClick}
    class={cn(
      'flex gap-3 p-3 bg-base-200 hover:bg-base-300 rounded-lg transition-colors cursor-pointer w-full text-left',
      className
    )}
  >
    <div class="flex-shrink-0">
      <div class="avatar w-16 h-16">
        <div class="mask mask-squircle bg-base-300">
          {#if species.thumbnail_url}
            <img
              src={species.thumbnail_url}
              alt={species.common_name}
              class="object-cover"
              onerror={handleImageError}
            />
          {/if}
        </div>
      </div>
    </div>
    <div class="flex-1 min-w-0">
      <h3 class="font-bold text-sm truncate">{species.common_name}</h3>
      <p class="text-xs text-base-content opacity-60 italic truncate">{species.scientific_name}</p>
      <div class="flex gap-2 mt-1 text-xs">
        <div class="flex items-center gap-1 bg-base-100 rounded px-2 py-1">
          <span class="font-semibold">{species.count}</span>
          <span class="opacity-60">{t('analytics.species.card.detections')}</span>
        </div>
        <div
          class="flex items-center gap-1 rounded px-2 py-1 {species.avg_confidence >= 0.8
            ? 'bg-success/20'
            : species.avg_confidence >= 0.4
              ? 'bg-warning/20'
              : 'bg-error/20'}"
        >
          <span class="font-semibold">{formatPercentage(species.avg_confidence)}</span>
        </div>
      </div>
    </div>
    <div class="flex-shrink-0 flex items-center">
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="h-5 w-5 text-base-content opacity-50"
        viewBox="0 0 20 20"
        fill="currentColor"
      >
        <path
          fill-rule="evenodd"
          d="M7.293 14.707a1 1 0 010-1.414L10.586 10 7.293 6.707a1 1 0 011.414-1.414l4 4a1 1 0 010 1.414l-4 4a1 1 0 01-1.414 0z"
          clip-rule="evenodd"
        />
      </svg>
    </div>
  </button>
{:else if variant === 'list'}
  <!-- List Row Variant -->
  <button
    onclick={handleClick}
    class={cn(
      'flex items-center gap-3 w-full p-3 hover:bg-base-200 transition-colors cursor-pointer text-left border-b border-base-300 last:border-b-0',
      className
    )}
  >
    <div class="avatar flex-shrink-0">
      <div class="mask mask-squircle w-12 h-12 bg-base-300">
        {#if species.thumbnail_url}
          <img
            src={species.thumbnail_url}
            alt={species.common_name}
            class="object-cover"
            onerror={handleImageError}
          />
        {/if}
      </div>
    </div>
    <div class="flex-1 min-w-0">
      <h4 class="font-bold text-sm truncate">{species.common_name}</h4>
      <p class="text-xs text-base-content opacity-60 truncate">{species.scientific_name}</p>
    </div>
    <div class="text-right flex-shrink-0">
      <p class="text-sm font-semibold">{species.count}</p>
      <p class="text-xs text-base-content opacity-60">{formatPercentage(species.avg_confidence)}</p>
    </div>
  </button>
{/if}
