<!--
  DetailNavBar - breadcrumb and older/newer navigation of the detection detail page.
  Two pairs of links step through all detections and through detections of the
  same species, by detection ID. A step with nowhere to go stays in place as a
  blocked link (the same element, so keyboard focus survives the next step).

  The step buttons keep their place on screen: both groups have a fixed width and
  stay on the first line, so a long species name is cut short instead of pushing
  a button sideways, and the buttons stay in place (blocked) while the next
  detection loads.
-->
<script lang="ts">
  import { ArrowLeft, ChevronLeft, ChevronRight } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { handleAppLinkClick } from '$lib/stores/navigation.svelte';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { formatCalendarDate } from './detailTime';
  import type { DetectionNeighbors } from './detailTypes';

  interface Props {
    speciesName: string;
    date: string;
    time: string;
    /** The neighbours of the detection; null until they are known. */
    neighbors: DetectionNeighbors | null;
    /** True when the neighbours could not be loaded; the step links then say so. */
    failed?: boolean;
    /**
     * False when the server cannot step between detections; the groups are then left out.
     * While the neighbours are not known, the step links are blocked under their own names.
     */
    available: boolean;
  }

  let { speciesName, date, time, neighbors, failed = false, available }: Props = $props();

  // The name of a blocked step link. Every state keeps the link's own name so the four
  // links stay distinguishable: while the neighbours are being looked up or when the lookup
  // failed it adds that reason, and once they are known it states why there is nowhere to go.
  function blockedLabel(label: string, emptyLabel: string): string {
    if (failed) return t('detections.detail.nav.stepFailed', { label });
    if (neighbors === null) return t('detections.detail.nav.stepLoading', { label });
    return emptyLabel;
  }

  const listUrl = buildAppUrl('/ui/detections');

  function blockClick(event: MouseEvent) {
    event.preventDefault();
  }

  // A click focuses the link it steps with, as a keyboard step does: Safari does not focus
  // a clicked link, and focus left in the page would be lost while the next detection loads.
  function step(event: MouseEvent) {
    if (event.currentTarget instanceof window.HTMLElement) {
      event.currentTarget.focus({ preventScroll: true });
    }
    handleAppLinkClick(event);
  }
</script>

{#snippet stepLink(id: number | null, label: string, emptyLabel: string, older: boolean)}
  <a
    class="dd-nav-step focus-ring"
    href={id === null ? undefined : buildAppUrl(`/ui/detections/${id}`)}
    role={id === null ? 'link' : undefined}
    tabindex={id === null ? 0 : undefined}
    aria-disabled={id === null ? 'true' : undefined}
    aria-label={id === null ? blockedLabel(label, emptyLabel) : label}
    title={id === null ? blockedLabel(label, emptyLabel) : label}
    onclick={id === null ? blockClick : step}
  >
    {#if older}
      <ChevronLeft class="size-4" />
    {:else}
      <ChevronRight class="size-4" />
    {/if}
  </a>
{/snippet}

<nav class="dd-nav" aria-label={t('detections.detail.nav.label')}>
  <ol class="dd-nav-trail">
    <li class="dd-nav-item">
      <a class="dd-nav-back focus-ring" href={listUrl} onclick={handleAppLinkClick}>
        <ArrowLeft class="size-4" />
        <span>{t('detections.detail.nav.back')}</span>
      </a>
    </li>
    <li class="dd-nav-item dd-nav-item-species">
      <span class="dd-nav-crumb dd-nav-crumb-species">{speciesName}</span>
    </li>
    <li class="dd-nav-item" aria-current="page">
      <span class="dd-nav-crumb dd-nav-crumb-time">{formatCalendarDate(date)} {time}</span>
    </li>
  </ol>

  {#if available}
    <div class="dd-nav-groups" aria-busy={neighbors === null && !failed}>
      <div class="dd-nav-group" role="group" aria-label={t('detections.detail.nav.allGroup')}>
        {@render stepLink(
          neighbors?.previous ?? null,
          t('detections.detail.nav.older'),
          t('detections.detail.nav.noOlder'),
          true
        )}
        <span class="dd-nav-group-label">{t('detections.detail.nav.allGroup')}</span>
        {@render stepLink(
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
        {@render stepLink(
          neighbors?.previousSameSpecies ?? null,
          t('detections.detail.nav.olderSpecies', { species: speciesName }),
          t('detections.detail.nav.noOlderSpecies', { species: speciesName }),
          true
        )}
        <span class="dd-nav-group-label dd-nav-group-species" title={speciesName}
          >{speciesName}</span
        >
        {@render stepLink(
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

  .dd-nav-item {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 0.5rem;
  }

  .dd-nav-item-species {
    flex: 0 1 auto;
    min-width: 0;
  }

  /* The separator is drawn, not written, and has empty alternative text. */
  .dd-nav-item + .dd-nav-item::before {
    content: '/' / '';
    color: var(--text-subtle);
  }

  .dd-nav-back {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 0.375rem;
    padding: 0.25rem 0.5rem;
    margin-left: -0.5rem;
    border-radius: var(--radius-field);
    color: var(--text-subtle);
  }

  .dd-nav-back:hover {
    background-color: var(--color-base-200);
    color: var(--color-base-content);
  }

  .dd-nav-crumb {
    min-width: 0;
    white-space: nowrap;
    color: var(--text-subtle);
  }

  /* Only the species crumb gives way to a long name; the current page is never cut. */
  .dd-nav-crumb-species {
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .dd-nav-crumb-time {
    flex: 0 0 auto;
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
    color: var(--text-subtle);
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
  }

  .dd-nav-step:hover {
    background-color: var(--color-base-200);
  }

  /* Dimmed by colour, not opacity, so the focus ring keeps its contrast. */
  .dd-nav-step[aria-disabled='true'] {
    color: color-mix(in srgb, var(--color-base-content) 35%, transparent);
    cursor: not-allowed;
  }

  .dd-nav-step[aria-disabled='true']:hover {
    background-color: transparent;
  }

  /* On a narrow page the trail and the step groups each take their own line, at every
     detection, so the trail has room and nothing depends on its length. The page grid
     switches to one column at the same width (DetectionDetail.svelte, container dd-page). */
  @container dd-page (width < 64rem) {
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
