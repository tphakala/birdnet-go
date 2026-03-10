<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import { parseLocalDateString } from '$lib/utils/date';
  import { ChevronRight } from '@lucide/svelte';

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

  let imageLoadFailed = $state(false);

  function handleImageError() {
    imageLoadFailed = true;
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
      'card bg-[var(--color-base-200)] hover:shadow-lg transition-shadow cursor-pointer text-left',
      className
    )}
  >
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
      <h3 class="card-title text-base">{species.common_name}</h3>
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
  </button>
{:else if variant === 'compact'}
  <!-- Compact Mobile Variant -->
  <button
    onclick={handleClick}
    class={cn(
      'flex gap-3 p-3 bg-[var(--color-base-200)] hover:bg-[var(--color-base-300)] rounded-lg transition-colors cursor-pointer w-full text-left',
      className
    )}
  >
    <div class="flex-shrink-0">
      <div class="avatar w-16 h-16">
        <div class="mask mask-squircle bg-[var(--color-base-300)]">
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
      <p class="text-xs text-[var(--color-base-content)] opacity-60 italic truncate">
        {species.scientific_name}
      </p>
      <div class="flex gap-2 mt-1 text-xs">
        <div class="flex items-center gap-1 bg-[var(--color-base-100)] rounded px-2 py-1">
          <span class="font-semibold">{species.count}</span>
          <span class="opacity-60">{t('analytics.species.card.detections')}</span>
        </div>
        <div
          class="flex items-center gap-1 rounded px-2 py-1 {species.avg_confidence >= 0.8
            ? 'bg-[var(--color-success)]/20'
            : species.avg_confidence >= 0.4
              ? 'bg-[var(--color-warning)]/20'
              : 'bg-[var(--color-error)]/20'}"
        >
          <span class="font-semibold">{formatPercentage(species.avg_confidence)}</span>
        </div>
      </div>
    </div>
    <div class="flex-shrink-0 flex items-center">
      <ChevronRight class="size-5 text-[var(--color-base-content)] opacity-50" />
    </div>
  </button>
{:else if variant === 'list'}
  <!-- List Row Variant -->
  <button
    onclick={handleClick}
    class={cn(
      'flex items-center gap-3 w-full p-3 hover:bg-[var(--color-base-200)] transition-colors cursor-pointer text-left border-b border-[var(--color-base-300)] last:border-b-0',
      className
    )}
  >
    <div class="avatar flex-shrink-0">
      <div class="mask mask-squircle w-12 h-12 bg-[var(--color-base-300)]">
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
      <p class="text-xs text-[var(--color-base-content)] opacity-60 truncate">
        {species.scientific_name}
      </p>
    </div>
    <div class="text-right flex-shrink-0">
      <p class="text-sm font-semibold">{species.count}</p>
      <p class="text-xs text-[var(--color-base-content)] opacity-60">
        {formatPercentage(species.avg_confidence)}
      </p>
    </div>
  </button>
{/if}
