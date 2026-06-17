<script lang="ts">
  import { onMount } from 'svelte';
  import { X, ChevronDown, ChevronRight } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { api, ApiError } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import {
    parseGuideDescription,
    type SpeciesGuideData,
    type SimilarSpeciesResponse,
    type SimilarSpeciesEntry,
  } from '$lib/types/species';

  const logger = loggers.ui;

  // 503: surfaced when the guide feature is enabled but the cache is unavailable.
  const HTTP_SERVICE_UNAVAILABLE = 503;

  interface Props {
    scientificName: string;
    commonName: string;
    onclose: () => void;
    className?: string;
    [key: string]: unknown;
  }

  let { scientificName, commonName, onclose, className = '' }: Props = $props();

  // Instance-scoped id prefix so two instances on one page don't collide on
  // aria-controls (DetectionDetail + an open modal).
  const uid = $props.id();

  let guide = $state<SpeciesGuideData | null>(null);
  let similar = $state<SimilarSpeciesEntry[]>([]);
  let loading = $state(true);
  let unavailable = $state(false);
  let error = $state<string | null>(null);

  let openSections = $state<Record<string, boolean>>({
    description: true,
    songs: false,
    similar: true,
  });

  // Localized Wikipedia heading fragments mapped to canonical section ids.
  const SONGS_HEADINGS = [
    'songs and calls',
    'song',
    'calls',
    'voice',
    'stimme',
    'chant et cris',
    'voix',
    'voz',
    'canto',
    'głos',
    'ääntelyt',
    'läte',
  ];

  function classifyHeading(heading: string): 'description' | 'songs' | 'other' {
    const h = heading.trim().toLowerCase();
    if (h === '') return 'description';
    if (SONGS_HEADINGS.some(token => h.includes(token))) return 'songs';
    return 'other';
  }

  let sections = $derived(guide ? parseGuideDescription(guide.description) : []);

  let descriptionBody = $derived.by(() => {
    const intro = sections.find(s => classifyHeading(s.heading) === 'description');
    return intro?.body ?? '';
  });

  let songsBody = $derived.by(() => {
    const songs = sections.find(s => classifyHeading(s.heading) === 'songs');
    return songs?.body ?? '';
  });

  async function load(): Promise<void> {
    loading = true;
    error = null;
    unavailable = false;
    const enc = encodeURIComponent(scientificName);
    try {
      const [g, s] = await Promise.all([
        api.get<SpeciesGuideData>(`/api/v2/species/${enc}/guide`),
        api
          .get<SimilarSpeciesResponse>(`/api/v2/species/${enc}/similar`)
          .catch(
            (): SimilarSpeciesResponse => ({
              scientific_name: scientificName,
              genus: '',
              similar: [],
            })
          ),
      ]);
      guide = g;
      similar = s.similar ?? [];
    } catch (e) {
      if (e instanceof ApiError && e.status === HTTP_SERVICE_UNAVAILABLE) {
        unavailable = true;
      } else {
        error = e instanceof Error ? e.message : String(e);
      }
      logger.error('Failed to load species comparison', e, { component: 'SpeciesComparison' });
    } finally {
      loading = false;
    }
  }

  function toggle(id: string): void {
    openSections[id] = !openSections[id];
  }

  onMount(load);
</script>

<section
  class={`species-comparison ${className}`}
  aria-label={t('analytics.species.similar.title')}
>
  <header class="flex items-center justify-between gap-2 mb-3">
    <h2 class="text-lg font-semibold">{commonName || scientificName}</h2>
    <button
      type="button"
      class="btn btn-ghost btn-sm btn-circle"
      aria-label={t('common.close')}
      data-testid="species-comparison-close"
      onclick={onclose}
    >
      <X class="h-4 w-4" />
    </button>
  </header>

  {#if loading}
    <div role="status" aria-live="polite" class="flex items-center gap-2 text-base-content/70 p-4">
      <span
        class="animate-spin h-5 w-5 border-2 border-primary border-t-transparent rounded-full"
        aria-hidden="true"
      ></span>
      <span>{t('analytics.species.guide.loading')}</span>
    </div>
  {:else if unavailable}
    <div role="alert" class="p-4 rounded-lg bg-warning/10 text-warning-content">
      {t('analytics.species.guide.loading')}
    </div>
  {:else if error}
    <div role="alert" class="p-4 rounded-lg bg-error/10 text-error">{error}</div>
  {:else if guide}
    <!-- Description -->
    {#if descriptionBody}
      <div class="border-b border-base-300">
        <button
          type="button"
          class="flex w-full items-center justify-between py-2 text-left font-medium"
          aria-expanded={openSections.description}
          aria-controls={`${uid}-description`}
          onclick={() => toggle('description')}
        >
          <span>{t('analytics.species.guide.description')}</span>
          {#if openSections.description}
            <ChevronDown class="h-4 w-4" />
          {:else}
            <ChevronRight class="h-4 w-4" />
          {/if}
        </button>
        {#if openSections.description}
          <div id={`${uid}-description`} class="pb-3 text-sm whitespace-pre-line">
            {descriptionBody}
          </div>
        {/if}
      </div>
    {/if}

    <!-- Songs & Calls -->
    {#if songsBody}
      <div class="border-b border-base-300">
        <button
          type="button"
          class="flex w-full items-center justify-between py-2 text-left font-medium"
          aria-expanded={openSections.songs}
          aria-controls={`${uid}-songs`}
          onclick={() => toggle('songs')}
        >
          <span>{t('analytics.species.guide.songsAndCalls')}</span>
          {#if openSections.songs}
            <ChevronDown class="h-4 w-4" />
          {:else}
            <ChevronRight class="h-4 w-4" />
          {/if}
        </button>
        {#if openSections.songs}
          <div id={`${uid}-songs`} class="pb-3 text-sm whitespace-pre-line">{songsBody}</div>
        {/if}
      </div>
    {/if}

    <!-- Similar species -->
    <div>
      <button
        type="button"
        class="flex w-full items-center justify-between py-2 text-left font-medium"
        aria-expanded={openSections.similar}
        aria-controls={`${uid}-similar`}
        onclick={() => toggle('similar')}
      >
        <span>{t('analytics.species.similar.title')}</span>
        {#if openSections.similar}
          <ChevronDown class="h-4 w-4" />
        {:else}
          <ChevronRight class="h-4 w-4" />
        {/if}
      </button>
      {#if openSections.similar}
        <div id={`${uid}-similar`} class="pb-3">
          {#if similar.length === 0}
            <p class="text-sm text-base-content/70">{t('analytics.species.similar.empty')}</p>
          {:else}
            <ul class="space-y-2">
              {#each similar as entry (entry.scientific_name)}
                <li class="rounded-md border border-base-300 p-2">
                  <div class="flex items-baseline justify-between gap-2">
                    <span class="font-medium">{entry.common_name || entry.scientific_name}</span>
                    <span class="text-xs text-base-content/60 italic">{entry.scientific_name}</span>
                  </div>
                  {#if entry.guide_summary}
                    <p class="mt-1 text-sm text-base-content/80">{entry.guide_summary}</p>
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {/if}
    </div>
  {:else}
    <p class="text-sm text-base-content/70 p-4">{t('analytics.species.guide.noSimilar')}</p>
  {/if}
</section>
