<script lang="ts">
  import { t } from '$lib/i18n';
  import { navigationIcons } from '$lib/utils/icons';

  export interface FilterState {
    species: string;
    startDate: string;
    endDate: string;
    confidenceMin: number;
    timeOfDay: string[];
    hourStart: number;
    hourEnd: number;
    verified: string;
  }

  interface Props {
    open: boolean;
    filters: FilterState;
    onClose: () => void;
    onApply: (_newFilters: FilterState) => void;
    onClear: () => void;
  }

  let { open, filters = $bindable(), onClose, onApply, onClear }: Props = $props();

  const timeOfDayOptions = ['Sunrise', 'Day', 'Sunset', 'Night'];

  function toggleTimeOfDay(option: string) {
    if (filters.timeOfDay.includes(option)) {
      filters.timeOfDay = filters.timeOfDay.filter(t => t !== option);
    } else {
      filters.timeOfDay = [...filters.timeOfDay, option];
    }
  }

  function handleApply() {
    onApply(filters);
  }

  function handleClear() {
    onClear();
  }
</script>

{#if open}
  <div class="fixed inset-0 z-50 bg-base-100 flex flex-col">
    <!-- Header -->
    <div class="flex items-center justify-between p-4 border-b border-base-300">
      <button
        type="button"
        class="btn btn-ghost btn-sm btn-square"
        aria-label={t('common.close')}
        onclick={onClose}
      >
        {@html navigationIcons.close}
      </button>
      <h2 class="text-lg font-semibold">{t('detections.filters.title')}</h2>
      <button type="button" class="btn btn-primary btn-sm" onclick={handleApply}>
        {t('detections.filters.apply')}
      </button>
    </div>

    <!-- Scrollable Content -->
    <div class="flex-1 overflow-y-auto p-4 space-y-6">
      <!-- Species Filter -->
      <div class="form-control">
        <label class="label" for="filter-species">
          <span class="label-text font-medium">{t('detections.filters.species')}</span>
        </label>
        <input
          id="filter-species"
          type="text"
          bind:value={filters.species}
          placeholder={t('detections.filters.speciesPlaceholder')}
          class="input input-bordered w-full"
        />
      </div>

      <!-- Date Range -->
      <div class="space-y-3">
        <span class="label-text font-medium">{t('detections.filters.dateRange')}</span>
        <div class="grid grid-cols-2 gap-3">
          <div class="form-control">
            <label class="label py-1" for="filter-start-date">
              <span class="label-text text-sm">{t('detections.filters.from')}</span>
            </label>
            <input
              id="filter-start-date"
              type="date"
              bind:value={filters.startDate}
              class="input input-bordered w-full"
            />
          </div>
          <div class="form-control">
            <label class="label py-1" for="filter-end-date">
              <span class="label-text text-sm">{t('detections.filters.to')}</span>
            </label>
            <input
              id="filter-end-date"
              type="date"
              bind:value={filters.endDate}
              class="input input-bordered w-full"
            />
          </div>
        </div>
      </div>

      <!-- Confidence -->
      <div class="form-control">
        <label class="label" for="filter-confidence">
          <span class="label-text font-medium">{t('detections.filters.confidence')}</span>
          <span class="label-text-alt">{filters.confidenceMin}%+</span>
        </label>
        <input
          id="filter-confidence"
          type="range"
          min="0"
          max="100"
          step="5"
          bind:value={filters.confidenceMin}
          class="range range-primary"
        />
        <div class="flex justify-between text-xs text-base-content/50 px-1 mt-1">
          <span>0%</span>
          <span>50%</span>
          <span>100%</span>
        </div>
      </div>

      <!-- Time of Day -->
      <div class="space-y-3">
        <span class="label-text font-medium">{t('detections.filters.timeOfDay')}</span>
        <div class="flex flex-wrap gap-2">
          {#each timeOfDayOptions as option}
            <button
              type="button"
              class="btn btn-sm {filters.timeOfDay.includes(option)
                ? 'btn-primary'
                : 'btn-outline'}"
              onclick={() => toggleTimeOfDay(option)}
            >
              {t(`detections.timeOfDay.${option.toLowerCase()}`)}
            </button>
          {/each}
        </div>
      </div>

      <!-- Hour Range -->
      <div class="space-y-3">
        <span class="label-text font-medium">{t('detections.filters.hourRange')}</span>
        <div class="grid grid-cols-2 gap-3">
          <div class="form-control">
            <label class="label py-1" for="filter-hour-start">
              <span class="label-text text-sm">{t('detections.filters.from')}</span>
            </label>
            <input
              id="filter-hour-start"
              type="number"
              min="0"
              max="23"
              bind:value={filters.hourStart}
              class="input input-bordered w-full"
            />
          </div>
          <div class="form-control">
            <label class="label py-1" for="filter-hour-end">
              <span class="label-text text-sm">{t('detections.filters.to')}</span>
            </label>
            <input
              id="filter-hour-end"
              type="number"
              min="0"
              max="23"
              bind:value={filters.hourEnd}
              class="input input-bordered w-full"
            />
          </div>
        </div>
      </div>

      <!-- Verified Status -->
      <div class="form-control">
        <label class="label" for="filter-verified">
          <span class="label-text font-medium">{t('detections.filters.verified')}</span>
        </label>
        <select
          id="filter-verified"
          bind:value={filters.verified}
          class="select select-bordered w-full"
        >
          <option value="">{t('detections.filters.verifiedAll')}</option>
          <option value="true">{t('detections.filters.verifiedOnly')}</option>
          <option value="false">{t('detections.filters.unverifiedOnly')}</option>
        </select>
      </div>
    </div>

    <!-- Footer -->
    <div class="p-4 border-t border-base-300">
      <button type="button" class="btn btn-ghost w-full" onclick={handleClear}>
        {t('detections.filters.clearAll')}
      </button>
    </div>
  </div>
{/if}
