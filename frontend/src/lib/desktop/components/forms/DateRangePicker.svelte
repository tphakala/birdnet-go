<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { formatDateForInput, formatDate } from '$lib/utils/formatters';
  import FormField from './FormField.svelte';

  interface DatePreset {
    label: string;
    getValue: () => { startDate: Date; endDate: Date };
  }

  interface Props {
    startDate?: Date | string | null;
    endDate?: Date | string | null;
    minDate?: Date | string;
    maxDate?: Date | string;
    presets?: DatePreset[];
    showPresets?: boolean;
    required?: boolean;
    disabled?: boolean;
    className?: string;
    startLabel?: string;
    endLabel?: string;
    onChange?: (_dates: { startDate: Date | null; endDate: Date | null }) => void;
    onStartChange?: (_date: Date | null) => void;
    onEndChange?: (_date: Date | null) => void;
  }

  let {
    startDate = $bindable(null),
    endDate = $bindable(null),
    minDate,
    maxDate,
    presets = getDefaultPresets(),
    showPresets = true,
    required = false,
    disabled = false,
    className = '',
    startLabel = 'Start Date',
    endLabel = 'End Date',
    onChange,
    onStartChange,
    onEndChange,
  }: Props = $props();

  // Convert dates to proper Date objects
  let startDateObj = $derived(startDate ? new Date(startDate) : null);
  let endDateObj = $derived(endDate ? new Date(endDate) : null);

  // Format dates for input fields
  let startDateFormatted = $derived(formatDateForInput(startDateObj));
  let endDateFormatted = $derived(formatDateForInput(endDateObj));

  // Validation states
  let error = $state<string | null>(null);

  // Derived min/max for date inputs
  let minDateFormatted = $derived(minDate ? formatDateForInput(minDate) : undefined);
  let maxDateFormatted = $derived(maxDate ? formatDateForInput(maxDate) : undefined);

  // Ensure end date is not before start date
  let endMinDate = $derived(startDateFormatted || minDateFormatted);

  function getDefaultPresets(): DatePreset[] {
    const now = new Date();
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());

    return [
      {
        label: 'Today',
        getValue: () => ({
          startDate: today,
          endDate: today,
        }),
      },
      {
        label: 'Yesterday',
        getValue: () => {
          const yesterday = new Date(today);
          yesterday.setDate(yesterday.getDate() - 1);
          return {
            startDate: yesterday,
            endDate: yesterday,
          };
        },
      },
      {
        label: 'Last 7 days',
        getValue: () => {
          const start = new Date(today);
          start.setDate(start.getDate() - 6);
          return {
            startDate: start,
            endDate: today,
          };
        },
      },
      {
        label: 'Last 30 days',
        getValue: () => {
          const start = new Date(today);
          start.setDate(start.getDate() - 29);
          return {
            startDate: start,
            endDate: today,
          };
        },
      },
      {
        label: 'This month',
        getValue: () => {
          const start = new Date(now.getFullYear(), now.getMonth(), 1);
          return {
            startDate: start,
            endDate: today,
          };
        },
      },
      {
        label: 'Last month',
        getValue: () => {
          const start = new Date(now.getFullYear(), now.getMonth() - 1, 1);
          const end = new Date(now.getFullYear(), now.getMonth(), 0);
          return {
            startDate: start,
            endDate: end,
          };
        },
      },
      {
        label: 'This year',
        getValue: () => {
          const start = new Date(now.getFullYear(), 0, 1);
          return {
            startDate: start,
            endDate: today,
          };
        },
      },
    ];
  }

  function handleStartDateChange(value: string | number | boolean | string[]) {
    const dateStr = value as string;
    const newDate = dateStr ? new Date(dateStr) : null;

    if (newDate && isNaN(newDate.getTime())) {
      error = 'Invalid start date';
      return;
    }

    error = null;
    startDate = newDate;

    // Validate date range
    if (newDate && endDateObj && newDate > endDateObj) {
      error = 'Start date cannot be after end date';
    }

    onStartChange?.(newDate);
    onChange?.({ startDate: newDate, endDate: endDateObj });
  }

  function handleEndDateChange(value: string | number | boolean | string[]) {
    const dateStr = value as string;
    const newDate = dateStr ? new Date(dateStr) : null;

    if (newDate && isNaN(newDate.getTime())) {
      error = 'Invalid end date';
      return;
    }

    error = null;
    endDate = newDate;

    // Validate date range
    if (newDate && startDateObj && newDate < startDateObj) {
      error = 'End date cannot be before start date';
    }

    onEndChange?.(newDate);
    onChange?.({ startDate: startDateObj, endDate: newDate });
  }

  function applyPreset(preset: DatePreset) {
    const { startDate: presetStart, endDate: presetEnd } = preset.getValue();

    error = null;
    startDate = presetStart;
    endDate = presetEnd;

    onStartChange?.(presetStart);
    onEndChange?.(presetEnd);
    onChange?.({ startDate: presetStart, endDate: presetEnd });
  }

  function clearDates() {
    error = null;
    startDate = null;
    endDate = null;

    onStartChange?.(null);
    onEndChange?.(null);
    onChange?.({ startDate: null, endDate: null });
  }

  // Check if a preset is currently active
  function isPresetActive(preset: DatePreset): boolean {
    if (!startDateObj || !endDateObj) return false;

    const { startDate: presetStart, endDate: presetEnd } = preset.getValue();

    // Use getTime() for timezone-safe date comparison
    return (
      startDateObj.getTime() === presetStart.getTime() &&
      endDateObj.getTime() === presetEnd.getTime()
    );
  }
</script>

<div class={cn('date-range-picker', className)}>
  <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
    <FormField
      type="date"
      name="startDate"
      label={startLabel}
      value={startDateFormatted}
      min={minDateFormatted}
      max={maxDateFormatted}
      {required}
      {disabled}
      onChange={handleStartDateChange}
    />

    <FormField
      type="date"
      name="endDate"
      label={endLabel}
      value={endDateFormatted}
      min={endMinDate}
      max={maxDateFormatted}
      {required}
      {disabled}
      onChange={handleEndDateChange}
    />
  </div>

  {#if error}
    <div class="alert alert-error mt-2">
      <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          stroke-width="2"
          d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
        />
      </svg>
      <span class="text-sm">{error}</span>
    </div>
  {/if}

  {#if showPresets && presets.length > 0}
    <div class="mt-4">
      <div class="text-sm font-medium mb-2">Quick Select</div>
      <div class="flex flex-wrap gap-2">
        {#each presets as preset}
          <button
            type="button"
            class={cn('btn btn-sm', isPresetActive(preset) ? 'btn-primary' : 'btn-ghost')}
            {disabled}
            onclick={() => applyPreset(preset)}
          >
            {preset.label}
          </button>
        {/each}

        {#if startDateObj || endDateObj}
          <button
            type="button"
            class="btn btn-sm btn-ghost text-error"
            {disabled}
            onclick={clearDates}
          >
            Clear
          </button>
        {/if}
      </div>
    </div>
  {/if}

  {#if startDateObj && endDateObj}
    <div class="mt-2 text-sm text-base-content/70">
      Selected: {formatDate(startDateObj)} - {formatDate(endDateObj)}
    </div>
  {/if}
</div>
