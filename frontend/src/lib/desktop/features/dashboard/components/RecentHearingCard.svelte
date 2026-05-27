<!--
RecentHearingCard.svelte - Recent species activity tile for wall dashboards.

Shows species heard in the recent past, ranked by recency and confidence, with a
compact confidence sparkline for the configured time window.
-->
<script lang="ts">
  import { RefreshCw, XCircle } from '@lucide/svelte';
  import Sparkline from '$lib/desktop/features/system/components/Sparkline.svelte';
  import { t } from '$lib/i18n';
  import type { RecentSpeciesActivity } from '$lib/types/detection.types';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { cn } from '$lib/utils/cn';

  interface Props {
    data: RecentSpeciesActivity[];
    loading?: boolean;
    error?: string | null;
    hours?: number;
    onRefresh?: () => void;
    className?: string;
  }

  let {
    data = [],
    loading = false,
    error = null,
    hours = 4,
    onRefresh,
    className = '',
  }: Props = $props();

  const MAX_VISIBLE_ROWS = 8;
  const HIGH_CONFIDENCE = 0.8;
  const MEDIUM_CONFIDENCE = 0.5;
  const MINUTE_SECONDS = 60;
  const HOUR_SECONDS = 3600;

  let tick = $state(0);
  let visibleRows = $derived(data.slice(0, MAX_VISIBLE_ROWS));
  let hasRows = $derived(visibleRows.length > 0);

  $effect(() => {
    if (!hasRows) return;
    const interval = setInterval(() => {
      tick++;
    }, MINUTE_SECONDS * 1000);
    return () => clearInterval(interval);
  });

  function confidencePercent(value: number): number {
    return Math.round(value * 100);
  }

  function relativeTime(value: string): string {
    void tick;
    const detectedAt = Date.parse(value);
    if (Number.isNaN(detectedAt)) return '';

    const elapsedSeconds = Math.max(0, Math.floor((Date.now() - detectedAt) / 1000));
    if (elapsedSeconds < MINUTE_SECONDS) {
      return t('dashboard.recentHearing.justNow');
    }

    const minutes = Math.floor(elapsedSeconds / MINUTE_SECONDS);
    if (minutes < MINUTE_SECONDS) {
      return t('dashboard.recentHearing.minutesAgo', { count: minutes });
    }

    const hoursElapsed = Math.floor(elapsedSeconds / HOUR_SECONDS);
    return t('dashboard.recentHearing.hoursAgo', { count: hoursElapsed });
  }

  function confidenceTone(value: number): string {
    if (value >= HIGH_CONFIDENCE) {
      return 'bg-[var(--color-success)]/15 text-[var(--color-success)] ring-[var(--color-success)]/25';
    }
    if (value >= MEDIUM_CONFIDENCE) {
      return 'bg-[var(--color-warning)]/15 text-[var(--color-warning)] ring-[var(--color-warning)]/25';
    }
    return 'bg-[var(--color-base-200)] text-[var(--color-base-content)]/70 ring-[var(--color-base-content)]/10';
  }

  function rowKey(item: RecentSpeciesActivity): string {
    return item.scientific_name || item.common_name;
  }
</script>

<section
  class={cn(
    'col-span-12 flex h-full flex-col rounded-2xl border border-border-100 bg-[var(--color-base-100)] shadow-sm',
    className
  )}
>
  <div
    class="flex items-center justify-between gap-3 border-b border-[var(--color-base-200)] px-6 py-4"
  >
    <div class="min-w-0">
      <h3 class="truncate font-semibold">{t('dashboard.recentHearing.title')}</h3>
      <p class="truncate text-sm text-[var(--color-base-content)]/60">
        {t('dashboard.recentHearing.subtitle', { hours })}
      </p>
    </div>
    {#if onRefresh}
      <button
        onclick={onRefresh}
        class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-[var(--color-base-content)]/60 transition-colors hover:bg-[var(--color-base-200)] hover:text-[var(--color-base-content)] disabled:pointer-events-none disabled:opacity-50"
        disabled={loading}
        title={t('dashboard.recentHearing.controls.refresh')}
        aria-label={t('dashboard.recentHearing.controls.refresh')}
      >
        <RefreshCw class={loading ? 'size-4 animate-spin' : 'size-4'} />
      </button>
    {/if}
  </div>

  <div class="flex-1 px-4 py-3">
    {#if loading && !hasRows}
      <div class="space-y-2" aria-label={t('dashboard.recentHearing.loading')}>
        {#each Array(4) as _, index (index)}
          <div
            class="grid min-h-14 grid-cols-[minmax(0,1fr)_5.75rem_3.25rem] items-center gap-3 rounded-lg px-2"
          >
            <div class="flex min-w-0 items-center gap-3">
              <div
                class="h-9 aspect-[4/3] animate-pulse rounded-md bg-[var(--color-base-200)]"
              ></div>
              <div class="min-w-0 flex-1 space-y-1.5">
                <div class="h-3 w-3/4 animate-pulse rounded bg-[var(--color-base-200)]"></div>
                <div class="h-2.5 w-1/2 animate-pulse rounded bg-[var(--color-base-200)]"></div>
              </div>
            </div>
            <div class="h-8 animate-pulse rounded bg-[var(--color-base-200)]"></div>
            <div class="h-6 animate-pulse rounded-full bg-[var(--color-base-200)]"></div>
          </div>
        {/each}
      </div>
    {:else if error}
      <div
        class="flex h-full min-h-32 items-center justify-center gap-2 px-4 text-sm text-[var(--color-error)]"
      >
        <XCircle class="size-4 shrink-0" />
        <span>{error}</span>
      </div>
    {:else if hasRows}
      <ul class="divide-y divide-[var(--color-base-200)]" role="list">
        {#each visibleRows as item (rowKey(item))}
          <li
            class="grid min-h-14 grid-cols-[minmax(0,1fr)_5.75rem_3.25rem] items-center gap-3 px-2 py-2"
          >
            <div class="flex min-w-0 items-center gap-3">
              {#if item.thumbnail_url}
                <img
                  src={buildAppUrl(item.thumbnail_url)}
                  alt={item.common_name}
                  class="h-9 aspect-[4/3] shrink-0 rounded-md object-cover"
                />
              {:else}
                <div
                  class="flex h-9 aspect-[4/3] shrink-0 items-center justify-center rounded-md bg-[var(--color-base-content)]/10 text-xs font-bold text-[var(--color-base-content)]/50"
                >
                  {item.common_name.slice(0, 2).toUpperCase()}
                </div>
              {/if}

              <div class="min-w-0">
                <div
                  class="truncate text-sm font-medium leading-tight text-[var(--color-base-content)]"
                >
                  {item.common_name}
                </div>
                <div class="truncate text-xs text-[var(--color-base-content)]/55">
                  {relativeTime(item.latest_heard_at)} · {t('dashboard.recentHearing.detections', {
                    count: item.count,
                  })}
                </div>
              </div>
            </div>

            <div
              class="h-8"
              role="img"
              aria-label={t('dashboard.recentHearing.confidenceTrend', {
                species: item.common_name,
              })}
            >
              <Sparkline
                data={item.confidence_trend}
                color="var(--color-primary)"
                threshold={HIGH_CONFIDENCE}
                thresholdColor="var(--color-success)"
                viewWidth={92}
                viewHeight={32}
              />
            </div>

            <div
              class={cn(
                'justify-self-end rounded-full px-2 py-1 text-xs font-semibold tabular-nums ring-1',
                confidenceTone(item.latest_confidence)
              )}
              title={t('dashboard.recentHearing.confidence', {
                confidence: confidencePercent(item.latest_confidence),
              })}
            >
              {confidencePercent(item.latest_confidence)}%
            </div>
          </li>
        {/each}
      </ul>
    {:else}
      <div class="flex h-full min-h-32 items-center justify-center px-6">
        <p class="text-sm text-[var(--color-base-content)]/40">
          {t('dashboard.recentHearing.empty')}
        </p>
      </div>
    {/if}
  </div>
</section>
