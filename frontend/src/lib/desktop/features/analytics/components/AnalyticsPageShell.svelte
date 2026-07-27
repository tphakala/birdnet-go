<script lang="ts">
  import type { Snippet } from 'svelte';
  import { untrack } from 'svelte';
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

  // Apply any filter query carried in the URL we mounted on, then register the
  // popstate listener. syncFromUrl runs first so an in-session deep link (e.g.
  // /ui/analytics/trends?range=year) is honored on mount. With
  // Part A sidebar links carry the active query, so a sidebar navigation lands
  // on a URL that already encodes the current filters - syncFromUrl reads the
  // same filters back and persistence is preserved.
  $effect(() => {
    analyticsControls.syncFromUrl(); // honor filter query carried in the URL we mounted on
    return analyticsControls.init(); // register the ref-counted popstate listener; its cleanup is the teardown
  });

  // Load the filter option lists the active view needs; re-runs when the range
  // changes (ensureSpecies dedupes by range key, ensureSources fetches once).
  $effect(() => {
    // Touch the range so a range change re-triggers the species fetch.
    void analyticsControls.params.startDate;
    void analyticsControls.params.endDate;
    const reqSpecies = speciesApplicable;
    const reqSource = sourceApplicable;
    untrack(() => {
      if (reqSpecies) analyticsControls.ensureSpecies();
      if (reqSource) analyticsControls.ensureSources();
    });
  });
</script>

<section class="col-span-12 flex flex-col gap-4" aria-label={t(titleKey)}>
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
