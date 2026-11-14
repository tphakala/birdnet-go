<!--
  DetailNavBar - breadcrumb and older/newer navigation of the detection detail page.
  Two pairs of buttons step through all detections and through detections of the
  same species, by detection ID.

  The step buttons keep their place on screen: both groups have a fixed width and
  stay on the first line, so a long species name is cut short instead of pushing
  a button sideways, and the buttons stay in place (blocked) while the next
  detection loads.
-->
<script lang="ts">
  import { ArrowLeft, ChevronLeft, ChevronRight } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { DetectionNeighbors } from './detailTypes';

  interface Props {
    speciesName: string;
    date: string;
    time: string;
    /** The neighbours of the detection; null until they are known. */
    neighbors: DetectionNeighbors | null;
    /** False when the server cannot step between detections; the groups are then left out. */
    available: boolean;
    onNavigate: (_id: number) => void;
    onBack: () => void;
  }

  let { speciesName, date, time, neighbors, available, onNavigate, onBack }: Props = $props();

  function go(id: number | null) {
    if (id !== null) onNavigate(id);
  }
</script>

{#snippet stepButton(id: number | null, label: string, emptyLabel: string, older: boolean)}
  <button
    type="button"
    class="dd-nav-step"
    aria-label={id === null ? emptyLabel : label}
    title={id === null ? emptyLabel : label}
    aria-disabled={id === null ? 'true' : undefined}
    onclick={() => go(id)}
  >
    {#if older}
      <ChevronLeft class="size-4" />
    {:else}
      <ChevronRight class="size-4" />
    {/if}
  </button>
{/snippet}

<nav class="dd-nav" aria-label={t('detections.detail.nav.label')}>
  <div class="dd-nav-trail">
    <button type="button" class="dd-nav-back" onclick={onBack}>
      <ArrowLeft class="size-4" />
      <span>{t('detections.detail.nav.back')}</span>
    </button>
    <span class="dd-nav-sep" aria-hidden="true">/</span>
    <span class="dd-nav-crumb dd-nav-crumb-species">{speciesName}</span>
    <span class="dd-nav-sep" aria-hidden="true">/</span>
    <span class="dd-nav-crumb dd-nav-crumb-time">{date} {time}</span>
  </div>

  {#if available}
    <div class="dd-nav-groups" aria-busy={neighbors === null}>
      <div class="dd-nav-group" role="group" aria-label={t('detections.detail.nav.allGroup')}>
        {@render stepButton(
          neighbors?.previous ?? null,
          t('detections.detail.nav.older'),
          t('detections.detail.nav.noOlder'),
          true
        )}
        <span class="dd-nav-group-label">{t('detections.detail.nav.allGroup')}</span>
        {@render stepButton(
          neighbors?.next ?? null,
          t('detections.detail.nav.newer'),
          t('detections.detail.nav.noNewer'),
          false
        )}
      </div>
      <div
        class="dd-nav-group"
        role="group"
        aria-label={t('detections.detail.nav.speciesGroup', { species: speciesName })}
      >
        {@render stepButton(
          neighbors?.previousSameSpecies ?? null,
          t('detections.detail.nav.olderSpecies', { species: speciesName }),
          t('detections.detail.nav.noOlderSpecies', { species: speciesName }),
          true
        )}
        <span class="dd-nav-group-label dd-nav-group-species" title={speciesName}
          >{speciesName}</span
        >
        {@render stepButton(
          neighbors?.nextSameSpecies ?? null,
          t('detections.detail.nav.newerSpecies', { species: speciesName }),
          t('detections.detail.nav.noNewerSpecies', { species: speciesName }),
          false
        )}
      </div>
    </div>
  {/if}
</nav>

<style>
  .dd-nav {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem 1.5rem;
    min-width: 0;
  }

  .dd-nav-trail {
    display: flex;
    flex: 1 1 0;
    align-items: center;
    gap: 0.5rem;
    min-width: 0;
    font-size: 0.875rem;
  }

  .dd-nav-back {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 0.375rem;
    padding: 0.25rem 0.5rem;
    margin-left: -0.5rem;
    border-radius: var(--radius-field);
    color: color-mix(in srgb, var(--color-base-content) 75%, transparent);
    cursor: pointer;
  }

  .dd-nav-back:hover {
    background-color: var(--color-base-200);
    color: var(--color-base-content);
  }

  .dd-nav-sep {
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-nav-crumb {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: color-mix(in srgb, var(--color-base-content) 75%, transparent);
  }

  .dd-nav-crumb-time {
    font-variant-numeric: tabular-nums;
    color: var(--color-base-content);
    font-weight: 500;
  }

  .dd-nav-groups {
    display: flex;
    flex: 0 0 auto;
    gap: 0.5rem;
  }

  .dd-nav-group {
    display: inline-flex;
    align-items: center;
    background-color: var(--color-base-100);
    border: 1px solid var(--border-100);
    border-radius: var(--radius-field);
  }

  /* Fixed width, so the arrow after the label never moves with the text. */
  .dd-nav-group-label {
    width: 6.5rem;
    padding: 0 0.25rem;
    overflow: hidden;
    font-size: 0.75rem;
    text-align: center;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-nav-group-species {
    width: 9rem;
  }

  .dd-nav-step {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 2rem;
    height: 2rem;
    border-radius: var(--radius-field);
    color: var(--color-base-content);
    cursor: pointer;
  }

  .dd-nav-step:hover {
    background-color: var(--color-base-200);
  }

  .dd-nav-step[aria-disabled='true'] {
    opacity: 0.35;
    cursor: not-allowed;
  }

  .dd-nav-step[aria-disabled='true']:hover {
    background-color: transparent;
  }

  .dd-nav-back:focus-visible,
  .dd-nav-step:focus-visible {
    outline: 2px solid var(--color-primary);
    outline-offset: 1px;
  }

  /* In a narrow window the trail and the step groups each take their own line, at
     every detection, so the trail has room and nothing depends on its length. */
  @media (max-width: 1100px) {
    .dd-nav {
      flex-direction: column;
      align-items: stretch;
      gap: 0.5rem;
    }

    .dd-nav-trail {
      flex: 0 0 auto;
    }

    .dd-nav-groups {
      align-self: flex-end;
    }
  }
</style>
