<!--
  Per-card display options for the Daily Species Trend chart.

  Rendered by ChartCard in the card header for charts that declare `controls`.
  Keeps chart-specific toggles (relative trends, zoom/pan, brush selection) out
  of the shared AnalyticsControlBar while preserving the legacy behavior. State
  is owned by ChartCard; this component reads `options` and reports changes via
  `setOption` (it never mutates `options` directly).
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import type { ChartControlsProps } from '../registry/types';

  let { options, setOption }: ChartControlsProps = $props();
</script>

<div
  class="flex flex-wrap items-center gap-x-4 gap-y-1"
  role="group"
  aria-label={t('analytics.advanced.chartOptions')}
>
  <Checkbox
    checked={Boolean(options.showRelative)}
    onchange={checked => setOption('showRelative', checked)}
    label={t('analytics.advanced.options.relativeTrends')}
    size="sm"
  />
  <Checkbox
    checked={Boolean(options.enableZoom)}
    onchange={checked => setOption('enableZoom', checked)}
    label={t('analytics.advanced.options.zoomPan')}
    size="sm"
  />
  <Checkbox
    checked={Boolean(options.enableBrush)}
    onchange={checked => setOption('enableBrush', checked)}
    label={t('analytics.advanced.options.brushSelection')}
    size="sm"
  />
</div>
