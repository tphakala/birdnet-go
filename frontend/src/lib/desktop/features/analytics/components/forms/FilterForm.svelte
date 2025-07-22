<script lang="ts">
  import FormField from '$lib/desktop/components/forms/FormField.svelte';
  import Select from '$lib/desktop/components/ui/Select.svelte';
  import Input from '$lib/desktop/components/ui/Input.svelte';

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
    { value: 'all', label: 'All Time' },
    { value: 'today', label: 'Today' },
    { value: 'week', label: 'Last 7 Days' },
    { value: 'month', label: 'Last 30 Days' },
    { value: '90days', label: 'Last 90 Days' },
    { value: 'year', label: 'Last 12 Months' },
    { value: 'custom', label: 'Custom Range' },
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
    <h2 class="card-title">Filter Data</h2>

    <form class="space-y-4" onsubmit={handleSubmit}>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <!-- Time Period Filter -->
        <FormField label="Time Period" id="timePeriod">
          <Select id="timePeriod" bind:value={filters.timePeriod} options={timePeriodOptions} />
        </FormField>

        <!-- Custom Date Range -->
        {#if filters.timePeriod === 'custom'}
          <FormField label="From" id="startDate">
            <Input type="date" id="startDate" bind:value={filters.startDate} />
          </FormField>

          <FormField label="To" id="endDate">
            <Input type="date" id="endDate" bind:value={filters.endDate} />
          </FormField>
        {/if}
      </div>

      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-ghost" onclick={handleReset} disabled={isLoading}>
          Reset
        </button>
        <button type="submit" class="btn btn-primary" disabled={isLoading}>
          {#if isLoading}
            <span class="loading loading-spinner loading-sm"></span>
          {/if}
          Apply Filters
        </button>
      </div>
    </form>
  </div>
</div>
