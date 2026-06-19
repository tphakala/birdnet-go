<!--
  AnalyticsControlBar: the shared, sticky filter bar for the analytics hub.

  Holds the controls that apply across charts: date range (preset + custom),
  species selection (reusing SpeciesSelector), and a source/mic filter. All
  state lives in the URL via the hub; this component is presentational: it reads
  the resolved params and reports changes through `onParamsChange`.

  Each control honors the active tab's chart `supports` flags: when no chart in
  the active tab filters by species (e.g. Biodiversity), the species selector is
  disabled with an explanation rather than silently ignored. The source filter
  is present but inert in PR0 (no backend wiring yet) and says so.
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import SpeciesSelector from '$lib/components/ui/SpeciesSelector.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { Species } from '$lib/types/species';
  import { formatDateForAPI } from '../registry/analyticsParams';
  import type { AnalyticsParams, DateRangePreset } from '../registry/types';

  interface Props {
    params: AnalyticsParams;
    availableSpecies: Species[];
    loadingSpecies?: boolean;
    /** Whether any chart in the active tab filters by species. */
    speciesApplicable?: boolean;
    onParamsChange: (_partial: Partial<AnalyticsParams>) => void;
  }

  let {
    params,
    availableSpecies,
    loadingSpecies = false,
    speciesApplicable = true,
    onParamsChange,
  }: Props = $props();

  // Backend caps multi-species queries; keep the legacy client limit.
  const MAX_SPECIES = 10;

  const dateRangeOptions = $derived([
    { value: 'week', label: t('analytics.advanced.dateRangeOptions.week') },
    { value: 'month', label: t('analytics.advanced.dateRangeOptions.month') },
    { value: 'quarter', label: t('analytics.advanced.dateRangeOptions.quarter') },
    { value: 'year', label: t('analytics.advanced.dateRangeOptions.year') },
    { value: 'custom', label: t('analytics.advanced.dateRangeOptions.custom') },
  ]);

  // Source filter is inert in PR0: a single "All sources" option, disabled.
  const sourceOptions = $derived([{ value: '', label: t('analytics.hub.controls.sourceAll') }]);

  // Custom date inputs reflect the resolved range so switching to "custom"
  // starts from whatever was showing, and reloads restore the typed dates.
  const customStart = $derived(params.start || formatDateForAPI(params.startDate));
  const customEnd = $derived(params.end || formatDateForAPI(params.endDate));

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

<div class="bg-[var(--color-base-100)] rounded-xl shadow-sm border border-[var(--color-base-200)]">
  <div class="p-4 sm:p-6 overflow-visible space-y-4">
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-4 lg:gap-6">
      <!-- Date range -->
      <div class="space-y-2">
        <SelectDropdown
          value={params.range}
          options={dateRangeOptions}
          onChange={handleRangeChange}
          label={t('analytics.advanced.dateRange')}
          variant="select"
          size="sm"
          menuSize="sm"
        />

        {#if params.range === 'custom'}
          <div class="grid grid-cols-2 gap-2 mt-2">
            <label for="analyticsStartDate" class="sr-only"
              >{t('analytics.advanced.filters.startDate')}</label
            >
            <input
              id="analyticsStartDate"
              type="date"
              class="input w-full"
              value={customStart}
              max={customEnd}
              onchange={handleStartChange}
              aria-label={t('analytics.advanced.filters.startDate')}
            />
            <label for="analyticsEndDate" class="sr-only"
              >{t('analytics.advanced.filters.endDate')}</label
            >
            <input
              id="analyticsEndDate"
              type="date"
              class="input w-full"
              value={customEnd}
              min={customStart}
              onchange={handleEndChange}
              aria-label={t('analytics.advanced.filters.endDate')}
            />
          </div>
        {/if}
      </div>

      <!-- Source / mic filter (present but inert in PR0) -->
      <div class="space-y-1">
        <SelectDropdown
          value={params.source}
          options={sourceOptions}
          disabled={true}
          label={t('analytics.hub.controls.source')}
          helpText={t('analytics.hub.controls.sourceComingSoon')}
          variant="select"
          size="sm"
          menuSize="sm"
        />
      </div>
    </div>

    <!-- Species selection -->
    <div class="space-y-2">
      <div class="flex items-baseline justify-between gap-2">
        <span id="analyticsSpeciesLabel" class="label-text font-medium"
          >{t('analytics.advanced.speciesSelection', {
            count: params.species.length,
            max: MAX_SPECIES,
          })}</span
        >
        <span
          id="analyticsSpeciesHint"
          class="label-text-alt text-xs text-[var(--color-base-content)] opacity-60"
        >
          {speciesApplicable
            ? t('analytics.advanced.speciesSelectionHint')
            : t('analytics.hub.controls.speciesNotApplicable')}
        </span>
      </div>

      <div
        class="w-full min-h-[100px] relative"
        role="group"
        aria-labelledby="analyticsSpeciesLabel"
        aria-describedby="analyticsSpeciesHint"
      >
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
          disabled={!speciesApplicable}
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
  </div>
</div>
