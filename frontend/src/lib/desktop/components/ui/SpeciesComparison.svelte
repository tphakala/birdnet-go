<script lang="ts">
  import { onMount } from 'svelte';
  import type { Component } from 'svelte';
  import {
    X,
    ChevronDown,
    ChevronRight,
    ExternalLink,
    Sprout,
    Sun,
    Leaf,
    Snowflake,
    CloudRain,
    SunDim,
  } from '@lucide/svelte';
  import { t, getLocale } from '$lib/i18n';
  import { api, ApiError } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { getSeasonHighlight, type SeasonIcon } from '$lib/utils/seasonHighlight';

  // Maps the season's stable icon id to its lucide component. A Map (not a plain
  // object) avoids indexing by a dynamic key and keeps the season badge on the
  // same icon system as the rest of the UI.
  const SEASON_ICON_COMPONENT = new Map<SeasonIcon, Component>([
    ['sprout', Sprout],
    ['sun', Sun],
    ['leaf', Leaf],
    ['snowflake', Snowflake],
    ['cloud-rain', CloudRain],
    ['sun-dim', SunDim],
  ]);
  import {
    parseGuideDescription,
    GUIDE_SONGS_HEADINGS,
    type SpeciesGuideData,
    type SimilarSpeciesResponse,
    type SimilarSpeciesEntry,
  } from '$lib/types/species';
  import SimilarSpeciesPanel from './SimilarSpeciesPanel.svelte';

  const logger = loggers.ui;

  // 503: surfaced when the guide feature is enabled but the cache is unavailable.
  const HTTP_SERVICE_UNAVAILABLE = 503;
  // 404: a species with no guide content (e.g. obscure species, or non-bird
  // labels like "Noise"/"Engine"). This is an expected, benign case, so it gets a
  // soft "no guide" message rather than the alarming red error alert.
  const HTTP_NOT_FOUND = 404;

  interface Props {
    scientificName: string;
    commonName: string;
    onclose: () => void;
    /**
     * Heading shown in the panel header. Defaults to the species name. Parents
     * that already display the species name (e.g. the species detail modal, whose
     * title is the species name) pass a generic label so it isn't shown twice.
     */
    heading?: string;
    className?: string;
    [key: string]: unknown;
  }

  let { scientificName, commonName, onclose, heading, className = '' }: Props = $props();

  // Instance-scoped id prefix so two instances on one page don't collide on
  // aria-controls (DetectionDetail + an open modal).
  const uid = $props.id();

  let guide = $state<SpeciesGuideData | null>(null);
  let similar = $state<SimilarSpeciesEntry[]>([]);
  let loading = $state(true);
  let unavailable = $state(false);
  let noGuide = $state(false);
  let error = $state<string | null>(null);

  let openSections = $state<Record<string, boolean>>({
    description: true,
    songs: false,
    similar: true,
  });

  function classifyHeading(heading: string): 'description' | 'songs' | 'other' {
    const h = heading.trim().toLowerCase();
    if (h === '') return 'description';
    if (GUIDE_SONGS_HEADINGS.some(token => h.includes(token))) return 'songs';
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

  // Enrichments (expectedness, season, external links) are shown only when the
  // guide's enrichments feature flag is on (driven by the showEnrichments setting).
  let enrichmentsOn = $derived(guide?.features?.enrichments ?? false);
  let season = $derived(guide ? getSeasonHighlight(guide.current_season) : null);
  let externalLinks = $derived(guide?.external_links ?? []);

  async function load(): Promise<void> {
    loading = true;
    error = null;
    unavailable = false;
    noGuide = false;
    const enc = encodeURIComponent(scientificName);
    const loc = encodeURIComponent(getLocale());
    try {
      const [g, s] = await Promise.all([
        api.get<SpeciesGuideData>(`/api/v2/species/${enc}/guide?locale=${loc}`),
        api.get<SimilarSpeciesResponse>(`/api/v2/species/${enc}/similar?locale=${loc}`).catch(
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
      } else if (e instanceof ApiError && e.status === HTTP_NOT_FOUND) {
        // Expected when no guide exists for this species: show a soft empty state.
        noGuide = true;
      } else {
        error = e instanceof Error ? e.message : String(e);
      }
      logger.error('Failed to load species comparison', e, { component: 'SpeciesComparison' });
    } finally {
      loading = false;
    }
  }

  function toggle(id: string): void {
    // eslint-disable-next-line security/detect-object-injection -- id is a fixed internal section key (description/songs/similar), not external input
    openSections[id] = !openSections[id];
  }

  onMount(load);
</script>

<section
  class={`species-comparison ${className}`}
  aria-label={t('analytics.species.similar.title')}
>
  <header class="flex items-center justify-between gap-2 mb-3">
    <h2 class="text-lg font-semibold">{heading ?? (commonName || scientificName)}</h2>
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
      {t('analytics.species.guide.unavailable')}
    </div>
  {:else if noGuide}
    <div role="status" class="p-4 text-sm text-base-content/70">
      {t('analytics.species.guide.noGuide')}
    </div>
  {:else if error}
    <div role="alert" class="p-4 rounded-lg bg-error/10 text-error">{error}</div>
  {:else if guide}
    <!-- Enrichments: expectedness + season badges and external resource links -->
    {#if enrichmentsOn && (guide.expectedness || season || externalLinks.length > 0)}
      <div class="mb-3 flex flex-wrap items-center gap-2" data-testid="guide-enrichments">
        {#if guide.expectedness}
          <span class="badge badge-sm badge-outline">
            {t(`analytics.species.guide.expectedness.${guide.expectedness}`)}
          </span>
        {/if}
        {#if season}
          {@const SeasonIcon = season.icon ? SEASON_ICON_COMPONENT.get(season.icon) : undefined}
          <span class="badge badge-sm badge-outline gap-1">
            {#if SeasonIcon}<SeasonIcon class="h-3 w-3" aria-hidden="true" />{/if}
            {t(season.i18nKey)}
          </span>
        {/if}
        {#if externalLinks.length > 0}
          <span class="sr-only">{t('analytics.species.guide.externalLinks')}</span>
          {#each externalLinks as link (link.url)}
            <a
              href={link.url}
              target="_blank"
              rel="noopener noreferrer"
              class="badge badge-sm badge-ghost gap-1"
            >
              {link.name}
              <ExternalLink class="h-3 w-3" aria-hidden="true" />
            </a>
          {/each}
        {/if}
      </div>
    {/if}

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
          <SimilarSpeciesPanel mainName={commonName || scientificName} {similar} />
        </div>
      {/if}
    </div>
  {:else}
    <p class="text-sm text-base-content/70 p-4">{t('analytics.species.guide.noSimilar')}</p>
  {/if}
</section>
