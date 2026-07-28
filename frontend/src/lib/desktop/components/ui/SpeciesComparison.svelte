<script lang="ts">
  import type { Component } from 'svelte';
  import { SvelteSet } from 'svelte/reactivity';
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
    extractCanonicalSections,
    type CanonicalSectionId,
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

  // Descriptions longer than this (characters) are clamped with a "read more"
  // toggle so a very long guide entry doesn't dominate the panel as one wall of
  // text; shorter descriptions render in full with no ambiguous toggle. Mirrors
  // SimilarSpeciesPanel's character-length heuristic (jsdom has no layout).
  const DESC_CLAMP_CHARS = 800;

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
  // Whether the similar-species list was actually requested for the current
  // species. Distinguishes "we asked and there are none" from "we never asked".
  let similarRequested = $state(false);
  let error = $state<string | null>(null);

  // Description (the article lead) is always shown, so it isn't tracked here — only
  // the canonical body sections and similar species toggle. A SvelteSet keeps the
  // per-section state keyed without indexing a plain object by a dynamic key,
  // mirroring SimilarSpeciesPanel's expandedRows. Sections start collapsed.
  const openBodySections = new SvelteSet<CanonicalSectionId>();
  let similarOpen = $state(true);

  // Canonical comparison rows in display order. Voice keeps the guide's own
  // "Songs & Calls" label (already translated in every locale); the rest reuse the
  // comparison-row labels the similar-species panel uses, so no new i18n keys.
  const SECTION_ROWS: { id: CanonicalSectionId; labelKey: string }[] = [
    { id: 'appearance', labelKey: 'analytics.species.similar.sections.appearance' },
    { id: 'voice', labelKey: 'analytics.species.guide.songsAndCalls' },
    { id: 'habitat', labelKey: 'analytics.species.similar.sections.habitat' },
    { id: 'behaviour', labelKey: 'analytics.species.similar.sections.behaviour' },
  ];

  // Whether a long description is expanded past its clamp. Parents key this
  // component on the species, so it remounts (and resets to collapsed) per species.
  let descExpanded = $state(false);

  let sections = $derived(guide ? parseGuideDescription(guide.description) : []);

  // The article lead: the segment before the first `## ` header.
  let descriptionBody = $derived(sections.find(s => s.heading === '')?.body ?? '');

  // The remaining prose, grouped into the canonical comparison categories.
  //
  // Without this the panel rendered only the lead and a Songs section, and every other
  // `## ` section the backend produced — Description, Distribution and habitat,
  // Behaviour, and the sub-sections convertWikiSections deliberately promotes to top
  // level — was parsed, shipped over the wire, and then silently dropped.
  //
  // extractCanonicalSections falls back to the lead for `appearance` when the article
  // has no description-like section, so that case is filtered out here rather than
  // repeating the lead under a second heading.
  let canonicalRows = $derived.by(() => {
    if (!guide) return [];
    const canonical = extractCanonicalSections(guide.description);
    return SECTION_ROWS.map(row => ({ ...row, body: canonical[row.id].trim() })).filter(
      row => row.body !== '' && row.body !== descriptionBody.trim()
    );
  });

  // Enrichments (expectedness, season, external links) are shown only when the
  // guide's enrichments feature flag is on (driven by the showEnrichments setting).
  let enrichmentsOn = $derived(guide?.features?.enrichments ?? false);
  let season = $derived(guide ? getSeasonHighlight(guide.current_season) : null);
  let externalLinks = $derived(guide?.external_links ?? []);
  // Similar-species section gate: the server-computed per-response flag is
  // authoritative once the guide resolved; the prop covers the guide-404 case.
  let similarSectionOn = $derived(guide?.features?.similar_species ?? showSimilarSpecies);

  // Identifies the in-flight load. A late response for a species the user has
  // already navigated away from must not overwrite the current one, and the
  // component must re-fetch when its prop changes rather than relying on every
  // caller remembering to wrap it in {#key}.
  let loadToken = 0;

  async function load(): Promise<void> {
    const token = ++loadToken;
    loading = true;
    error = null;
    unavailable = false;
    noGuide = false;
    similarRequested = showSimilarSpecies;
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
      const fetched = await api.get<SpeciesGuideData>(`/api/v2/species/${enc}/guide?locale=${loc}`);
      if (token !== loadToken) return; // superseded by a newer species
      guide = fetched;
    } catch (e) {
      if (token !== loadToken) return;
      if (e instanceof ApiError && e.status === HTTP_SERVICE_UNAVAILABLE) {
        unavailable = true;
      } else if (e instanceof ApiError && e.status === HTTP_NOT_FOUND) {
        // Expected when no guide exists for this species: show a soft empty state.
        noGuide = true;
      } else {
        error = e instanceof Error ? e.message : String(e);
      }
      logger.error('Failed to load species guide', e, { component: 'SpeciesComparison' });
    } finally {
      // In a finally so an unexpected throw below cannot strand the spinner. The
      // similar-species await used to sit outside any guard, so a null body (a 204
      // or a zero-length response, which api.get surfaces as null) threw on the
      // property access and left `loading` true for the life of the component.
      if (token === loadToken) loading = false;
    }

    const similarResult = await similarPromise;
    if (token !== loadToken) return;
    similar = similarResult?.similar ?? [];
    // The backend reports a degraded rail explicitly, so an unavailable cache is
    // distinguishable from "these species genuinely have no guides".
    if (similarResult?.guide_unavailable) unavailable = true;
  }

  function toggleBodySection(id: CanonicalSectionId): void {
    if (openBodySections.has(id)) openBodySections.delete(id);
    else openBodySections.add(id);
  }

  // Re-load whenever the species (or the similar-species gate) changes, not just at
  // mount. Loading only in onMount meant a parent that reused the instance for a
  // different species kept showing the previous one's guide, links and comparison
  // rows under the new header, with no loading state to signal the mismatch — the
  // props contract could not enforce the {#key} wrapper every caller had to
  // remember. load() reads the current values, and its token guard drops any
  // response that a newer request has superseded.
  $effect(() => {
    void scientificName;
    void showSimilarSpecies;
    void load();
  });
</script>

<!--
  The similar-species disclosure, rendered from both the guide-present and the
  guide-404 branches, which previously carried byte-identical copies of it.

  `requireEntries` preserves the one deliberate difference between those copies:
  the guide-404 branch shows the section only when there is something in it (an
  empty section beside "no guide for this species" is just noise), while the
  guide-present branch shows it regardless so SimilarSpeciesPanel can render its
  own "no similar species" empty state.

  Both are additionally gated on `similarRequested`. Without it, a client whose
  showSimilarSpecies prop said "off" (so the fetch was skipped) while the server's
  features flag said "on" rendered the section over a list that was never
  requested — and the panel then told the user there were no similar species,
  which is a wrong answer rather than an empty one.

  A snippet, not the shared CollapsibleSection: that component derives its content
  id by slugifying its title, which collides when two instances of this panel are
  on one page (DetectionDetail plus an open modal). The instance-scoped `uid` here
  is what keeps aria-controls unambiguous.
-->
{#snippet similarSection(requireEntries: boolean)}
  {#if similarSectionOn && similarRequested && (!requireEntries || similar.length > 0)}
    <div>
      <button
        type="button"
        class="flex w-full cursor-pointer items-center justify-between py-2 text-left font-medium"
        aria-expanded={similarOpen}
        aria-controls={`${uid}-similar`}
        onclick={() => (similarOpen = !similarOpen)}
      >
        <span>{t('analytics.species.similar.title')}</span>
        {#if similarOpen}
          <ChevronDown class="h-4 w-4" />
        {:else}
          <ChevronRight class="h-4 w-4" />
        {/if}
      </button>
      {#if similarOpen}
        <div id={`${uid}-similar`} class="pb-3">
          <SimilarSpeciesPanel mainName={commonName || scientificName} {similar} />
        </div>
      {/if}
    </div>
  {/if}
{/snippet}

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
        {@render similarSection(true)}
      {:else if guide}
        <!-- Enrichments: expectedness + season badges and external resource links -->
        {#if enrichmentsOn && (guide.expectedness || season || externalLinks.length > 0)}
          <div class="mb-3 flex flex-wrap items-center gap-2" data-testid="guide-enrichments">
            {#if guide.expectedness}
              <!-- Filled tints instead of thin outlines: expectedness and season are
                   the guide's most glanceable facts, so they get prominence rather
                   than being the quietest thing in the panel. -->
              <span class="badge badge-sm border-0 bg-primary/10 text-primary font-medium">
                {t(`analytics.species.guide.expectedness.${guide.expectedness}`)}
              </span>
            {/if}
            {#if season}
              {@const SeasonIcon = season.icon ? SEASON_ICON_COMPONENT.get(season.icon) : undefined}
              <!-- Dark base-content text (not warning-content, which is white and
                   meant for a solid amber fill) so the label stays legible on the
                   pale amber tint in light mode; it flips to light in dark mode. -->
              <span
                class="badge badge-sm border-0 gap-1 bg-warning/15 text-[var(--color-base-content)] font-medium"
              >
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

        <!-- Description: the primary reason the guide is opened, so it stays visible
             rather than hidden behind a toggle. Songs and similar species below
             remain collapsible because they can run long. -->
        {#if descriptionBody}
          {@const descClampable = descriptionBody.length > DESC_CLAMP_CHARS}
          <div class="border-b border-base-300 pb-3">
            <h3 class="py-2 text-sm font-medium">{t('analytics.species.guide.description')}</h3>
            <div
              id={`${uid}-description`}
              class={`text-base leading-relaxed whitespace-pre-line${descClampable && !descExpanded ? ' line-clamp-[10]' : ''}`}
            >
              {descriptionBody}
            </div>
            {#if descClampable}
              <button
                type="button"
                class="mt-1 text-xs font-medium text-primary hover:underline"
                aria-expanded={descExpanded}
                aria-controls={`${uid}-description`}
                onclick={() => (descExpanded = !descExpanded)}
              >
                {descExpanded ? t('common.ui.showLess') : t('common.ui.showMore')}
              </button>
            {/if}
          </div>
        {/if}

        <!-- Canonical body sections (appearance / songs & calls / habitat / behaviour).
             Collapsed by default because each can run long; the lead above stays open. -->
        {#each canonicalRows as row (row.id)}
          {@const open = openBodySections.has(row.id)}
          <div class="border-b border-base-300">
            <button
              type="button"
              class="flex w-full cursor-pointer items-center justify-between py-2 text-left font-medium"
              aria-expanded={open}
              aria-controls={`${uid}-section-${row.id}`}
              onclick={() => toggleBodySection(row.id)}
            >
              <span>{t(row.labelKey)}</span>
              {#if open}
                <ChevronDown class="h-4 w-4" />
              {:else}
                <ChevronRight class="h-4 w-4" />
              {/if}
            </button>
            {#if open}
              <div
                id={`${uid}-section-${row.id}`}
                class="pb-3 text-base leading-relaxed whitespace-pre-line"
              >
                {row.body}
              </div>
            {/if}
          </div>
        {/each}

        {@render similarSection(false)}
      {:else}
        <p class="text-sm text-base-content/70 p-4">{t('analytics.species.guide.noSimilar')}</p>
      {/if}
    </div>
  {/if}
</section>
