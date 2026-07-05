<script lang="ts">
  import { onMount } from 'svelte';
  import type { Component } from 'svelte';
  import {
    ChevronDown,
    ChevronRight,
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
  import ExternalLinkBadge from '$lib/desktop/components/ui/ExternalLinkBadge.svelte';

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
    /**
     * Heading shown in the panel header. Defaults to the species name. Parents
     * that already display the species name (e.g. the species detail modal, whose
     * title is the species name) pass a generic label so it isn't shown twice.
     */
    heading?: string;
    /**
     * Whether the similar-species section is enabled (the showSimilarSpecies
     * setting). The guide description and enrichments render regardless — they
     * are gated only by the guide feature itself. When the guide response
     * arrives, its server-computed features.similar_species flag takes over as
     * the authoritative gate.
     */
    showSimilarSpecies?: boolean;
    className?: string;
    /**
     * Whether the whole guide panel is collapsed. It collapses in place (header
     * stays visible) rather than closing to a separate reopen button, matching the
     * section toggles inside it. Bindable so a parent can reset it — the species
     * modal re-expands on each open so the description is shown — while the inner
     * section toggles keep their own state. Defaults to expanded; parents that key
     * this component on the species also get a fresh expanded panel per species.
     */
    collapsed?: boolean;
    [key: string]: unknown;
  }

  let {
    scientificName,
    commonName,
    heading,
    showSimilarSpecies = true,
    className = '',
    collapsed = $bindable(false),
  }: Props = $props();

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
  // Similar-species section gate: the server-computed per-response flag is
  // authoritative once the guide resolved; the prop covers the guide-404 case.
  let similarSectionOn = $derived(guide?.features?.similar_species ?? showSimilarSpecies);

  async function load(): Promise<void> {
    loading = true;
    error = null;
    unavailable = false;
    noGuide = false;
    const enc = encodeURIComponent(scientificName);
    const loc = encodeURIComponent(getLocale());
    // The two endpoints are independent on the backend; fetch them independently
    // so a guide 404 (species without guide content) doesn't discard a
    // successfully fetched similar-species list.
    const emptySimilar: SimilarSpeciesResponse = {
      scientific_name: scientificName,
      genus: '',
      similar: [],
    };
    const similarPromise = showSimilarSpecies
      ? api
          .get<SimilarSpeciesResponse>(`/api/v2/species/${enc}/similar?locale=${loc}`)
          .catch((e): SimilarSpeciesResponse => {
            logger.error('Failed to load similar species', e, { component: 'SpeciesComparison' });
            return emptySimilar;
          })
      : Promise.resolve(emptySimilar);
    try {
      guide = await api.get<SpeciesGuideData>(`/api/v2/species/${enc}/guide?locale=${loc}`);
    } catch (e) {
      if (e instanceof ApiError && e.status === HTTP_SERVICE_UNAVAILABLE) {
        unavailable = true;
      } else if (e instanceof ApiError && e.status === HTTP_NOT_FOUND) {
        // Expected when no guide exists for this species: show a soft empty state.
        noGuide = true;
      } else {
        error = e instanceof Error ? e.message : String(e);
      }
      logger.error('Failed to load species guide', e, { component: 'SpeciesComparison' });
    }
    similar = (await similarPromise).similar ?? [];
    loading = false;
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
  <header class="mb-3">
    <h2 class="text-lg font-semibold">
      <button
        type="button"
        class="flex w-full cursor-pointer items-center justify-between gap-2 text-left"
        aria-expanded={!collapsed}
        aria-controls={`${uid}-guide-body`}
        data-testid="species-comparison-toggle"
        onclick={() => (collapsed = !collapsed)}
      >
        <span>{heading ?? (commonName || scientificName)}</span>
        {#if collapsed}
          <ChevronRight class="h-5 w-5 shrink-0" aria-hidden="true" />
        {:else}
          <ChevronDown class="h-5 w-5 shrink-0" aria-hidden="true" />
        {/if}
      </button>
    </h2>
  </header>

  {#if !collapsed}
    <div id={`${uid}-guide-body`}>
      {#if loading}
        <div
          role="status"
          aria-live="polite"
          class="flex items-center gap-2 text-base-content/70 p-4"
        >
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
      {:else if error}
        <div role="alert" class="p-4 rounded-lg bg-error/10 text-error">{error}</div>
      {:else if noGuide}
        <!-- No guide content for this species, but the similar-species list is an
         independent endpoint: render it when it returned entries so a guide 404
         doesn't discard useful data. -->
        <div role="status" class="p-4 text-sm text-base-content/70">
          {t('analytics.species.guide.noGuide')}
        </div>
        {#if similarSectionOn && similar.length > 0}
          <div>
            <button
              type="button"
              class="flex w-full cursor-pointer items-center justify-between py-2 text-left font-medium"
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
        {/if}
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
                <ExternalLinkBadge {link} />
              {/each}
            {/if}
          </div>
        {/if}

        <!-- Description -->
        {#if descriptionBody}
          <div class="border-b border-base-300">
            <button
              type="button"
              class="flex w-full cursor-pointer items-center justify-between py-2 text-left font-medium"
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
              class="flex w-full cursor-pointer items-center justify-between py-2 text-left font-medium"
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

        <!-- Similar species (gated by the showSimilarSpecies setting, with the
         guide response's server-computed features flag as the authority) -->
        {#if similarSectionOn}
          <div>
            <button
              type="button"
              class="flex w-full cursor-pointer items-center justify-between py-2 text-left font-medium"
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
        {/if}
      {:else}
        <p class="text-sm text-base-content/70 p-4">{t('analytics.species.guide.noSimilar')}</p>
      {/if}
    </div>
  {/if}
</section>
