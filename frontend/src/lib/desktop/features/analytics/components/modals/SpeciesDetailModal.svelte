<script lang="ts">
  import { untrack } from 'svelte';
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import { t } from '$lib/i18n';
  import { formatDate } from '$lib/utils/formatters';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { isAuthenticated } from '$lib/utils/auth';
  import { dashboardSettings } from '$lib/stores/settings';
  import {
    resolveSpeciesGuideConfig,
    type SpeciesGuideUIConfig,
  } from '$lib/utils/speciesGuideConfig';
  import SpeciesComparison from '$lib/desktop/components/ui/SpeciesComparison.svelte';
  import SpeciesNotes from '$lib/desktop/components/ui/SpeciesNotes.svelte';
  import { api } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import type { SpeciesTaxonomyResponse } from '$lib/types/species';

  const logger = loggers.ui;

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
    species: SpeciesData | null;
    isOpen: boolean;
    onClose?: () => void;
    /**
     * When false, hide the detection stats grid (count / confidence / first /
     * last). Used for live "currently hearing" pings, which have no aggregate
     * stats — only the species name, image, and guide are meaningful there.
     */
    showStats?: boolean;
  }

  let { species, isOpen, onClose, showStats = true }: Props = $props();

  // Cache species data so content persists during the Modal close animation.
  // Updated when a new species is provided, retained when species becomes null
  // while isOpen transitions to false.
  let cachedSpecies = $state<SpeciesData | null>(null);

  // Clear stale cache when the modal opens so previous species data doesn't flash.
  // The cache is only useful during the close transition (species becomes null while
  // isOpen transitions to false), not during open.
  let prevIsOpen = $state(false);
  // Species guide config (gated on settings). Gating must work for unauthenticated
  // guests too — the guide endpoints and GET /settings/dashboard are public, but the
  // settings store is populated only by the auth-protected full-settings load.
  // resolveSpeciesGuideConfig prefers the store value and falls back to one cached
  // fetch of the public endpoint.
  let guideConfig = $state<SpeciesGuideUIConfig | null>(null);
  $effect(() => {
    const fromStore = $dashboardSettings?.speciesGuide;
    let stale = false;
    void resolveSpeciesGuideConfig(fromStore).then(cfg => {
      if (!stale) guideConfig = cfg;
    });
    return () => {
      stale = true;
    };
  });
  let guideEnabled = $derived(guideConfig?.enabled ?? false);
  let showSimilarSpecies = $derived(guideConfig?.showSimilarSpecies ?? true);
  let showNotes = $derived(guideConfig?.showNotes ?? true);
  let showTaxonomy = $derived(guideConfig?.showTaxonomy ?? true);

  // Taxonomy (offline OpenFauna, public endpoint). Fetched only when the section
  // will render; a `stale` guard prevents a previous species' taxonomy flashing
  // when the modal is reused for a different species.
  let taxonomy = $state<SpeciesTaxonomyResponse | null>(null);
  let taxonomyLoading = $state(false);
  // Outer collapse state for the guide panel (bindable into SpeciesComparison).
  // Declared before `wideLayout` because that derived reads it; reset to expanded
  // on each open / species change by the $effect.pre further down.
  let guidePanelCollapsed = $state(false);
  // On desktop, when the guide (right column) is expanded, widen the modal and
  // split into two columns so the horizontal space is used without stretching the
  // description past a readable measure. Below `lg` the modal stays its default
  // width and the content stacks in a single column (tablet/desktop-only UI).
  // When the guide is collapsed the right column would just be an empty half, so
  // drop back to the single-column `md` width — the same layout used when the
  // guide is disabled entirely. The image renders at ~400px there, matching the
  // Wikipedia thumbnail source, so nothing is upscaled.
  let wideLayout = $derived(guideEnabled && !guidePanelCollapsed);
  $effect(() => {
    if (isOpen && !untrack(() => prevIsOpen)) {
      cachedSpecies = null;
    }
    prevIsOpen = isOpen;
  });

  $effect(() => {
    if (species) {
      cachedSpecies = species;
    }
  });

  // Use cached data for rendering, fall back to current prop
  let displaySpecies = $derived(species ?? cachedSpecies);
  let displayName = $derived(
    localizeSpeciesName(displaySpecies?.scientific_name, displaySpecies?.common_name)
  );

  // The guide panel re-expands whenever the modal shows a species: the description is
  // the primary thing users open the guide for, so it should be visible on every open.
  // The inner section toggles (songs/similar) live in SpeciesComparison and persist on
  // their own across reopens (it isn't remounted for the same species), so this resets
  // only the outer collapse via the bindable prop (declared above).
  // $effect.pre runs before the DOM update, so when the species changes the reset lands
  // before the {#key} remounts SpeciesComparison — the panel mounts already expanded
  // rather than briefly adopting a stale collapsed value. It deliberately does not read
  // guidePanelCollapsed, so collapsing the panel mid-view isn't fought.
  $effect.pre(() => {
    const name = displaySpecies?.scientific_name;
    if (isOpen && name) {
      guidePanelCollapsed = false;
    }
  });

  $effect(() => {
    const name = displaySpecies?.scientific_name;
    // Only fetch when the taxonomy section will actually render. Also clear any
    // in-flight loading flag: if a previous run's fetch is still pending, its
    // cleanup already set stale=true so its .finally won't reset the flag, so
    // reset it here to avoid a stuck loading state when the section re-renders.
    if (!name || !guideEnabled || !showTaxonomy) {
      taxonomy = null;
      taxonomyLoading = false;
      return;
    }
    let stale = false;
    taxonomyLoading = true;
    void api
      .get<SpeciesTaxonomyResponse>(
        `/api/v2/species/taxonomy?scientific_name=${encodeURIComponent(name)}`
      )
      .then(data => {
        if (!stale) taxonomy = data;
      })
      .catch((e: unknown) => {
        if (!stale) {
          taxonomy = null;
          logger.error('Failed to fetch taxonomy for guide modal', e, {
            component: 'SpeciesDetailModal',
          });
        }
      })
      .finally(() => {
        if (!stale) taxonomyLoading = false;
      });
    return () => {
      stale = true;
    };
  });

  // Per-rank indent for the taxonomy lineage, using the Tailwind spacing scale
  // (0.75rem steps) so each level steps in against its rail without an inline-style
  // magic number. Keyed by rank position (class=0 … species=4). A Map (not an
  // array indexed by a dynamic key) avoids the security/detect-object-injection lint.
  const TAXONOMY_INDENT = new Map<number, string>([
    [0, 'ml-0'],
    [1, 'ml-3'],
    [2, 'ml-6'],
    [3, 'ml-9'],
    [4, 'ml-12'],
  ]);

  function formatPercentage(value: number): string {
    return (value * 100).toFixed(1) + '%';
  }

  function handleClose() {
    if (onClose) onClose();
  }
</script>

<Modal
  isOpen={isOpen && displaySpecies !== null}
  title={displayName}
  size="md"
  type="default"
  onClose={handleClose}
  className={`sm:modal-middle${wideLayout ? ' lg:max-w-5xl' : ''}`}
  aria-labelledby="species-detail-modal-title"
>
  {#snippet header()}
    {#if displaySpecies}
      <div class="flex items-center justify-between">
        <div class="min-w-0">
          <h3 id="species-detail-modal-title" class="font-bold text-lg truncate">
            {displayName}
          </h3>
          <p class="text-sm text-[var(--color-base-content)] opacity-70 italic truncate">
            {displaySpecies.scientific_name}
          </p>
        </div>
      </div>
    {/if}
  {/snippet}

  {#snippet children()}
    {#if displaySpecies}
      <!-- On desktop (lg+) with guide content, split into two columns so the wide
           modal isn't a single narrow column with large empty margins. Below lg
           everything stacks (tablet/desktop-only UI). -->
      <div class={wideLayout ? 'lg:grid lg:grid-cols-2 lg:gap-6 lg:items-start' : ''}>
        <!-- Primary column: image, stats, notes -->
        <div class="space-y-3">
          {#if displaySpecies.thumbnail_url}
            <div class="w-full aspect-[4/3] rounded-xl overflow-hidden bg-[var(--color-base-300)]">
              <img
                src={displaySpecies.thumbnail_url}
                alt={displayName}
                class="w-full h-full object-cover"
              />
            </div>
          {/if}

          {#if showStats}
            <!-- "Your detections": the user's own data, set apart from the reference
                 taxonomy below by a primary accent rail and value-forward typography
                 rather than repeating the same gray-pill row idiom. -->
            <div class="border-l-2 border-[var(--color-primary)] pl-3">
              <div class="flex flex-wrap gap-x-6 gap-y-3">
                <div>
                  <div class="text-xl font-semibold leading-none">{displaySpecies.count}</div>
                  <div class="mt-1 text-xs opacity-60">
                    {t('analytics.species.card.detections')}
                  </div>
                </div>
                <div>
                  <div class="text-xl font-semibold leading-none">
                    {formatPercentage(displaySpecies.avg_confidence)}
                  </div>
                  <div class="mt-1 text-xs opacity-60">
                    {t('analytics.species.card.confidence')}
                  </div>
                </div>
                {#if displaySpecies.first_heard}
                  <div>
                    <div class="text-sm font-medium leading-none">
                      {formatDate(displaySpecies.first_heard)}
                    </div>
                    <div class="mt-1 text-xs opacity-60">
                      {t('analytics.species.headers.firstDetected')}
                    </div>
                  </div>
                {/if}
                {#if displaySpecies.last_heard}
                  <div>
                    <div class="text-sm font-medium leading-none">
                      {formatDate(displaySpecies.last_heard)}
                    </div>
                    <div class="mt-1 text-xs opacity-60">
                      {t('analytics.species.headers.lastDetected')}
                    </div>
                  </div>
                {/if}
              </div>
            </div>
          {/if}

          {#if guideEnabled && showTaxonomy && (taxonomyLoading || taxonomy?.taxonomy)}
            <!-- Taxonomy: factual metadata (like the stats), rendered in the modal's
                 own row idiom rather than DetectionDetail's connector-tree CSS. The
                 two renderings are intentionally separate to keep this branch-scoped;
                 a future upstream PR can extract a shared TaxonomyTree component. -->
            <div class="border-t border-[var(--color-base-300)] pt-4 space-y-2">
              <h4 class="text-sm font-semibold opacity-70">{t('species.taxonomy.hierarchy')}</h4>
              {#if taxonomyLoading}
                <div class="animate-pulse space-y-2" aria-hidden="true">
                  {#each Array(5) as _, i (i)}
                    <div class="h-8 rounded bg-[var(--color-base-200)]"></div>
                  {/each}
                </div>
              {:else if taxonomy?.taxonomy}
                <!-- Rendered as an indented lineage so the class -> order -> family ->
                     genus -> species nesting is visible, instead of five equal rows
                     that discard the hierarchy. Each level steps in against a rail;
                     the species (the leaf) carries the primary accent. -->
                <dl class="text-sm">
                  {#each [{ key: 'class', value: taxonomy.taxonomy.class }, { key: 'order', value: taxonomy.taxonomy.order }, { key: 'family', value: taxonomy.taxonomy.family }, { key: 'genus', value: taxonomy.taxonomy.genus }, { key: 'species', value: taxonomy.taxonomy.species }] as rank, i (rank.key)}
                    <!-- DOM order is term (dt) then value (dd) so assistive tech keeps
                         the "rank: name" association; `order` flips them visually so the
                         name stays prominent on the left and the muted rank sits after. -->
                    <div
                      class={`flex items-baseline gap-2 py-1 pl-3 ${TAXONOMY_INDENT.get(i) ?? 'ml-12'} ${rank.key === 'species' ? 'border-l-2 border-[var(--color-primary)]' : 'border-l border-[var(--color-base-300)]'}`}
                    >
                      <dt class="order-2 text-xs opacity-50">
                        {t(`species.taxonomy.labels.${rank.key}`)}
                      </dt>
                      <dd
                        class="order-1 font-medium"
                        class:italic={rank.key === 'species' || rank.key === 'genus'}
                      >
                        {rank.value}
                      </dd>
                    </div>
                  {/each}
                </dl>
                {#if taxonomy.subspecies && taxonomy.subspecies.length > 0}
                  <div class="pt-2">
                    <h5 class="text-xs font-medium opacity-60 mb-1">
                      {t('species.taxonomy.subspecies')}
                    </h5>
                    <ul class="space-y-1 text-sm">
                      {#each taxonomy.subspecies as sub, i (`${sub.scientific_name}_${i}`)}
                        <li class="flex flex-col">
                          <span class="italic">{sub.scientific_name}</span>
                          {#if sub.common_name}
                            <span class="text-xs opacity-70"
                              >{localizeSpeciesName(sub.scientific_name, sub.common_name)}</span
                            >
                          {/if}
                        </li>
                      {/each}
                    </ul>
                  </div>
                {/if}
              {/if}
            </div>
          {/if}

          {#if guideEnabled && showNotes && $isAuthenticated}
            <!-- Notes are auth-gated (reads included), so only render the section
                 for authenticated users. Key on the species so notes remount
                 (and refetch) when the modal is reused for a different species. -->
            <div class="border-t border-[var(--color-base-300)] pt-4">
              {#key displaySpecies.scientific_name}
                <SpeciesNotes scientificName={displaySpecies.scientific_name} />
              {/key}
            </div>
          {/if}
        </div>

        <!-- Guide column: description + enrichments, plus similar species when
             that section is enabled (gated inside SpeciesComparison). -->
        {#if guideEnabled}
          <!-- In the two-column (expanded) layout the guide is grid column 2, so the
               `lg:` resets drop the top separator. When collapsed the modal is single
               column and the guide stacks below the left content, so it keeps its
               top-border separator at every breakpoint. -->
          <div
            class={wideLayout
              ? 'mt-4 border-t border-[var(--color-base-300)] pt-4 lg:mt-0 lg:border-t-0 lg:pt-0'
              : 'mt-4 border-t border-[var(--color-base-300)] pt-4'}
          >
            {#key displaySpecies.scientific_name}
              <SpeciesComparison
                scientificName={displaySpecies.scientific_name}
                commonName={displayName}
                heading={t('analytics.species.guide.speciesGuide')}
                {showSimilarSpecies}
                bind:collapsed={guidePanelCollapsed}
              />
            {/key}
          </div>
        {/if}
      </div>
    {/if}
  {/snippet}

  {#snippet footer()}
    <!-- Close is an escape hatch, not the primary action, so it reads as a quiet
         secondary control rather than a full-width primary button that would draw
         the eye to the least valuable thing on screen. -->
    <div class="flex justify-end">
      <button
        class="px-4 py-2 rounded-lg font-medium transition-colors
               border border-[var(--color-base-300)]
               hover:bg-[var(--color-base-200)]"
        onclick={handleClose}
      >
        {t('common.close')}
      </button>
    </div>
  {/snippet}
</Modal>
