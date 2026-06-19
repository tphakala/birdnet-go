<!--
  ChartCard: shared chrome for every analytics chart.

  Owns the card frame, title/description, optional per-card options toolbar and
  export-menu stub, and the full state matrix: loading, error (with retry),
  empty ("no data for these filters"), a distinct "not enough data yet" state
  driven by `minDataPoints`, and the chart itself. It drives the registry
  `fetch` from the shared params and maps the result onto the chart component
  via the registry `mapProps`, so individual charts no longer carry their own
  loading/empty markup.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { Inbox, Sprout, TriangleAlert, RefreshCw, Download } from '@lucide/svelte';

  import { t } from '$lib/i18n';
  import { getLogger } from '$lib/utils/logger';
  import { formatDateForAPI } from '../registry/analyticsParams';
  import type { AnalyticsParams, ChartDef } from '../registry/types';

  interface Props {
    chart: ChartDef;
    params: AnalyticsParams;
    /** Scientific -> common name map (for series labels). */
    speciesNames?: Map<string, string>;
    /**
     * True while the hub is still loading the species list. Used to suppress the
     * empty state for species-driven charts before the top-species auto-select
     * lands, so a fresh load shows a continuous spinner instead of flashing
     * "no data" (matches the legacy page's hold-until-species-resolved behavior).
     */
    speciesLoading?: boolean;
    /** Lets a chart request shared-param changes (e.g. brush selecting a range). */
    onParamsChange?: (_partial: Partial<AnalyticsParams>) => void;
  }

  let { chart, params, speciesNames, speciesLoading = false, onParamsChange }: Props = $props();

  const logger = getLogger('analytics-chart-card');

  // Per-card display options (e.g. the trend chart's relative/zoom/brush toggles).
  // ChartCard owns the state; the controls component reads it and reports changes
  // via setOption. Seed once from the registry; `chart` is stable per card
  // instance (it is keyed by id in the parent {#each}).
  let options = $state<Record<string, unknown>>(
    untrack(() => ({ ...(chart.defaultOptions ?? {}) }))
  );
  function setOption(key: string, value: unknown): void {
    options = { ...options, [key]: value };
  }

  // Fetch lifecycle state.
  let loading = $state(true);
  let error = $state<string | null>(null);
  let result = $state<unknown>(null);
  let reloadNonce = $state(0);
  let controller: AbortController | null = null;

  // The fetch only depends on the params the chart actually consumes. Charts
  // that ignore species/source (e.g. diversity) do not refetch when those
  // change. Keyed on the resolved dates so it reflects exactly what is fetched.
  const fetchKey = $derived(
    JSON.stringify({
      start: formatDateForAPI(params.startDate),
      end: formatDateForAPI(params.endDate),
      species: chart.supports.species ? [...params.species].sort() : [],
      source: chart.supports.source ? params.source : '',
      nonce: reloadNonce,
    })
  );

  $effect(() => {
    // Track only fetchKey; read the live params untracked so options/tab changes
    // never trigger a refetch.
    void fetchKey;
    const currentParams = untrack(() => params);
    const currentChart = untrack(() => chart);

    controller?.abort();
    const ac = new AbortController();
    controller = ac;

    loading = true;
    error = null;

    currentChart
      .fetch(currentParams, ac.signal)
      .then(data => {
        if (ac.signal.aborted) return;
        result = data;
        loading = false;
      })
      .catch((err: unknown) => {
        if (ac.signal.aborted || (err instanceof Error && err.name === 'AbortError')) return;
        logger.error('Chart fetch failed', err, { chart: currentChart.id });
        error = t('analytics.errors.loadFailed');
        loading = false;
      });

    return () => ac.abort();
  });

  function retry(): void {
    reloadNonce += 1;
  }

  // Number of data points in the current result, for the empty / not-enough checks.
  const dataCount = $derived.by(() => {
    if (result == null) return 0;
    if (chart.countDataPoints) return chart.countDataPoints(result);
    return Array.isArray(result) ? result.length : 0;
  });

  // A species-driven chart with no selection yet, while the hub is still loading
  // the species list, is mid-auto-select: keep the spinner up instead of flashing
  // the empty state.
  const isPending = $derived(
    chart.supports.species && params.species.length === 0 && speciesLoading
  );

  const hasResult = $derived(result !== null);
  const isEmpty = $derived(hasResult && dataCount === 0 && !isPending);
  const isNotEnough = $derived(
    hasResult && chart.minDataPoints != null && dataCount > 0 && dataCount < chart.minDataPoints
  );
  const showChart = $derived(hasResult && !isEmpty && !isNotEnough);

  // Map the fetched data + params (+ options/callbacks) onto the chart component's props.
  const resolvedProps = $derived.by<Record<string, unknown> | null>(() => {
    if (!showChart) return null;
    const ctx = {
      options,
      onParamsChange: onParamsChange ?? (() => {}),
      speciesNames: speciesNames ?? new Map<string, string>(),
    };
    return chart.mapProps ? chart.mapProps(result, params, ctx) : { data: result };
  });

  const titleId = $derived(`chart-card-${chart.id}-title`);
  const exportHintId = $derived(`chart-card-${chart.id}-export-hint`);
</script>

<section
  id={chart.id}
  class="bg-[var(--color-base-100)] rounded-xl shadow-sm border border-[var(--color-base-200)]"
  aria-labelledby={titleId}
>
  <div class="p-6">
    <!-- Header: title/description on the left, per-card controls + export on the right -->
    <div class="flex flex-wrap items-start justify-between gap-x-4 gap-y-2 mb-4">
      <div class="min-w-0">
        <h3 id={titleId} class="text-lg font-semibold">{t(chart.titleKey)}</h3>
        <p class="text-sm text-[var(--color-base-content)] opacity-70">{t(chart.descKey)}</p>
      </div>

      <div class="flex flex-wrap items-center gap-3 shrink-0">
        {#if chart.controls}
          {@const Controls = chart.controls}
          <Controls {options} {setOption} />
        {/if}

        {#if chart.export}
          <!-- Export menu stub: real CSV export arrives with the new endpoints. -->
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium border border-[var(--color-base-200)] text-[var(--color-base-content)] opacity-60 cursor-not-allowed"
            disabled
            title={t('analytics.hub.card.exportComingSoon')}
            aria-describedby={exportHintId}
          >
            <Download class="h-4 w-4" />
            <span>{t('analytics.hub.card.export')}</span>
          </button>
          <span id={exportHintId} class="sr-only">{t('analytics.hub.card.exportComingSoon')}</span>
        {/if}
      </div>
    </div>

    <!-- Body: state machine + chart, with a loading overlay that preserves the chart underneath -->
    <div class="h-96 relative">
      {#if error}
        <div class="absolute inset-0 flex items-center justify-center rounded-lg" role="alert">
          <div class="text-center max-w-sm px-4">
            <TriangleAlert class="h-10 w-10 mx-auto mb-3 text-[var(--color-error)]" />
            <p class="text-lg mb-1">{t('analytics.hub.card.error')}</p>
            <p class="text-sm text-[var(--color-base-content)] opacity-70 mb-4">{error}</p>
            <button
              type="button"
              class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium border border-[var(--color-base-300)] hover:bg-[var(--color-base-200)]"
              onclick={retry}
            >
              <RefreshCw class="h-4 w-4" />
              <span>{t('analytics.hub.card.retry')}</span>
            </button>
          </div>
        </div>
      {:else if isEmpty}
        <div
          class="absolute inset-0 flex items-center justify-center text-[var(--color-base-content)] opacity-60 rounded-lg"
          role="status"
        >
          <div class="text-center px-4">
            <Inbox class="h-10 w-10 mx-auto mb-3 opacity-70" />
            <p class="text-lg mb-2">{t(chart.emptyKey)}</p>
            <p class="text-sm">{t(chart.emptyHintKey)}</p>
          </div>
        </div>
      {:else if isNotEnough}
        <div
          class="absolute inset-0 flex items-center justify-center text-[var(--color-base-content)] opacity-70 rounded-lg"
          role="status"
        >
          <div class="text-center px-4">
            <Sprout class="h-10 w-10 mx-auto mb-3 text-[var(--color-success)] opacity-80" />
            <p class="text-lg mb-2">{t('analytics.hub.card.notEnoughData')}</p>
            <p class="text-sm">
              {t('analytics.hub.card.notEnoughDataHint', { min: chart.minDataPoints ?? 0 })}
            </p>
          </div>
        </div>
      {:else if resolvedProps}
        {@const ChartComponent = chart.component}
        <ChartComponent {...resolvedProps} />
      {/if}

      {#if loading || isPending}
        <div
          class="absolute inset-0 bg-[var(--color-base-100)]/80 backdrop-blur-xs flex items-center justify-center rounded-lg"
          role="status"
          aria-busy="true"
          aria-label={t('analytics.advanced.aria.loadingAnalytics')}
        >
          <div
            class="w-12 h-12 border-4 border-[var(--color-primary)] border-t-transparent rounded-full animate-spin"
          ></div>
        </div>
      {/if}
    </div>
  </div>
</section>
