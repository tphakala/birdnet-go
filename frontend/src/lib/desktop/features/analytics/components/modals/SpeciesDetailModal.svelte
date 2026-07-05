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
  // On desktop, when the guide (right column) has content, widen the modal and
  // split into two columns so the horizontal space is used without stretching the
  // description past a readable measure. Below `lg` the modal stays its default
  // width and the content stacks in a single column (tablet/desktop-only UI).
  let wideLayout = $derived(guideEnabled);
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
  // only the outer collapse via the bindable prop.
  let guidePanelCollapsed = $state(false);
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
    // Only fetch when the taxonomy section will actually render.
    if (!name || !guideEnabled || !showTaxonomy) {
      taxonomy = null;
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
            <div class="grid grid-cols-2 gap-3 text-sm">
              <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
                <span class="opacity-70">{t('analytics.species.card.detections')}</span>
                <span class="font-semibold">{displaySpecies.count}</span>
              </div>
              <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
                <span class="opacity-70">{t('analytics.species.card.confidence')}</span>
                <span class="font-semibold">{formatPercentage(displaySpecies.avg_confidence)}</span>
              </div>
              {#if displaySpecies.first_heard}
                <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
                  <span class="opacity-70">{t('analytics.species.headers.firstDetected')}</span>
                  <span class="font-semibold">{formatDate(displaySpecies.first_heard)}</span>
                </div>
              {/if}
              {#if displaySpecies.last_heard}
                <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
                  <span class="opacity-70">{t('analytics.species.headers.lastDetected')}</span>
                  <span class="font-semibold">{formatDate(displaySpecies.last_heard)}</span>
                </div>
              {/if}
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
                <dl class="space-y-1 text-sm">
                  {#each [{ key: 'class', value: taxonomy.taxonomy.class }, { key: 'order', value: taxonomy.taxonomy.order }, { key: 'family', value: taxonomy.taxonomy.family }, { key: 'genus', value: taxonomy.taxonomy.genus }, { key: 'species', value: taxonomy.taxonomy.species }] as rank (rank.key)}
                    <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
                      <dt class="opacity-70">{t(`species.taxonomy.labels.${rank.key}`)}</dt>
                      <dd class="font-medium" class:italic={rank.key === 'species'}>
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
          <div
            class="mt-4 border-t border-[var(--color-base-300)] pt-4 lg:mt-0 lg:border-t-0 lg:pt-0"
          >
            {#key displaySpecies.scientific_name}
              <SpeciesComparison
                scientificName={displaySpecies.scientific_name}
                commonName={displayName}
                heading={t('analytics.species.guide.title')}
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
    <button
      class="px-4 py-2 rounded-lg font-medium transition-colors w-full
             bg-[var(--color-primary)] text-[var(--color-primary-content)]
             hover:bg-[var(--color-primary)]/90"
      onclick={handleClose}
    >
      {t('common.close')}
    </button>
  {/snippet}
</Modal>
