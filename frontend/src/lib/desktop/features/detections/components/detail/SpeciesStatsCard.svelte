<!--
  SpeciesStatsCard - how this species behaves at this station: range filter
  score, totals, first and last heard, confidence, and daily counts for the
  weeks before the detection.
-->
<script lang="ts">
  import { ArrowRight, TriangleAlert } from '@lucide/svelte';
  import Skeleton from '$lib/desktop/components/ui/Skeleton.svelte';
  import SkeletonStatus from '$lib/desktop/components/ui/SkeletonStatus.svelte';
  import { t } from '$lib/i18n';
  import { handleAppLinkClick } from '$lib/stores/navigation.svelte';
  import { addDays } from '$lib/utils/date';
  import { buildSpeciesDetectionUrl } from '$lib/utils/detectionUrls';
  import DetailCard from './DetailCard.svelte';
  import { barHeight, formatCount, formatPercent, PERCENT, toPercent } from './detailFormat';
  import { dailyWindowStart, formatCalendarDate } from './detailTime';
  import type {
    DailyCounts,
    DetectionDetailData,
    SpeciesRarity,
    SpeciesStats,
  } from './detailTypes';

  interface Props {
    detection: DetectionDetailData;
    speciesName: string;
    rarity?: SpeciesRarity | null;
    /** All-time totals, times, confidences and review counts; null until known. */
    stats?: SpeciesStats | null;
    /** Daily counts for the days leading up to the detection. */
    daily?: DailyCounts | null;
    /** Number of days in the daily counts window, ending on the detection's day. */
    dailyDays: number;
    /** Which parts are still being requested; each shows placeholders until it answers. */
    loading?: Partial<Record<'rarity' | 'stats' | 'daily', boolean>>;
  }

  let {
    detection,
    speciesName,
    rarity = null,
    stats = null,
    daily = null,
    dailyDays,
    loading = {},
  }: Props = $props();

  // A species is called out as often rejected once at least this many of its
  // detections were reviewed and this share of them were false positives.
  const MIN_REVIEWS_FOR_WARNING = 3;
  const FALSE_POSITIVE_WARNING_SHARE = 0.5;
  const MISSING = '-';

  // Placeholders stand in for a part only while it has nothing to show yet.
  let pending = $derived({
    rarity: Boolean(loading.rarity) && rarity === null,
    stats: Boolean(loading.stats) && stats === null,
    daily: Boolean(loading.daily) && daily === null,
  });
  let anyPending = $derived(Object.values(pending).some(Boolean));

  // How reviewers judged this species' detections.
  let reviews = $derived(stats?.reviews ?? null);
  let reviewed = $derived(reviews ? reviews.correct + reviews.falsePositive : 0);
  let falsePositiveShare = $derived(reviews && reviewed > 0 ? reviews.falsePositive / reviewed : 0);
  let oftenRejected = $derived(
    reviewed >= MIN_REVIEWS_FOR_WARNING && falsePositiveShare >= FALSE_POSITIVE_WARNING_SHARE
  );

  // One bar per calendar day of the window, including days without detections.
  let dailyBars = $derived.by(() => {
    const counts = new Map((daily?.data ?? []).map(entry => [entry.date, entry.count]));
    let bars: { date: string; label: string; count: number }[];
    try {
      const start = dailyWindowStart(detection.date, dailyDays);
      bars = Array.from({ length: dailyDays }, (_, index) => {
        const date = addDays(start, index);
        return { date, label: formatCalendarDate(date, true), count: counts.get(date) ?? 0 };
      });
    } catch {
      // A malformed detection date leaves the chart out.
      return [];
    }
    return bars;
  });

  let dailyPeak = $derived(Math.max(1, ...dailyBars.map(bar => bar.count)));

  let rarityText = $derived(
    rarity ? `${t(`species.rarity.statuses.${rarity.status}`)}, ${formatPercent(rarity.score)}` : ''
  );
  // The track is a meter only once there is a score to show.
  let meterAttributes = $derived(
    rarity
      ? {
          role: 'meter',
          'aria-valuemin': 0,
          'aria-valuemax': PERCENT,
          'aria-valuenow': toPercent(rarity.score),
          'aria-valuetext': rarityText,
          'aria-label': t('detections.detail.stats.rangeScore'),
        }
      : {}
  );

  let windowTotal = $derived(dailyBars.reduce((sum, bar) => sum + bar.count, 0));

  let speciesUrl = $derived(buildSpeciesDetectionUrl(detection.scientificName, detection.date));

  function heard(timestamp: string | null | undefined): string {
    return timestamp ? formatCalendarDate(timestamp) : MISSING;
  }
</script>

<!-- A value still on its way: a bar in place of the text, the same line height. -->
{#snippet value(waiting: boolean, text: string, width = 'w-16')}
  {#if waiting}
    <Skeleton inline className="h-4 {width}" />
  {:else}
    {text}
  {/if}
{/snippet}

<DetailCard title={t('detections.detail.stats.title')} subtitle={speciesName}>
  {#snippet header()}
    {#if anyPending}
      <SkeletonStatus label={t('common.loading')} className="ml-auto text-xs" />
    {/if}
  {/snippet}

  {#if rarity || pending.rarity}
    <div class="dd-rarity">
      <div class="dd-rarity-head">
        <span class="dd-rarity-label">{t('detections.detail.stats.rangeScore')}</span>
        {#if rarity}
          <span class="dd-rarity-status">
            {t(`species.rarity.statuses.${rarity.status}`)}
            <strong>{formatPercent(rarity.score)}</strong>
          </span>
        {:else}
          <Skeleton inline className="h-3 w-24" />
        {/if}
      </div>
      <div class="dd-rarity-track" {...meterAttributes}>
        {#if rarity}
          <span class="dd-rarity-fill" style:width="{rarity.score * PERCENT}%"></span>
        {/if}
      </div>
      <p class="dd-rarity-note">
        {#if rarity}
          {rarity.date
            ? t('detections.detail.stats.rangeScoreNote', { date: rarity.date })
            : t('detections.detail.stats.rangeScoreNoteToday')}
        {:else}
          <Skeleton inline className="h-2.5 w-48" />
        {/if}
      </p>
    </div>
  {/if}

  <dl class="dd-stats">
    <div class="dd-stat">
      <dt>{t('detections.detail.stats.total')}</dt>
      <dd>{@render value(pending.stats, stats ? formatCount(stats.count) : MISSING)}</dd>
    </div>
    <div class="dd-stat">
      <dt>{t('detections.detail.stats.windowTotal', { days: dailyDays })}</dt>
      <dd>
        {@render value(pending.daily, daily ? formatCount(windowTotal) : MISSING)}
      </dd>
    </div>
    <div class="dd-stat">
      <dt>{t('detections.detail.stats.firstHeard')}</dt>
      <dd class="dd-stat-text">
        {@render value(pending.stats, heard(stats?.firstHeard), 'w-28')}
      </dd>
    </div>
    <div class="dd-stat">
      <dt>{t('detections.detail.stats.lastHeard')}</dt>
      <dd class="dd-stat-text">
        {@render value(pending.stats, heard(stats?.lastHeard), 'w-28')}
      </dd>
    </div>
    <div class="dd-stat">
      <dt>{t('detections.detail.stats.avgConfidence')}</dt>
      <dd>{@render value(pending.stats, formatPercent(stats?.avgConfidence, MISSING))}</dd>
    </div>
    <div class="dd-stat">
      <dt>{t('detections.detail.stats.maxConfidence')}</dt>
      <dd>{@render value(pending.stats, formatPercent(stats?.maxConfidence, MISSING))}</dd>
    </div>
  </dl>

  {#if reviews || pending.stats}
    <div class="dd-reviews">
      <div class="dd-reviews-head">
        <span>{t('detections.detail.stats.reviewsTitle')}</span>
        {#if reviews}
          <span class="dd-reviews-count">
            {t('detections.detail.stats.reviewsCount', { count: reviewed })}
          </span>
        {:else}
          <Skeleton inline className="h-3 w-20" />
        {/if}
      </div>
      {#if !reviews}
        <div class="dd-reviews-bar"></div>
        <p class="dd-reviews-legend"><Skeleton inline className="h-3 w-40" /></p>
      {:else if reviewed > 0}
        <div class="dd-reviews-bar" aria-hidden="true">
          <span class="dd-reviews-correct" style:width="{(1 - falsePositiveShare) * PERCENT}%"
          ></span>
          <span class="dd-reviews-false" style:width="{falsePositiveShare * PERCENT}%"></span>
        </div>
        <p class="dd-reviews-legend">
          <span class="dd-reviews-key dd-reviews-key-correct"
            >{t('detections.detail.stats.reviewsCorrect', { count: reviews.correct })}</span
          >
          <span class="dd-reviews-key dd-reviews-key-false"
            >{t('detections.detail.stats.reviewsFalse', { count: reviews.falsePositive })}</span
          >
        </p>
        {#if oftenRejected}
          <p class="dd-reviews-warning">
            <TriangleAlert class="size-3.5" />
            {t('detections.detail.stats.oftenRejected', {
              percent: toPercent(falsePositiveShare),
            })}
          </p>
        {/if}
      {:else}
        <p class="dd-reviews-none">{t('detections.detail.stats.reviewsNone')}</p>
      {/if}
    </div>
  {/if}

  <!-- Left out when the daily counts did not load: no bars is not the same as zero. -->
  {#if dailyBars.length > 0 && (daily || pending.daily)}
    <div class="dd-daily">
      <div class="dd-daily-head">
        <span>{t('detections.detail.stats.dailyTitle', { days: dailyDays })}</span>
      </div>
      {#if pending.daily}
        <div class="dd-daily-bars"><Skeleton className="h-full w-full rounded-none" /></div>
      {:else}
        <!-- The bars are drawn for the eye; the table below carries their values. -->
        <div class="dd-daily-bars" aria-hidden="true">
          {#each dailyBars as bar (bar.date)}
            <span
              class="dd-daily-bar"
              class:dd-daily-bar-now={bar.date === detection.date}
              style:height="{barHeight(bar.count, dailyPeak)}%"
              title="{bar.label}  {bar.count}"
            ></span>
          {/each}
        </div>
        <!-- A table is as tall as its rows whatever its height, so the wrapper hides it. -->
        <div class="sr-only">
          <table>
            <caption>
              {t('detections.detail.stats.dailyLabel', { days: dailyDays, count: windowTotal })}
            </caption>
            <thead>
              <tr>
                <th scope="col">{t('detections.detail.identity.date')}</th>
                <th scope="col">{t('analytics.charts.detections')}</th>
              </tr>
            </thead>
            <tbody>
              {#each dailyBars as bar (bar.date)}
                <tr>
                  <th scope="row">{bar.label}</th>
                  <td>{bar.count}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
      <div class="dd-daily-axis" aria-hidden="true">
        <span>{dailyBars.at(0)?.label ?? ''}</span>
        <span>{dailyBars.at(-1)?.label ?? ''}</span>
      </div>
    </div>
  {/if}

  <a class="dd-stats-link focus-ring" href={speciesUrl} onclick={handleAppLinkClick}>
    {t('detections.detail.stats.allThisDay', { species: speciesName })}
    <ArrowRight class="size-3.5" />
  </a>
</DetailCard>

<style>
  .dd-rarity-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.75rem;
    font-size: 0.8125rem;
  }

  .dd-rarity-label {
    color: var(--text-subtle);
  }

  .dd-rarity-status strong {
    margin-left: 0.25rem;
    font-variant-numeric: tabular-nums;
  }

  .dd-rarity-track {
    height: 0.5rem;
    margin-top: 0.375rem;
    overflow: hidden;
    border-radius: var(--radius-full);
    background-color: var(--color-base-200);
  }

  .dd-rarity-fill {
    display: block;
    height: 100%;
    border-radius: var(--radius-full);
    background-color: var(--color-primary);
  }

  .dd-rarity-note {
    margin-top: 0.3125rem;
    font-size: 0.6875rem;
    color: var(--text-subtle);
  }

  .dd-stats {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.75rem 1rem;
    margin-top: 1rem;
    padding-top: 0.875rem;
    border-top: 1px solid var(--border-100);
  }

  /* Without a range score the totals open the card: no divider and no space above them. */
  .dd-stats:first-child {
    margin-top: 0;
    padding-top: 0;
    border-top: none;
  }

  .dd-stat {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .dd-stat dt {
    font-size: 0.6875rem;
    color: var(--text-subtle);
  }

  .dd-stat dd {
    font-size: 1.125rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    line-height: 1.35;
  }

  .dd-stat dd.dd-stat-text {
    font-size: 0.875rem;
    line-height: 1.6;
  }

  .dd-reviews {
    margin-top: 1rem;
    padding-top: 0.875rem;
    border-top: 1px solid var(--border-100);
  }

  .dd-reviews-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.75rem;
    font-size: 0.75rem;
    color: var(--text-subtle);
  }

  .dd-reviews-count {
    font-variant-numeric: tabular-nums;
  }

  .dd-reviews-bar {
    display: flex;
    height: 0.5rem;
    margin-top: 0.5rem;
    overflow: hidden;
    border-radius: var(--radius-full);
    background-color: var(--color-base-200);
  }

  .dd-reviews-correct {
    background-color: var(--color-success);
  }

  .dd-reviews-false {
    background-color: var(--color-error);
  }

  .dd-reviews-legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.125rem 1rem;
    margin-top: 0.375rem;
    font-size: 0.8125rem;
  }

  .dd-reviews-key {
    display: inline-flex;
    align-items: center;
    gap: 0.375rem;
    font-variant-numeric: tabular-nums;
  }

  .dd-reviews-key::before {
    width: 0.5rem;
    height: 0.5rem;
    content: '';
    border-radius: 50%;
  }

  .dd-reviews-key-correct::before {
    background-color: var(--color-success);
  }

  .dd-reviews-key-false::before {
    background-color: var(--color-error);
  }

  .dd-reviews-warning {
    display: flex;
    align-items: center;
    gap: 0.375rem;
    margin-top: 0.5rem;
    font-size: 0.8125rem;
    font-weight: 500;
    color: var(--text-warning);
  }

  .dd-reviews-none {
    margin-top: 0.375rem;
    font-size: 0.8125rem;
    color: var(--text-subtle);
  }

  .dd-daily {
    margin-top: 1rem;
    padding-top: 0.875rem;
    border-top: 1px solid var(--border-100);
  }

  .dd-daily-head {
    font-size: 0.75rem;
    color: var(--text-subtle);
  }

  .dd-daily-bars {
    display: flex;
    align-items: flex-end;
    gap: 2px;
    height: 3rem;
    margin-top: 0.5rem;
    border-bottom: 1px solid var(--border-100);
  }

  .dd-daily-bar {
    flex: 1;
    min-width: 0;
    border-radius: 2px 2px 0 0;
    background-color: color-mix(in srgb, var(--color-primary) 38%, transparent);
  }

  .dd-daily-bar-now {
    background-color: var(--color-primary);
  }

  .dd-daily-axis {
    display: flex;
    justify-content: space-between;
    margin-top: 0.1875rem;
    font-size: 0.625rem;
    font-variant-numeric: tabular-nums;
    color: var(--text-subtle);
  }

  .dd-stats-link {
    display: inline-flex;
    align-items: center;
    gap: 0.375rem;
    margin-top: 1rem;
    font-size: 0.8125rem;
    font-weight: 500;
    color: var(--text-primary);
  }

  .dd-stats-link:hover {
    text-decoration: underline;
  }

  /* Windows contrast themes drop background colours: the fills and bars keep a system
     colour, so the score, the review split and the chart stay visible. */
  @media (forced-colors: active) {
    .dd-rarity-track,
    .dd-reviews-bar {
      border: 1px solid CanvasText;
    }

    .dd-rarity-fill,
    .dd-reviews-correct,
    .dd-daily-bar {
      forced-color-adjust: none;
      background-color: CanvasText;
    }

    .dd-reviews-false,
    .dd-daily-bar-now {
      forced-color-adjust: none;
      background-color: Highlight;
    }
  }
</style>
