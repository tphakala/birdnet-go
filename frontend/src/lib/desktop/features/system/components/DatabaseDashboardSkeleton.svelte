<!--
  DatabaseDashboardSkeleton - loading placeholder for the Database page.

  Mirrors the operational dashboard (metric strip, engine cards, detection rate
  and table breakdown) with the same grids and card chrome, so the strip and the
  engine row do not move when the data arrives. Bars sit inside elements with the
  loaded text's font size, so each line keeps its loaded height. The one loading
  label sits in the database tile header, where Connected appears once loaded.

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import Skeleton from '$lib/desktop/components/ui/Skeleton.svelte';
  import SkeletonStatus from '$lib/desktop/components/ui/SkeletonStatus.svelte';
  import { DETECTION_RATE_CHART_HEIGHT } from './detectionRateChartLayout';

  const CARD_CLASS =
    'bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm';
  const TILE_CLASS = `${CARD_CLASS} flex flex-col`;
  const TILE_HEADER_CLASS = 'flex flex-wrap items-center justify-between gap-x-2 gap-y-1 mb-3';
  /** A bar that sits on a line of text, so the line keeps the text's height. */
  const INLINE_BAR = 'inline-block align-middle';
  const SPARKLINE_TILE_COUNT = 3;
  const DATABASE_TILE_ROWS = 3;
  /** Label widths of the Database details rows, varied so they do not read as a table. */
  const DETAIL_LABEL_WIDTHS = ['w-16', 'w-16', 'w-20', 'w-20', 'w-24'];
  const WAL_ROW_COUNT = 4;
  const TABLE_COLUMN_COUNT = 4;
  const TABLE_ROW_COUNT = 8;
</script>

{#snippet tileTitle()}
  <div class="flex items-center gap-2">
    <Skeleton className="h-7 w-7 rounded-lg" />
    <span class="text-xs"><Skeleton className="{INLINE_BAR} h-3 w-20" /></span>
  </div>
{/snippet}

{#snippet cardTitle(width: string)}
  <div class="text-xs mb-3"><Skeleton className="{INLINE_BAR} h-3 {width}" /></div>
{/snippet}

{#snippet textRow(labelWidth: string, valueWidth: string)}
  <div class="flex justify-between gap-3 text-sm">
    <span><Skeleton className="{INLINE_BAR} h-3.5 {labelWidth}" /></span>
    <span><Skeleton className="{INLINE_BAR} h-3.5 {valueWidth}" /></span>
  </div>
{/snippet}

<div class="space-y-4" data-testid="database-skeleton">
  <!-- Metric strip: three sparkline tiles and the database tile -->
  <div class="@container">
    <div class="grid grid-cols-2 @4xl:grid-cols-4 gap-3">
      {#each { length: SPARKLINE_TILE_COUNT } as _, i (i)}
        <div class={TILE_CLASS}>
          <div class={TILE_HEADER_CLASS}>
            {@render tileTitle()}
            <span class="text-lg"><Skeleton className="{INLINE_BAR} h-5 w-16" /></span>
          </div>
          <!-- The sparkline keeps its 5:1 view box, which sets the tile height on narrow grids -->
          <div class="flex-1 min-h-[28px]">
            <Skeleton className="h-full w-full aspect-5/1" />
          </div>
          <div class="flex justify-between mt-2 text-[10px]">
            <span><Skeleton className="{INLINE_BAR} h-2 w-14" /></span>
            <span><Skeleton className="{INLINE_BAR} h-2 w-14" /></span>
          </div>
        </div>
      {/each}

      <div class={TILE_CLASS}>
        <div class={TILE_HEADER_CLASS}>
          {@render tileTitle()}
          <SkeletonStatus label={t('system.database.dashboard.loading')} className="text-xs" />
        </div>
        <div class="space-y-2 flex-1">
          {#each { length: DATABASE_TILE_ROWS } as _, i (i)}
            {@render textRow('w-16', 'w-20')}
          {/each}
        </div>
      </div>
    </div>
  </div>

  <!-- Engine row: database details and locks & WAL -->
  <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
    <div class={CARD_CLASS}>
      {@render cardTitle('w-32')}
      <div class="space-y-2.5">
        {#each DETAIL_LABEL_WIDTHS as width, i (i)}
          <div class="flex items-center gap-3">
            <Skeleton className="h-3.5 w-3.5 shrink-0" />
            <div class="flex-1">{@render textRow(width, 'w-12')}</div>
          </div>
        {/each}
      </div>
    </div>

    <div class={CARD_CLASS}>
      {@render cardTitle('w-24')}
      <div class="space-y-3">
        <div class="flex items-center gap-2">
          <Skeleton className="h-3.5 w-3.5 shrink-0" />
          <div class="flex-1">{@render textRow('w-28', 'w-8')}</div>
        </div>
        <div class="border-t border-[var(--border-100)]"></div>
        <div class="space-y-2">
          {#each { length: WAL_ROW_COUNT } as _, i (i)}
            {@render textRow('w-24', 'w-14')}
          {/each}
        </div>
      </div>
    </div>
  </div>

  <!-- Detection rate: chart block at the chart's height, then the min, avg and max line -->
  <div class={CARD_CLASS}>
    {@render cardTitle('w-36')}
    <div class="mb-2" style:height="{DETECTION_RATE_CHART_HEIGHT}px">
      <Skeleton className="h-full w-full" />
    </div>
    <div class="flex justify-between text-[10px]">
      <span><Skeleton className="{INLINE_BAR} h-2 w-12" /></span>
      <span><Skeleton className="{INLINE_BAR} h-2 w-14" /></span>
      <span><Skeleton className="{INLINE_BAR} h-2 w-14" /></span>
    </div>
  </div>

  <!-- Table breakdown: header, column headings and rows -->
  <div class={CARD_CLASS}>
    <div class="flex items-center justify-between mb-3 text-xs">
      <span><Skeleton className="{INLINE_BAR} h-3 w-24" /></span>
      <span><Skeleton className="{INLINE_BAR} h-3 w-20" /></span>
    </div>
    <div class="grid grid-cols-4 gap-x-3 border-b border-[var(--border-100)] py-2 px-3 text-xs">
      {#each { length: TABLE_COLUMN_COUNT } as _, i (i)}
        <span><Skeleton className="{INLINE_BAR} h-3 w-12" /></span>
      {/each}
    </div>
    {#each { length: TABLE_ROW_COUNT } as _, i (i)}
      <div
        class="grid grid-cols-4 gap-x-3 border-b border-[var(--border-100)] last:border-b-0 py-2 px-3 text-sm"
      >
        <span><Skeleton className="{INLINE_BAR} h-3 w-40" /></span>
        <span><Skeleton className="{INLINE_BAR} h-3 w-16" /></span>
        <span><Skeleton className="{INLINE_BAR} h-3 w-12" /></span>
        <span><Skeleton className="{INLINE_BAR} h-1.5 w-16 rounded-full" /></span>
      </div>
    {/each}
  </div>
</div>
