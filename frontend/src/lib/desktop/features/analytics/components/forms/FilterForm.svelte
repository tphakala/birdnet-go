<script lang="ts">
  import FormField from '$lib/desktop/components/forms/FormField.svelte';
  import Select from '$lib/desktop/components/ui/Select.svelte';
  import Input from '$lib/desktop/components/ui/Input.svelte';
  import { t } from '$lib/i18n/store.svelte.js';

  interface Filters {
    timePeriod: 'all' | 'today' | 'week' | 'month' | '90days' | 'year' | 'custom';
    startDate: string;
    endDate: string;
  }

  interface Props {
    filters: Filters;
    isLoading?: boolean;
    onSubmit: () => void;
    onReset: () => void;
  }

  let { filters = $bindable(), isLoading = false, onSubmit, onReset }: Props = $props();

  const timePeriodOptions = [
    { value: 'all', label: t('analytics.timePeriodOptions.allTime') },
    { value: 'today', label: t('analytics.timePeriodOptions.today') },
    { value: 'week', label: t('analytics.timePeriodOptions.lastWeek') },
    { value: 'month', label: t('analytics.timePeriodOptions.lastMonth') },
    { value: '90days', label: t('analytics.timePeriodOptions.last90Days') },
    { value: 'year', label: t('analytics.timePeriodOptions.lastYear') },
    { value: 'custom', label: t('analytics.timePeriodOptions.customRange') },
  ];

  function handleSubmit(event: Event) {
    event.preventDefault();
    onSubmit();
  }

  function handleReset() {
    onReset();
  }
</script>

<div class="card bg-base-100 shadow-sm">
  <div class="card-body card-padding">
    <h2 class="card-title">{t('analytics.filters.title')}</h2>

    <form class="space-y-4" onsubmit={handleSubmit}>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <!-- Time Period Filter -->
        <FormField label={t('analytics.filters.timePeriod')} id="timePeriod">
          <Select id="timePeriod" bind:value={filters.timePeriod} options={timePeriodOptions} />
        </FormField>

        <!-- Custom Date Range -->
        {#if filters.timePeriod === 'custom'}
          <FormField label={t('analytics.filters.from')} id="startDate">
            <Input type="date" id="startDate" bind:value={filters.startDate} />
          </FormField>

          <FormField label={t('analytics.filters.to')} id="endDate">
            <Input type="date" id="endDate" bind:value={filters.endDate} />
          </FormField>
        {/if}
      </div>

      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-ghost" onclick={handleReset} disabled={isLoading}>
          {t('analytics.filters.reset')}
        </button>
        <button type="submit" class="btn btn-primary" disabled={isLoading}>
          {#if isLoading}
            <span class="loading loading-spinner loading-sm"></span>
          {/if}
          {t('analytics.filters.applyFilters')}
        </button>
      </div>
    </form>
  </div>
</div>
