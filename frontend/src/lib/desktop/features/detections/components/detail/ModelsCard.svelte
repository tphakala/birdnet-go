<!--
  ModelsCard - which models took part in a detection.

  One row per model that was active on the source: how many analysis windows
  hit, the confidence range of those hits, and when they happened. A model that
  ran without a hit is listed too, since "no other model agreed" is itself
  evidence.
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import DetailCard from './DetailCard.svelte';
  import { wallClockTime } from './detailTime';
  import type { ModelContribution } from './detailTypes';

  interface Props {
    contributions?: ModelContribution[] | null;
  }

  let { contributions = null }: Props = $props();

  const PERCENT = 100;
  const DEFAULT_VARIANT = 'default';
  // Narrowest confidence span drawn, in percent, so a single hit stays visible.
  const MIN_SPAN_PERCENT = 1.5;

  let rows = $derived(
    [...(contributions ?? [])].sort(
      (a, b) => b.hitCount - a.hitCount || (b.maxConfidence ?? 0) - (a.maxConfidence ?? 0)
    )
  );

  let hitModels = $derived(rows.filter(row => row.hitCount > 0).length);

  function modelLabel(name: string, version: string, variant?: string): string {
    const base = [name, version].filter(Boolean).join(' ');
    return variant && variant !== DEFAULT_VARIANT ? `${base} (${variant})` : base;
  }

  function percent(value: number | undefined): string {
    return value === undefined ? '' : `${Math.round(value * PERCENT)}%`;
  }

  function confidenceText(row: ModelContribution): string {
    if (row.minConfidence === undefined || row.maxConfidence === undefined) return '';
    const low = Math.round(row.minConfidence * PERCENT);
    const high = Math.round(row.maxConfidence * PERCENT);
    return low === high ? `${high}%` : `${low}-${high}%`;
  }

  function hitTimes(row: ModelContribution): string {
    const first = wallClockTime(row.firstHitAt, true);
    const last = wallClockTime(row.lastHitAt, true);
    if (!first) return '';
    return !last || first === last ? first : `${first}-${last}`;
  }
</script>

<DetailCard
  title={t('detections.detail.models.title')}
  subtitle={rows.length > 0
    ? t('detections.detail.models.summary', { hit: hitModels, total: rows.length })
    : ''}
>
  {#if rows.length === 0}
    <p class="dd-models-empty">{t('detections.detail.models.empty')}</p>
  {:else}
    <ul class="dd-models">
      {#each rows as row (`${row.modelName}-${row.modelVersion}-${row.modelVariant ?? ''}`)}
        {@const hit = row.hitCount > 0}
        <li class="dd-model" class:dd-model-miss={!hit}>
          <div class="dd-model-name">
            <span class="dd-model-dot" class:dd-model-dot-hit={hit} aria-hidden="true"></span>
            <span class="dd-model-title">
              {modelLabel(row.modelName, row.modelVersion, row.modelVariant)}
            </span>
            {#if row.modelType}
              <span class="dd-model-type">{row.modelType}</span>
            {/if}
          </div>
          {#if hit}
            <div class="dd-model-hits">
              {t('detections.detail.models.hits', { count: row.hitCount })}
            </div>
            <div class="dd-model-range">
              {#if row.minConfidence !== undefined && row.maxConfidence !== undefined}
                <div class="dd-range-track" aria-hidden="true">
                  <span
                    class="dd-range-span"
                    style:left="{row.minConfidence * PERCENT}%"
                    style:width="{Math.max(
                      (row.maxConfidence - row.minConfidence) * PERCENT,
                      MIN_SPAN_PERCENT
                    )}%"
                  ></span>
                  {#if row.meanConfidence !== undefined}
                    <span class="dd-range-mean" style:left="{row.meanConfidence * PERCENT}%"></span>
                  {/if}
                </div>
              {/if}
              <span class="dd-model-confidence">{confidenceText(row)}</span>
            </div>
            <div class="dd-model-times">
              {#if row.hitCount > 1 && row.meanConfidence !== undefined}
                <span
                  >{t('detections.detail.models.mean', {
                    value: percent(row.meanConfidence),
                  })}</span
                >
              {/if}
              <span>{hitTimes(row)}</span>
            </div>
          {:else}
            <div class="dd-model-nohit">{t('detections.detail.models.noHit')}</div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</DetailCard>

<style>
  .dd-models-empty {
    font-size: 0.875rem;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-models {
    display: flex;
    flex-direction: column;
  }

  .dd-model {
    display: grid;
    grid-template-columns: minmax(12rem, 1.3fr) 4rem minmax(7rem, 1.4fr) minmax(7rem, auto);
    align-items: center;
    gap: 0.25rem 0.875rem;
    padding: 0.5rem 0;
    font-size: 0.875rem;
    border-top: 1px solid var(--border-100);
  }

  .dd-model:first-child {
    border-top: none;
    padding-top: 0;
  }

  .dd-model-name {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    min-width: 0;
  }

  .dd-model-dot {
    flex: 0 0 auto;
    width: 0.5rem;
    height: 0.5rem;
    border: 1.5px solid color-mix(in srgb, var(--color-base-content) 40%, transparent);
    border-radius: 50%;
  }

  .dd-model-dot-hit {
    border-color: var(--color-success);
    background-color: var(--color-success);
  }

  .dd-model-title {
    overflow: hidden;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .dd-model-type {
    flex: 0 0 auto;
    padding: 0 0.375rem;
    font-size: 0.625rem;
    font-weight: 600;
    letter-spacing: 0.04em;
    line-height: 1.125rem;
    text-transform: uppercase;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
    border: 1px solid var(--border-200);
    border-radius: var(--radius-full);
  }

  .dd-model-hits {
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .dd-model-range {
    display: flex;
    align-items: center;
    gap: 0.625rem;
    min-width: 0;
  }

  .dd-range-track {
    position: relative;
    flex: 1;
    height: 0.375rem;
    border-radius: var(--radius-full);
    background-color: var(--color-base-200);
  }

  .dd-range-span {
    position: absolute;
    top: 0;
    bottom: 0;
    border-radius: var(--radius-full);
    background-color: var(--color-success);
  }

  .dd-range-mean {
    position: absolute;
    top: -0.1875rem;
    bottom: -0.1875rem;
    width: 2px;
    background-color: var(--color-base-content);
    transform: translateX(-1px);
  }

  .dd-model-confidence {
    flex: 0 0 auto;
    min-width: 3.5rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    text-align: right;
  }

  .dd-model-times {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    font-size: 0.75rem;
    font-variant-numeric: tabular-nums;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
    white-space: nowrap;
  }

  .dd-model-miss .dd-model-title {
    font-weight: 500;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-model-nohit {
    grid-column: 2 / -1;
    font-size: 0.8125rem;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  @media (max-width: 900px) {
    .dd-model {
      grid-template-columns: minmax(0, 1fr) auto;
    }

    .dd-model-range,
    .dd-model-nohit {
      grid-column: 1 / -1;
    }

    .dd-model-times {
      flex-direction: row;
      gap: 0.5rem;
    }
  }
</style>
