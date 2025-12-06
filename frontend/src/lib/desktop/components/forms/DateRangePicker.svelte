<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { formatDateForInput, formatDate } from '$lib/utils/formatters';
  import { parseLocalDateString } from '$lib/utils/date';
  import FormField from './FormField.svelte';
  import { XCircle } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  interface DatePreset {
    key: string;
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
    startLabel,
    endLabel,
    onChange,
    onStartChange,
    onEndChange,
  }: Props = $props();

  // Reactive labels with prop override capability
  let effectiveStartLabel = $derived(startLabel ?? t('forms.dateRange.labels.startDate'));
  let effectiveEndLabel = $derived(endLabel ?? t('forms.dateRange.labels.endDate'));

  // Convert dates to proper Date objects
  let startDateObj = $derived(startDate ? parseLocalDateString(startDate) : null);
  let endDateObj = $derived(endDate ? parseLocalDateString(endDate) : null);

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
        key: 'forms.dateRange.presets.today',
        getValue: () => ({
          startDate: today,
          endDate: today,
        }),
      },
      {
        key: 'forms.dateRange.presets.yesterday',
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
        key: 'forms.dateRange.presets.last7Days',
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
        key: 'forms.dateRange.presets.last30Days',
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
        key: 'forms.dateRange.presets.thisMonth',
        getValue: () => {
          const start = new Date(now.getFullYear(), now.getMonth(), 1);
          return {
            startDate: start,
            endDate: today,
          };
        },
      },
      {
        key: 'forms.dateRange.presets.lastMonth',
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
        key: 'forms.dateRange.presets.thisYear',
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
    const newDate = dateStr ? parseLocalDateString(dateStr) : null;

    if (newDate && isNaN(newDate.getTime())) {
      error = t('forms.dateRange.errors.invalidStartDate');
      return;
    }

    error = null;
    startDate = newDate;

    // Validate date range
    if (newDate && endDateObj && newDate > endDateObj) {
      error = t('forms.dateRange.errors.startAfterEnd');
    }

    onStartChange?.(newDate);
    onChange?.({ startDate: newDate, endDate: endDateObj });
  }

  function handleEndDateChange(value: string | number | boolean | string[]) {
    const dateStr = value as string;
    const newDate = dateStr ? parseLocalDateString(dateStr) : null;

    if (newDate && isNaN(newDate.getTime())) {
      error = t('forms.dateRange.errors.invalidEndDate');
      return;
    }

    error = null;
    endDate = newDate;

    // Validate date range
    if (newDate && startDateObj && newDate < startDateObj) {
      error = t('forms.dateRange.errors.endBeforeStart');
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
      label={effectiveStartLabel}
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
      label={effectiveEndLabel}
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
      <XCircle class="size-4" />
      <span class="text-sm">{error}</span>
    </div>
  {/if}

  {#if showPresets && presets.length > 0}
    <div class="mt-4">
      <div class="text-sm font-medium mb-2">{t('forms.dateRange.labels.quickSelect')}</div>
      <div class="flex flex-wrap gap-2">
        {#each presets as preset}
          <button
            type="button"
            class={cn('btn btn-sm', isPresetActive(preset) ? 'btn-primary' : 'btn-ghost')}
            {disabled}
            onclick={() => applyPreset(preset)}
          >
            {t(preset.key)}
          </button>
        {/each}

        {#if startDateObj || endDateObj}
          <button
            type="button"
            class="btn btn-sm btn-ghost text-error"
            {disabled}
            onclick={clearDates}
          >
            {t('common.buttons.clear')}
          </button>
        {/if}
      </div>
    </div>
  {/if}

  {#if startDateObj && endDateObj}
    <div class="mt-2 text-sm text-base-content/70">
      {t('forms.dateRange.labels.selected', {
        startDate: formatDate(startDateObj),
        endDate: formatDate(endDateObj),
      })}
    </div>
  {/if}
</div>
