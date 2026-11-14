<!--
  SpeciesHistoryModal.svelte - Daily detection counts for one species over a chosen period.

  Reusable anywhere a species is shown: pass the species and open/close it. Data comes from
  GET /api/v2/analytics/time/daily and is drawn with the analytics LineChart.
-->
<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import LineChart, { type LineSeries } from './charts/d3/LineChart.svelte';
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import { addDays, getLocalDateString, parseLocalDateString } from '$lib/utils/date';
  import { loggers } from '$lib/utils/logger';
  import { fillDailyCounts } from './speciesHistory';

  const logger = loggers.ui;

  interface Props {
    isOpen: boolean;
    scientificName: string;
    /** Name shown in the title (localized common name). */
    displayName: string;
    /** Last day shown (YYYY-MM-DD), e.g. the dashboard's selected date; defaults to today. */
    endDate?: string;
    onClose: () => void;
  }

  let {
    isOpen,
    scientificName,
    displayName,
    endDate = getLocalDateString(),
    onClose,
  }: Props = $props();

  interface DailyCountResponse {
    data?: { date: string; count: number }[];
  }

  /** Selectable periods, in days back from today. */
  const PERIODS = [
    { days: 30, label: () => t('analytics.periods.lastMonth') },
    { days: 90, label: () => t('analytics.periods.last90Days') },
    { days: 365, label: () => t('analytics.periods.lastYear') },
  ] as const;

  let periodDays = $state<number>(PERIODS[0].days);
  let series = $state<LineSeries[]>([]);
  let status = $state<'loading' | 'loaded' | 'error'>('loading');

  // The period includes today, so it starts periodDays - 1 days back.
  const startDate = $derived(addDays(endDate, -(periodDays - 1)));
  const dateRange = $derived.by((): [Date, Date] | undefined => {
    const start = parseLocalDateString(startDate);
    const end = parseLocalDateString(endDate);
    return start && end ? [start, end] : undefined;
  });

  // Refetch whenever the modal opens or the species/period changes; a stale response
  // from a superseded request is dropped.
  $effect(() => {
    if (!isOpen) return;
    const search = new URLSearchParams({
      species: scientificName,
      start_date: startDate,
      end_date: endDate,
    });
    let cancelled = false;
    status = 'loading';
    api
      .get<DailyCountResponse>(`/api/v2/analytics/time/daily?${search}`)
      .then(response => {
        if (cancelled) return;
        const points = fillDailyCounts(response?.data ?? [], startDate, endDate);
        series = [{ id: scientificName, label: displayName, data: points }];
        status = 'loaded';
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        logger.error('Failed to load species history', error, { component: 'SpeciesHistoryModal' });
        status = 'error';
      });
    return () => {
      cancelled = true;
    };
  });
</script>

<Modal {isOpen} title={displayName} size="3xl" {onClose}>
  <div class="flex flex-wrap gap-2 mb-4">
    {#each PERIODS as period (period.days)}
      <button
        type="button"
        class="btn btn-sm {periodDays === period.days ? 'btn-primary' : 'btn-ghost'}"
        aria-pressed={periodDays === period.days}
        onclick={() => (periodDays = period.days)}
      >
        {period.label()}
      </button>
    {/each}
  </div>

  <div class="h-80">
    {#if status === 'loading'}
      <p class="text-sm text-[var(--color-base-content)]/60" role="status">{t('common.loading')}</p>
    {:else if status === 'error'}
      <p class="text-sm text-[var(--color-error)]" role="alert">{t('common.ui.error')}</p>
    {:else}
      <LineChart
        {series}
        {dateRange}
        valueAxisLabel={t('analytics.charts.numberOfDetections')}
        dateAxisLabel={t('analytics.charts.date')}
        valueTooltipLabel={t('analytics.charts.detections')}
        ariaLabel={displayName}
      />
    {/if}
  </div>
</Modal>
