<script lang="ts">
  import type { Snippet } from 'svelte';
  import { t, type TranslationKey } from '$lib/i18n';
  import AnalyticsControlBar from './AnalyticsControlBar.svelte';
  import { analyticsControls } from '../registry/analyticsControls.svelte';
  import { chartsForGroup } from '../registry/charts';
  import type { ChartGroup } from '../registry/types';

  interface Props {
    titleKey: TranslationKey;
    group: ChartGroup;
    children: Snippet;
  }

  let { titleKey, group, children }: Props = $props();

  // 'overview' (Summary) has no registry charts; chartsForGroup returns [] so
  // both applicability flags are false and only the date-range control shows.
  const groupCharts = $derived(chartsForGroup(group));
  const speciesApplicable = $derived(groupCharts.some(c => c.supports.species));
  const sourceApplicable = $derived(groupCharts.some(c => c.supports.source));

  // Register the single popstate listener for the lifetime this shell is mounted.
  $effect(() => analyticsControls.init());

  // Load the filter option lists the active view needs; re-runs when the range
  // changes (ensureSpecies dedupes by range key, ensureSources fetches once).
  $effect(() => {
    // Touch the range so a range change re-triggers the species fetch.
    void analyticsControls.params.startDate;
    void analyticsControls.params.endDate;
    if (speciesApplicable) analyticsControls.ensureSpecies();
    if (sourceApplicable) analyticsControls.ensureSources();
  });
</script>

<section class="flex flex-col gap-4" aria-labelledby="analytics-page-title">
  <h1 id="analytics-page-title" class="text-2xl font-bold text-[var(--color-base-content)]">
    {t(titleKey)}
  </h1>

  <AnalyticsControlBar
    params={analyticsControls.params}
    availableSpecies={analyticsControls.availableSpecies}
    loadingSpecies={analyticsControls.loadingSpecies}
    {speciesApplicable}
    availableSources={analyticsControls.availableSources}
    loadingSources={analyticsControls.loadingSources}
    {sourceApplicable}
    onParamsChange={partial => analyticsControls.applyParams(partial, 'push')}
  />

  {@render children()}
</section>
