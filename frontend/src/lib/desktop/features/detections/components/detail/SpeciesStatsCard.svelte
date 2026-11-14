<!--
  SpeciesStatsCard - how this species behaves at this station: range filter
  score, totals, first and last heard, confidence, and daily counts for the
  weeks before the detection.
-->
<script lang="ts">
  import { ArrowRight, TriangleAlert } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { handleAppLinkClick } from '$lib/stores/navigation.svelte';
  import { buildSpeciesDetectionUrl } from '$lib/utils/detectionUrls';
  import DetailCard from './DetailCard.svelte';
  import { formatCalendarDate, wallClockDate } from './detailTime';
  import type {
    DailyCounts,
    DetectionDetailData,
    SpeciesRarity,
    SpeciesReviewCounts,
    SpeciesSummary,
  } from './detailTypes';

  interface Props {
    detection: DetectionDetailData;
    speciesName: string;
    rarity?: SpeciesRarity | null;
    summary?: SpeciesSummary | null;
    /** True while the all-time summary is still loading. */
    summaryLoading?: boolean;
    /** Daily counts for the days leading up to the detection. */
    daily?: DailyCounts | null;
    /** First day of the daily counts window (YYYY-MM-DD). */
    dailyStart: string;
    /** Number of days in the daily counts window. */
    dailyDays: number;
    /** How reviewers judged this species' detections; null until known. */
    reviews?: SpeciesReviewCounts | null;
  }

  let {
    detection,
    speciesName,
    rarity = null,
    summary = null,
    summaryLoading = false,
    daily = null,
    dailyStart,
    dailyDays,
    reviews = null,
  }: Props = $props();

  const PERCENT = 100;
  const MS_PER_DAY = 86_400_000;
  // Smallest bar height, in percent, for a day that has any detections.
  const MIN_BAR_PERCENT = 8;
  // A species is called out as often rejected once at least this many of its
  // detections were reviewed and this share of them were false positives.
  const MIN_REVIEWS_FOR_WARNING = 3;
  const FALSE_POSITIVE_WARNING_SHARE = 0.5;

  let reviewed = $derived(reviews ? reviews.correct + reviews.falsePositive : 0);
  let falsePositiveShare = $derived(reviews && reviewed > 0 ? reviews.falsePositive / reviewed : 0);
  let oftenRejected = $derived(
    reviewed >= MIN_REVIEWS_FOR_WARNING && falsePositiveShare >= FALSE_POSITIVE_WARNING_SHARE
  );

  function dayKey(date: Date): string {
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${date.getFullYear()}-${month}-${day}`;
  }

  // One bar per calendar day of the window, including days without detections.
  let dailyBars = $derived.by(() => {
    const [year, month, day] = dailyStart.split('-').map(Number);
    if (!year || !month || !day) return [];
    const counts = new Map((daily?.data ?? []).map(entry => [entry.date, entry.count]));
    const start = new Date(year, month - 1, day).getTime();
    const bars = Array.from({ length: dailyDays }, (_, index) => {
      // Midday keeps the date stable across daylight saving changes.
      const date = dayKey(new Date(start + index * MS_PER_DAY + MS_PER_DAY / 2));
      return { date, count: counts.get(date) ?? 0 };
    });
    const peak = Math.max(1, ...bars.map(bar => bar.count));
    return bars.map(bar => ({ ...bar, share: bar.count / peak }));
  });

  let windowTotal = $derived(dailyBars.reduce((sum, bar) => sum + bar.count, 0));

  let speciesUrl = $derived(buildSpeciesDetectionUrl(detection.scientificName, detection.date));

  function percent(value: number | undefined): string {
    return value === undefined ? '-' : `${Math.round(value * PERCENT)}%`;
  }

  function heard(timestamp: string | undefined): string {
    return timestamp ? formatCalendarDate(wallClockDate(timestamp)) : '-';
  }
</script>

<DetailCard title={t('detections.detail.stats.title')} subtitle={speciesName}>
  {#if rarity}
    <div class="dd-rarity">
      <div class="dd-rarity-head">
        <span class="dd-rarity-label">{t('detections.detail.stats.rangeScore')}</span>
        <span class="dd-rarity-status">
          {t(`species.rarity.statuses.${rarity.status}`)}
          <strong>{Math.round(rarity.score * PERCENT)}%</strong>
        </span>
      </div>
      <div
        class="dd-rarity-track"
        role="meter"
        aria-valuemin="0"
        aria-valuemax="100"
        aria-valuenow={Math.round(rarity.score * PERCENT)}
        aria-label={t('detections.detail.stats.rangeScore')}
      >
        <span class="dd-rarity-fill" style:width="{rarity.score * PERCENT}%"></span>
      </div>
      <p class="dd-rarity-note">
        {rarity.date
          ? t('detections.detail.stats.rangeScoreNote', { date: rarity.date })
          : t('detections.detail.stats.rangeScoreNoteToday')}
      </p>
    </div>
  {/if}

  <dl class="dd-stats">
    <div class="dd-stat">
      <dt>{t('detections.detail.stats.total')}</dt>
      <dd>
        {#if summary}
          {summary.count.toLocaleString()}
        {:else if summaryLoading}
          <span class="dd-stat-loading">{t('common.loading')}</span>
        {:else}
          -
        {/if}
      </dd>
    </div>
    <div class="dd-stat">
      <dt>{t('detections.detail.stats.windowTotal', { days: dailyDays })}</dt>
      <dd>{windowTotal.toLocaleString()}</dd>
    </div>
    <div class="dd-stat">
      <dt>{t('detections.detail.stats.firstHeard')}</dt>
      <dd class="dd-stat-text">{heard(summary?.first_heard)}</dd>
    </div>
    <div class="dd-stat">
      <dt>{t('detections.detail.stats.lastHeard')}</dt>
      <dd class="dd-stat-text">{heard(summary?.last_heard)}</dd>
    </div>
    <div class="dd-stat">
      <dt>{t('detections.detail.stats.avgConfidence')}</dt>
      <dd>{percent(summary?.avg_confidence)}</dd>
    </div>
    <div class="dd-stat">
      <dt>{t('detections.detail.stats.maxConfidence')}</dt>
      <dd>{percent(summary?.max_confidence)}</dd>
    </div>
  </dl>

  {#if reviews}
    <div class="dd-reviews">
      <div class="dd-reviews-head">
        <span>{t('detections.detail.stats.reviewsTitle')}</span>
        <span class="dd-reviews-count">
          {t('detections.detail.stats.reviewsCount', { count: reviewed })}
        </span>
      </div>
      {#if reviewed > 0}
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
              percent: Math.round(falsePositiveShare * PERCENT),
            })}
          </p>
        {/if}
      {:else}
        <p class="dd-reviews-none">{t('detections.detail.stats.reviewsNone')}</p>
      {/if}
    </div>
  {/if}

  {#if dailyBars.length > 0}
    <div class="dd-daily">
      <div class="dd-daily-head">
        <span>{t('detections.detail.stats.dailyTitle', { days: dailyDays })}</span>
      </div>
      <div
        class="dd-daily-bars"
        role="img"
        aria-label={t('detections.detail.stats.dailyLabel', {
          days: dailyDays,
          count: windowTotal,
        })}
      >
        {#each dailyBars as bar (bar.date)}
          <span
            class="dd-daily-bar"
            class:dd-daily-bar-now={bar.date === detection.date}
            style:height="{Math.max(bar.share * PERCENT, bar.count > 0 ? MIN_BAR_PERCENT : 0)}%"
            title="{bar.date}  {bar.count}"
          ></span>
        {/each}
      </div>
      <div class="dd-daily-axis" aria-hidden="true">
        <span>{dailyBars.at(0)?.date ?? ''}</span>
        <span>{dailyBars.at(-1)?.date ?? ''}</span>
      </div>
    </div>
  {/if}

  <a class="dd-stats-link" href={speciesUrl} onclick={handleAppLinkClick}>
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
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
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
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-stats {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.75rem 1rem;
    margin-top: 1rem;
    padding-top: 0.875rem;
    border-top: 1px solid var(--border-100);
  }

  .dd-stat {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .dd-stat dt {
    font-size: 0.6875rem;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
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

  .dd-stat-loading {
    font-size: 0.8125rem;
    font-weight: 400;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
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
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
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
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-daily {
    margin-top: 1rem;
    padding-top: 0.875rem;
    border-top: 1px solid var(--border-100);
  }

  .dd-daily-head {
    font-size: 0.75rem;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
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
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
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
</style>
