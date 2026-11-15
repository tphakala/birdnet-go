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
  import { formatModelLabel, formatPercent, PERCENT, toPercent } from './detailFormat';
  import { inOffsetOf, wallClockTime } from './detailTime';
  import type { ModelContribution } from './detailTypes';

  interface Props {
    contributions?: ModelContribution[] | null;
    /** The detection's timestamp: hit times are shown on its UTC offset. */
    detectedAt?: string;
  }

  let { contributions = null, detectedAt }: Props = $props();

  // Narrowest confidence span drawn, in percent, so a single hit stays visible.
  const MIN_SPAN_PERCENT = 1.5;

  let rows = $derived(
    [...(contributions ?? [])].sort(
      (a, b) => b.hitCount - a.hitCount || (b.maxConfidence ?? 0) - (a.maxConfidence ?? 0)
    )
  );

  let hitModels = $derived(rows.filter(row => row.hitCount > 0).length);

  function confidenceText(row: ModelContribution): string {
    if (row.minConfidence === undefined || row.maxConfidence === undefined) return '';
    const low = toPercent(row.minConfidence);
    const high = toPercent(row.maxConfidence);
    return low === high ? `${high}%` : `${low}-${high}%`;
  }

  function hitTimes(row: ModelContribution): string {
    const first = wallClockTime(inOffsetOf(row.firstHitAt, detectedAt), true);
    const last = wallClockTime(inOffsetOf(row.lastHitAt, detectedAt), true);
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
    <div class="dd-models-box">
      <ul class="dd-models">
        {#each rows as row (`${row.modelName}-${row.modelVersion}-${row.modelVariant ?? ''}`)}
          {@const hit = row.hitCount > 0}
          <li class="dd-model" class:dd-model-miss={!hit}>
            <div class="dd-model-name">
              <span class="dd-model-dot" class:dd-model-dot-hit={hit} aria-hidden="true"></span>
              <span class="dd-model-title">
                {formatModelLabel(row.modelName, row.modelVersion, row.modelVariant)}
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
                      <span class="dd-range-mean" style:left="{row.meanConfidence * PERCENT}%"
                      ></span>
                    {/if}
                  </div>
                {/if}
                <span class="dd-model-confidence">{confidenceText(row)}</span>
              </div>
              <div class="dd-model-times">
                {#if row.hitCount > 1 && row.meanConfidence !== undefined}
                  <span
                    >{t('detections.detail.models.mean', {
                      value: formatPercent(row.meanConfidence),
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
    </div>
  {/if}
</DetailCard>

<style>
  .dd-models-empty {
    font-size: 0.875rem;
    color: var(--text-subtle);
  }

  .dd-models-box {
    container: dd-models / inline-size;
  }

  /* The rows share the list's columns, so the hit count column is as wide as its longest
     text in the UI language and the range bars start at one line in every row. */
  .dd-models {
    display: grid;
    grid-template-columns:
      minmax(12rem, 1.3fr) minmax(4rem, max-content) minmax(7rem, 1.4fr)
      minmax(7rem, auto);
    column-gap: 0.875rem;
  }

  .dd-model {
    display: grid;
    grid-template-columns: subgrid;
    grid-column: 1 / -1;
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
    color: var(--text-subtle);
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
    color: var(--text-subtle);
    white-space: nowrap;
  }

  .dd-model-miss .dd-model-title {
    font-weight: 500;
    color: var(--text-subtle);
  }

  .dd-model-nohit {
    grid-column: 2 / -1;
    font-size: 0.8125rem;
    color: var(--text-subtle);
  }

  /* Stacked rows: in a narrow window (one page column), and in any card too narrow for
     the four columns. Their minimum is 12 + 4 + 7 + 7rem plus three 0.875rem gaps,
     32.625rem; the hit count column grows with a longer text, up to about 6rem in the
     longest languages, which every two-column page still has room for. The two rules
     hold the same declarations; container and media queries cannot share one block. */
  @media (max-width: 900px) {
    .dd-models {
      grid-template-columns: minmax(0, 1fr);
    }

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

  @container dd-models (width < 32.625rem) {
    .dd-models {
      grid-template-columns: minmax(0, 1fr);
    }

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

  /* Windows contrast themes drop background colours: the confidence span, its mean and
     the hit dot keep a system colour. */
  @media (forced-colors: active) {
    .dd-range-track {
      border: 1px solid CanvasText;
    }

    .dd-range-span,
    .dd-model-dot-hit {
      forced-color-adjust: none;
      background-color: CanvasText;
    }

    .dd-range-mean {
      forced-color-adjust: none;
      background-color: Highlight;
    }
  }
</style>
