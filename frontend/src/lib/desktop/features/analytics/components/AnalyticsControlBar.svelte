<!--
  AnalyticsControlBar: the shared filter toolbar for the analytics hub.

  A slim, page-colored toolbar (not a card) that sits under the tab bar. The
  controls that apply across charts live here: date range (preset + custom),
  species selection (reusing SpeciesSelector), and a source/mic filter. All
  state lives in the URL via the hub; this component is presentational and
  reports changes through `onParamsChange`.

  The species chip selector is the tall control, so it is collapsed behind a
  toggle (the date range stays visible) to keep the toolbar compact. Each
  control honors the active tab's chart `supports` flags: when no chart in the
  active tab filters by species (e.g. Biodiversity), the species toggle is
  hidden entirely (a permanently-disabled toggle showing a stale selection
  count would be confusing) and a short note explains why. The source/mic
  filter is governed by
  `sourceApplicable`: no chart consumes the source dimension yet, so the control
  is hidden until a source-aware chart lands. It still renders (enabled, so it is
  clearable) when a stale `source` value arrives from a URL/bookmark, so that
  value is never stuck; when shown and applicable it carries a specific disabled
  reason rather than being a silent dead end.
-->
<script lang="ts">
  import { ChevronDown, ChevronRight } from '@lucide/svelte';

  import { t } from '$lib/i18n';
  import SpeciesSelector from '$lib/components/ui/SpeciesSelector.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { Species } from '$lib/types/species';
  import { formatDateForAPI } from '../registry/analyticsParams';
  import type { AnalyticsParams, AudioSourceOption, DateRangePreset } from '../registry/types';

  interface Props {
    params: AnalyticsParams;
    availableSpecies: Species[];
    loadingSpecies?: boolean;
    /** Whether any chart in the active tab filters by species. */
    speciesApplicable?: boolean;
    /** Audio sources for the source/mic filter (empty until a source-aware tab loads them). */
    availableSources?: AudioSourceOption[];
    loadingSources?: boolean;
    /** Whether any chart in the active tab filters by source. */
    sourceApplicable?: boolean;
    onParamsChange: (_partial: Partial<AnalyticsParams>) => void;
  }

  let {
    params,
    availableSpecies,
    loadingSpecies = false,
    speciesApplicable = true,
    availableSources = [],
    loadingSources = false,
    sourceApplicable = false,
    onParamsChange,
  }: Props = $props();

  // Backend caps multi-species queries; keep the legacy client limit.
  const MAX_SPECIES = 10;

  // The tall chip selector starts collapsed to keep the toolbar slim; the toggle
  // shows the current count so the selection is never hidden without a hint.
  let speciesExpanded = $state(false);

  const dateRangeOptions = $derived([
    { value: 'week', label: t('analytics.advanced.dateRangeOptions.week') },
    { value: 'month', label: t('analytics.advanced.dateRangeOptions.month') },
    { value: 'quarter', label: t('analytics.advanced.dateRangeOptions.quarter') },
    { value: 'year', label: t('analytics.advanced.dateRangeOptions.year') },
    { value: 'custom', label: t('analytics.advanced.dateRangeOptions.custom') },
  ]);

  // Source/mic filter options: "All sources" plus the live source list (id -> opaque value, name ->
  // already anonymized server-side for unauthenticated clients).
  const sourceOptions = $derived([
    { value: '', label: t('analytics.hub.controls.sourceAll') },
    ...availableSources.map(s => ({ value: s.id, label: s.name })),
  ]);

  // The source filter is enabled when a chart in the active tab consumes the source dimension and
  // either there are sources to choose from or a source is already selected; the selected-source case
  // keeps a stale filter from a URL/bookmark clearable back to "All sources" even when the live list
  // came back empty. No chart sets supports.source yet, so the control is normally hidden (see the
  // {#if} gate below); the second clause keeps it enabled when it is shown solely to clear a stale
  // `source` value while sourceApplicable is false, so that value is never stuck.
  const sourceEnabled = $derived(
    (sourceApplicable &&
      !loadingSources &&
      (availableSources.length > 0 || params.source !== '')) ||
      (!sourceApplicable && params.source !== '')
  );

  const sourceDisabledReason = $derived.by(() => {
    if (sourceEnabled) return undefined;
    if (!sourceApplicable) return t('analytics.hub.controls.sourceNotApplicable');
    if (loadingSources) return t('analytics.hub.controls.sourceLoading');
    return t('analytics.hub.controls.sourceNone');
  });

  function handleSourceChange(value: string | string[]): void {
    const source = Array.isArray(value) ? (value[0] ?? '') : value;
    onParamsChange({ source });
  }

  // Custom date inputs reflect the resolved range so switching to "custom"
  // starts from whatever was showing, and reloads restore the typed dates.
  const customStart = $derived(params.start || formatDateForAPI(params.startDate));
  const customEnd = $derived(params.end || formatDateForAPI(params.endDate));

  const speciesLabel = $derived(
    t('analytics.advanced.speciesSelection', {
      count: params.species.length,
      max: MAX_SPECIES,
    })
  );

  // The panel is only open when species filtering applies AND the user expanded
  // it. Deriving this (rather than reading speciesExpanded directly) keeps the
  // toggle's chevron, aria-expanded, and aria-controls consistent when the active
  // tab switches to one whose charts do not filter by species.
  const speciesPanelOpen = $derived(speciesApplicable && speciesExpanded);

  function handleRangeChange(value: string | string[]): void {
    const range = (Array.isArray(value) ? value[0] : value) as DateRangePreset;
    if (range === 'custom') {
      // Seed the custom inputs (and the URL) from the currently resolved range.
      onParamsChange({ range, start: customStart, end: customEnd });
    } else {
      onParamsChange({ range });
    }
  }

  function handleStartChange(event: Event): void {
    const value = (event.target as HTMLInputElement).value;
    onParamsChange({ range: 'custom', start: value, end: customEnd });
  }

  function handleEndChange(event: Event): void {
    const value = (event.target as HTMLInputElement).value;
    onParamsChange({ range: 'custom', start: customStart, end: value });
  }
</script>

<div class="pb-3 border-b border-[var(--color-base-300)]/60">
  <!-- Compact controls row: date range always visible, source only when applicable, species behind a toggle -->
  <div class="flex flex-wrap items-end gap-x-4 gap-y-2">
    <!-- Date range -->
    <div class="w-44 max-w-full space-y-1">
      <SelectDropdown
        value={params.range}
        options={dateRangeOptions}
        onChange={handleRangeChange}
        label={t('analytics.advanced.dateRange')}
        variant="select"
        size="sm"
        menuSize="sm"
      />
    </div>

    {#if params.range === 'custom'}
      <div class="flex items-end gap-2">
        <div class="space-y-1">
          <label
            for="analyticsStartDate"
            class="text-xs font-medium text-[var(--color-base-content)]/70"
            >{t('analytics.advanced.filters.startDate')}</label
          >
          <input
            id="analyticsStartDate"
            type="date"
            class="input input-sm"
            value={customStart}
            max={customEnd}
            onchange={handleStartChange}
            aria-label={t('analytics.advanced.filters.startDate')}
          />
        </div>
        <div class="space-y-1">
          <label
            for="analyticsEndDate"
            class="text-xs font-medium text-[var(--color-base-content)]/70"
            >{t('analytics.advanced.filters.endDate')}</label
          >
          <input
            id="analyticsEndDate"
            type="date"
            class="input input-sm"
            value={customEnd}
            min={customStart}
            onchange={handleEndChange}
            aria-label={t('analytics.advanced.filters.endDate')}
          />
        </div>
      </div>
    {/if}

    <!-- Source / mic filter. No chart consumes the source dimension (supports.source) yet, so this
         control is normally hidden and returns automatically once a source-aware chart lands. It is
         still rendered (and enabled, so it is clearable) when a stale `source` value arrives from a
         URL/bookmark while sourceApplicable is false, so that value is never stuck; selecting
         "All sources" clears it and the control disappears again. When applicable it is enabled only
         when sources exist; otherwise it carries a specific disabled reason as visible help text
         (SelectDropdown wires it via aria-describedby), discoverable on touch/tablet and to screen
         readers, matching the species control rather than relying on a hover-only tooltip. -->
    {#if sourceApplicable || params.source !== ''}
      <div class="w-44 max-w-full space-y-1">
        <SelectDropdown
          value={params.source}
          options={sourceOptions}
          disabled={!sourceEnabled}
          onChange={handleSourceChange}
          id="analyticsSourceFilter"
          label={t('analytics.hub.controls.source')}
          placeholder={t('analytics.hub.controls.sourceAll')}
          helpText={sourceDisabledReason}
          variant="select"
          size="sm"
          menuSize="sm"
        />
      </div>
    {/if}

    <div class="grow"></div>

    <!-- Species toggle: expands the chip selector; shows the current count. Only rendered when a
         chart in the active tab filters by species — a permanently-disabled toggle showing a
         selection count that is silently ignored is confusing, so the control is dropped entirely
         rather than shown disabled (see the "not applicable" note below instead). -->
    {#if speciesApplicable}
      <button
        id="analyticsSpeciesToggle"
        type="button"
        class="inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-sm font-medium border border-[var(--color-base-300)] hover:bg-[var(--color-base-100)]"
        aria-expanded={speciesPanelOpen}
        aria-controls={speciesPanelOpen ? 'analyticsSpeciesPanel' : undefined}
        onclick={() => (speciesExpanded = !speciesExpanded)}
      >
        {#if speciesPanelOpen}
          <ChevronDown class="h-4 w-4" aria-hidden="true" />
        {:else}
          <ChevronRight class="h-4 w-4" aria-hidden="true" />
        {/if}
        <span>{speciesLabel}</span>
      </button>
    {/if}
  </div>

  {#if !speciesApplicable}
    <p class="mt-2 text-xs text-[var(--color-base-content)]/60">
      {t('analytics.hub.controls.speciesNotApplicable')}
    </p>
  {:else if speciesPanelOpen}
    <!-- Collapsible species selector -->
    <div
      id="analyticsSpeciesPanel"
      class="mt-3"
      role="group"
      aria-labelledby="analyticsSpeciesToggle"
      aria-describedby="analyticsSpeciesHint"
    >
      <p id="analyticsSpeciesHint" class="mb-2 text-xs text-[var(--color-base-content)]/60">
        {t('analytics.advanced.speciesSelectionHint')}
      </p>
      <div class="w-full relative">
        <SpeciesSelector
          species={availableSpecies}
          selected={params.species}
          variant="chip"
          size="md"
          maxSelections={MAX_SPECIES}
          placeholder={t('analytics.advanced.speciesPlaceholder')}
          searchable={true}
          showFrequency={true}
          categorized={false}
          loading={loadingSpecies}
          emptyText={t('analytics.advanced.noSpeciesFound')}
          className="w-full"
          on:change={e => onParamsChange({ species: e.detail.selected })}
        >
          {#snippet speciesDisplay(species)}
            <div class="flex items-center justify-between gap-2">
              <div class="flex-1 min-w-0">
                <div class="font-medium truncate">{species.commonName}</div>
                <div class="text-xs text-[var(--color-base-content)] opacity-60 truncate italic">
                  {species.scientificName}
                </div>
              </div>
              {#if species.count !== undefined}
                <div
                  class="inline-flex items-center px-2 py-1 text-xs font-medium rounded-full bg-[var(--color-base-200)]/50 text-[var(--color-base-content)]"
                >
                  {t('analytics.advanced.detections', { count: species.count ?? 0 })}
                </div>
              {/if}
            </div>
          {/snippet}
        </SpeciesSelector>
      </div>
    </div>
  {/if}
</div>
