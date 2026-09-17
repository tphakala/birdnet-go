<!--
  DetectionFilterPanel.svelte

  The filter form shown above the detections list. It replaces the standalone
  search page: the same filters now narrow the one detection view instead of a
  second list with its own row rendering, its own actions and its own endpoint.

  The panel edits a draft copy and only reports changes on submit, so dragging a
  confidence slider does not fire a request per pixel. The applied filter set
  lives in the URL (see $lib/utils/detectionFilters), which is why the draft
  re-syncs whenever the incoming `filters` prop changes: back/forward navigation
  and shared links have to be reflected in the form, not just in the results.

  Props:
  - filters: DetectionFilters - the currently applied filters (from the URL)
  - loading?: boolean - disables submit while a request is in flight
  - onApply: (filters: DetectionFilters) => void - submit handler
  - onReset: () => void - clear-all handler
-->
<script lang="ts">
  import DatePicker from '$lib/desktop/components/ui/DatePicker.svelte';
  import { t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { DEFAULT_DETECTION_FILTERS, type DetectionFilters } from '$lib/types/detection.types';
  import { api } from '$lib/utils/api';
  import { isAuthenticated } from '$lib/utils/auth';
  import { getLocalDateString } from '$lib/utils/date';
  import { loggers } from '$lib/utils/logger';
  import { ChevronDown, Search } from '@lucide/svelte';
  import { untrack } from 'svelte';

  interface AudioSourceOption {
    id: string;
    name: string;
  }

  interface Props {
    filters: DetectionFilters;
    loading?: boolean;
    onApply: (_filters: DetectionFilters) => void;
    onReset: () => void;
  }

  let { filters, loading = false, onApply, onReset }: Props = $props();

  const logger = loggers.ui;

  // The form's working copy. Edits here do not affect the list until submit, so it
  // deliberately starts from the applied filters and diverges until then; the
  // $effect below re-syncs it when a genuinely different set arrives.
  let draft = $state<DetectionFilters>(untrack(() => ({ ...filters })));

  /** Whether a filter set uses any control below the advanced toggle. */
  function usesAdvancedFilters(candidate: DetectionFilters): boolean {
    return (
      candidate.confidenceMin > DEFAULT_DETECTION_FILTERS.confidenceMin ||
      candidate.confidenceMax < DEFAULT_DETECTION_FILTERS.confidenceMax ||
      candidate.verified !== '' ||
      candidate.locked !== '' ||
      candidate.timeOfDay !== '' ||
      candidate.source !== ''
    );
  }

  // Start expanded when an advanced filter is already applied, so a shared link
  // never hides the controls that are narrowing what the user is looking at.
  let advancedFilters = $state(untrack(() => usesAdvancedFilters(filters)));

  // Re-sync the draft when a different filter set arrives from the URL (a shared
  // link, back/forward, or the header search box writing `search`). Comparing a
  // serialized signature avoids clobbering in-progress edits on every re-render,
  // which an unconditional copy would do.
  //
  // Expanding the advanced section belongs here, keyed on the *applied* filters,
  // rather than in an effect watching the draft: the latter would re-expand the
  // section the instant the user collapsed it while an advanced filter was set,
  // making the toggle look broken.
  let lastAppliedSignature = untrack(() => JSON.stringify(filters));
  $effect(() => {
    const signature = JSON.stringify(filters);
    if (signature === lastAppliedSignature) return;
    lastAppliedSignature = signature;
    draft = untrack(() => ({ ...filters }));
    if (untrack(() => usesAdvancedFilters(filters))) advancedFilters = true;
  });

  let availableSources = $state<AudioSourceOption[]>([]);
  let showTooltip = $state<string | null>(null);

  // The audio-source list requires authentication, so skip the request for guests
  // rather than letting it 401 on every visit. Without sources the filter is
  // simply not offered.
  $effect(() => {
    if (!$isAuthenticated) {
      availableSources = [];
      return;
    }
    const controller = new AbortController();
    untrack(() => {
      api
        .get<{ sources: AudioSourceOption[] }>('/api/v2/system/audio/sources', {
          signal: controller.signal,
        })
        .then(data => {
          availableSources = data.sources ?? [];
        })
        .catch((error: unknown) => {
          if (!controller.signal.aborted) {
            logger.warn('Failed to load audio sources for the source filter:', error);
            availableSources = [];
          }
        });
    });
    return () => controller.abort();
  });

  // Today, recomputed once per day rather than on every state change.
  const today = $derived.by(() => {
    const daysSinceEpoch = Math.floor(Date.now() / (1000 * 60 * 60 * 24));
    void daysSinceEpoch; // establishes the once-per-day dependency
    return getLocalDateString();
  });

  const startDateConstraints = $derived.by(() => {
    const endDate = draft.endDate;
    // The start cannot be after the end, nor in the future.
    return { maxDate: endDate && endDate < today ? endDate : endDate || today };
  });

  const endDateConstraints = $derived.by(() => {
    const constraints: { maxDate?: string; minDate?: string } = { maxDate: today };
    if (draft.startDate) constraints.minDate = draft.startDate;
    return constraints;
  });

  function handleStartDateChange(date: string) {
    draft.startDate = date;
    // Clear a now-impossible end rather than submitting an inverted range.
    if (date && draft.endDate && date > draft.endDate) {
      draft.endDate = '';
      toastActions.info(t('components.datePicker.feedback.endDateCleared'));
    }
  }

  function handleEndDateChange(date: string) {
    draft.endDate = date;
    // Allow working backwards: setting an end before the start clears the start.
    if (date && draft.startDate && date < draft.startDate) {
      draft.startDate = '';
      toastActions.info(t('components.datePicker.feedback.startDateCleared'));
    }
  }

  // The range inputs are independent, so a user can drag the minimum past the
  // maximum. Report it instead of silently swapping or submitting a range the
  // API would reject.
  let hasConfidenceError = $derived(draft.confidenceMin > draft.confidenceMax);

  function handleSubmit(event: Event) {
    event.preventDefault();
    if (hasConfidenceError || loading) return;
    onApply({ ...draft, search: draft.search.trim(), source: draft.source.trim() });
  }

  function handleReset() {
    draft = { ...DEFAULT_DETECTION_FILTERS };
    onReset();
  }
</script>

<div class="card bg-[var(--color-base-100)] shadow-xs">
  <div class="card-body card-padding">
    <h2 class="card-title" id="detection-filters-heading">{t('search.title')}</h2>

    <form class="space-y-4" onsubmit={handleSubmit} aria-labelledby="detection-filters-heading">
      <!-- Basic filters -->
      <div class="gap-4 search-form-grid">
        <!-- Species / free text -->
        <div class="form-control">
          <label class="label" for="detectionFilterSpecies">
            <span class="label-text">{t('search.fields.species')}</span>
            <span
              class="help-icon"
              onmouseenter={() => (showTooltip = 'species')}
              onmouseleave={() => (showTooltip = null)}
              onfocus={() => (showTooltip = 'species')}
              onblur={() => (showTooltip = null)}
              role="button"
              tabindex="0"
              aria-label={t('search.fields.speciesHelp')}
              aria-describedby="detectionFilterSpeciesTooltip">ⓘ</span
            >
          </label>
          <input
            type="text"
            id="detectionFilterSpecies"
            bind:value={draft.search}
            placeholder={t('search.fields.speciesPlaceholder')}
            class="input w-full"
          />
          {#if showTooltip === 'species'}
            <div class="tooltip" id="detectionFilterSpeciesTooltip" role="tooltip">
              {t('search.fields.speciesHelp')}
            </div>
          {/if}
        </div>

        <!-- Date range -->
        <div class="form-control">
          <label class="label" for="detectionFilterDateStart">
            <span class="label-text">{t('search.fields.dateRange')}</span>
            <span
              class="help-icon"
              onmouseenter={() => (showTooltip = 'dateRange')}
              onmouseleave={() => (showTooltip = null)}
              onfocus={() => (showTooltip = 'dateRange')}
              onblur={() => (showTooltip = null)}
              role="button"
              tabindex="0"
              aria-label={t('search.fields.dateRangeHelp')}
              aria-describedby="detectionFilterDateTooltip">ⓘ</span
            >
          </label>
          <div
            class="gap-2 search-date-grid"
            role="group"
            aria-label={t('search.fields.dateRange')}
          >
            <DatePicker
              value={draft.startDate}
              onChange={handleStartDateChange}
              placeholder={t('search.fields.from')}
              className="w-full"
              size="md"
              maxDate={startDateConstraints.maxDate}
            />
            <DatePicker
              value={draft.endDate}
              onChange={handleEndDateChange}
              placeholder={t('search.fields.to')}
              className="w-full"
              size="md"
              maxDate={endDateConstraints.maxDate}
              minDate={endDateConstraints.minDate}
            />
          </div>
          {#if showTooltip === 'dateRange'}
            <div class="tooltip" id="detectionFilterDateTooltip" role="tooltip">
              {t('search.fields.dateRangeHelp')}
            </div>
          {/if}
        </div>
      </div>

      <!-- Advanced filters toggle -->
      <div class="flex items-center justify-between">
        <button
          type="button"
          class="btn btn-sm btn-ghost"
          onclick={() => (advancedFilters = !advancedFilters)}
          aria-expanded={advancedFilters}
          aria-controls="detectionAdvancedFilters"
        >
          <span
            >{advancedFilters
              ? t('search.hideAdvancedFilters')
              : t('search.showAdvancedFilters')}</span
          >
          <span
            class="transition-transform duration-200"
            class:rotate-180={advancedFilters}
            aria-hidden="true"
          >
            <ChevronDown class="size-5" />
          </span>
        </button>
      </div>

      {#if advancedFilters}
        <div class="space-y-2 pt-2" id="detectionAdvancedFilters">
          <!-- Confidence range -->
          <div class="form-control">
            <label class="label" for="detectionFilterConfidenceMin">
              <span class="label-text">{t('search.fields.confidenceRange')}</span>
              <span class="label-text-alt">{draft.confidenceMin}% - {draft.confidenceMax}%</span>
            </label>
            <div class="gap-6 search-confidence-grid" role="group">
              <div>
                <input
                  type="range"
                  min="0"
                  max="100"
                  id="detectionFilterConfidenceMin"
                  bind:value={draft.confidenceMin}
                  class="range range-xs"
                  aria-label={t('search.fields.confidenceMin')}
                  aria-valuemin="0"
                  aria-valuemax="100"
                  aria-valuenow={draft.confidenceMin}
                  aria-valuetext="{draft.confidenceMin}%"
                />
                <div class="flex justify-between text-xs px-2">
                  <span>0%</span>
                  <span>{draft.confidenceMin}%</span>
                </div>
              </div>
              <div>
                <input
                  type="range"
                  min="0"
                  max="100"
                  bind:value={draft.confidenceMax}
                  class="range range-xs"
                  aria-label={t('search.fields.confidenceMax')}
                  aria-valuemin="0"
                  aria-valuemax="100"
                  aria-valuenow={draft.confidenceMax}
                  aria-valuetext="{draft.confidenceMax}%"
                />
                <div class="flex justify-between text-xs px-2">
                  <span>0%</span>
                  <span>{draft.confidenceMax}%</span>
                </div>
              </div>
            </div>
            {#if hasConfidenceError}
              <div class="text-[var(--color-error)] text-sm mt-1" role="alert">
                {t('search.errors.minMaxConfidence')}
              </div>
            {/if}
          </div>

          <!-- Status and time-of-day filters -->
          <div class="gap-6 search-filters-grid">
            <div class="form-control">
              <label class="label" for="detectionFilterVerified">
                <span class="label-text">{t('search.fields.verifiedStatus')}</span>
              </label>
              <select
                id="detectionFilterVerified"
                bind:value={draft.verified}
                class="select w-full"
              >
                <option value="">{t('search.verifiedOptions.any')}</option>
                <option value="correct">{t('search.verifiedOptions.verified')}</option>
                <option value="unverified">{t('search.verifiedOptions.unverified')}</option>
                <option value="false_positive">{t('common.review.status.falsePositive')}</option>
              </select>
            </div>

            <div class="form-control">
              <label class="label" for="detectionFilterLocked">
                <span class="label-text">{t('search.fields.lockedStatus')}</span>
              </label>
              <select id="detectionFilterLocked" bind:value={draft.locked} class="select w-full">
                <option value="">{t('search.lockedOptions.any')}</option>
                <option value="true">{t('search.lockedOptions.locked')}</option>
                <option value="false">{t('search.lockedOptions.unlocked')}</option>
              </select>
            </div>

            <div class="form-control">
              <label class="label" for="detectionFilterTimeOfDay">
                <span class="label-text">{t('search.fields.timeOfDay')}</span>
              </label>
              <select
                id="detectionFilterTimeOfDay"
                bind:value={draft.timeOfDay}
                class="select w-full"
              >
                <option value="">{t('search.timeOfDayOptions.any')}</option>
                <option value="day">{t('search.timeOfDayOptions.day')}</option>
                <option value="night">{t('search.timeOfDayOptions.night')}</option>
                <option value="sunrise">{t('search.timeOfDayOptions.sunrise')}</option>
                <option value="sunset">{t('search.timeOfDayOptions.sunset')}</option>
              </select>
            </div>

            {#if availableSources.length > 1}
              <div class="form-control">
                <label class="label" for="detectionFilterSource">
                  <span class="label-text">{t('search.fields.source')}</span>
                </label>
                <select id="detectionFilterSource" bind:value={draft.source} class="select w-full">
                  <option value="">{t('search.sourceOptions.any')}</option>
                  {#each availableSources as source (source.id)}
                    <option value={source.name}>{source.name}</option>
                  {/each}
                </select>
              </div>
            {/if}
          </div>
        </div>
      {/if}

      <!-- Actions -->
      <div class="flex flex-row gap-4 justify-end">
        <button
          type="button"
          class="btn btn-ghost shrink-0"
          onclick={handleReset}
          aria-label={t('common.reset')}
        >
          {t('common.reset')}
        </button>
        <button
          type="submit"
          class="btn btn-primary shrink-0"
          disabled={loading || hasConfidenceError}
          aria-label={t('common.search')}
          title={hasConfidenceError ? t('search.errors.minMaxConfidence') : undefined}
        >
          <span class="mr-2" aria-hidden="true">
            <Search class="size-5" />
          </span>
          {t('common.search')}
        </button>
      </div>
    </form>
  </div>
</div>

<style>
  /* Carried over from the standalone search view this panel replaces, so the
     filter form keeps its existing responsive layout. */
  .card-padding {
    padding: 1rem;
  }

  @media (min-width: 768px) {
    .card-padding {
      padding: 1.5rem;
    }
  }

  .tooltip {
    position: absolute;
    background-color: #1f2937;
    color: white;
    padding: 0.5rem;
    border-radius: 0.25rem;
    font-size: 0.875rem;
    margin-top: 0.25rem;
    z-index: 10;
  }

  .help-icon {
    cursor: help;
    font-size: 0.875rem;
    color: #6b7280;
  }

  .search-form-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .search-form-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  .search-confidence-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .search-confidence-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  .search-filters-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .search-filters-grid {
      grid-template-columns: repeat(4, minmax(0, 1fr));
    }
  }

  .search-date-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .search-date-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
</style>
