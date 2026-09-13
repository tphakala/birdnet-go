<!--
  DetectionResultRow.svelte

  Purpose: One collapsed row of an expandable detection list - date/time, time of
  day, species (thumbnail + names), confidence bar, source, review/lock status and
  the actions cell. Shared by Search's results table and the analytics Summary
  page's recent detections table so the two lists keep identical columns and
  styling.

  The actions cell is composed: the host passes its own leading controls through
  the `actions` snippet (Search's review dropdown, Summary's ActionMenu), and this
  component appends the "view details" and "expand" buttons both lists share.

  Clicking anywhere outside the actions cell toggles the expanded row; the
  expanded row itself is the host's, rendered with DetectionExpandedPanel.

  Props: see the Props interface below.
-->
<script lang="ts">
  import TimeOfDayIcon from '$lib/desktop/components/ui/TimeOfDayIcon.svelte';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils';
  import SourceBadge from '$lib/desktop/features/dashboard/components/SourceBadge.svelte';
  import { getLocale, t } from '$lib/i18n';
  import type { SourceInfo } from '$lib/types/detection.types';
  import { parseLocalDateString } from '$lib/utils/date';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { ChevronDown, Eye } from '@lucide/svelte';
  import type { Snippet } from 'svelte';

  // Mirrors TimeOfDayIcon's accepted values; detections carry it as a plain string.
  type TimeOfDayValue = 'day' | 'night' | 'sunrise' | 'sunset' | 'dawn' | 'dusk';

  interface Props {
    detectionId: number | string;
    /** id of the matching expanded <tr>, referenced by aria-controls */
    rowId: string;
    /** row position, used for the alternating row background */
    index: number;
    timestamp?: string;
    timeOfDay?: string;
    scientificName: string;
    /** localized common name shown to the user */
    displayName: string;
    confidence: number;
    verified?: string;
    locked?: boolean;
    source?: SourceInfo | null;
    expanded: boolean;
    onToggleExpand: () => void;
    onViewDetails: () => void;
    /** host-specific action controls, rendered ahead of view/expand */
    actions?: Snippet;
  }

  let {
    detectionId,
    rowId,
    index,
    timestamp,
    timeOfDay,
    scientificName,
    displayName,
    confidence,
    verified,
    locked = false,
    source = null,
    expanded,
    onToggleExpand,
    onViewDetails,
    actions,
  }: Props = $props();

  const speciesLabel = $derived(displayName || t('search.detailsPanel.unknownSpecies'));
  const confidencePercent = $derived(Math.round(confidence * 100));

  const expandLabel = $derived(
    expanded
      ? t('search.detailsPanel.collapseDetails', { species: speciesLabel })
      : t('search.detailsPanel.expandDetails', { species: speciesLabel })
  );

  function formatDate(dateString: string | undefined) {
    if (!dateString) return '';
    const date = parseLocalDateString(dateString);
    if (!date) return '';
    return date.toLocaleString(getLocale(), { dateStyle: 'medium', timeStyle: 'short' });
  }

  // Clicking the row toggles its details, except over the actions cell, whose
  // controls (menus, view, expand) already handle their own clicks.
  function handleRowClick(event: MouseEvent) {
    if ((event.target as HTMLElement).closest('.row-actions-cell')) return;
    onToggleExpand();
  }
</script>

<tr
  class="cursor-pointer {index % 2 === 0
    ? 'bg-[var(--color-base-100)]'
    : 'bg-[var(--color-base-200)]'}"
  onclick={handleRowClick}
>
  <td>{formatDate(timestamp)}</td>
  <td>
    <div class="flex items-center">
      <TimeOfDayIcon
        timeOfDay={timeOfDay as TimeOfDayValue | undefined}
        datetime={timestamp}
        className="mr-1"
      />
      <span>{timeOfDay || t('search.detailsPanel.unknownSpecies')}</span>
    </div>
  </td>
  <td>
    <div class="flex items-center gap-2">
      <div
        class="w-12 h-9 rounded-md overflow-hidden bg-gray-100 shrink-0 cursor-pointer hover:ring-2 hover:ring-primary transition-all focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]"
        onclick={e => {
          // The row toggles on click too; stop here so the event doesn't bubble
          // up and immediately re-toggle (expand then collapse).
          e.stopPropagation();
          onToggleExpand();
        }}
        onmousedown={e => {
          // Suppress focus-on-mousedown so a plain click doesn't leave the
          // persistent focus ring lit (browsers apply :focus-visible to
          // custom role="button" elements on click, unlike native <button>).
          // Keyboard users still get the ring via Tab, which focuses without
          // a mousedown event.
          e.preventDefault();
        }}
        onkeydown={e => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            onToggleExpand();
          }
        }}
        aria-label={expandLabel}
        aria-expanded={expanded}
        role="button"
        tabindex="0"
      >
        <!-- PERFORMANCE OPTIMIZATION: Enhanced image loading attributes -->
        <!-- loading="lazy": Defer loading until image enters viewport -->
        <!-- decoding="async": Decode image off-main-thread to prevent UI blocking -->
        <!-- fetchpriority="low": Lower network priority for species thumbnails -->
        <img
          src={buildAppUrl(
            `/api/v2/media/species-image?name=${encodeURIComponent(scientificName)}`
          )}
          alt={speciesLabel}
          class="w-full h-full object-cover"
          onload={e => {
            (e.currentTarget as HTMLImageElement).classList.remove('p-2');
          }}
          onerror={e => {
            (e.currentTarget as HTMLImageElement).classList.add('p-2');
            handleBirdImageError(e);
          }}
          loading="lazy"
          decoding="async"
          fetchpriority="low"
        />
      </div>
      <div>
        <div class="font-bold">{speciesLabel}</div>
        <div class="text-xs opacity-50">{scientificName || ''}</div>
      </div>
    </div>
  </td>
  <td>
    <div class="flex items-center">
      <div class="flex items-center gap-2 w-full">
        <div
          class="w-16 h-4 rounded-full overflow-hidden bg-[var(--color-base-200)]"
          role="progressbar"
          aria-valuenow={confidencePercent}
          aria-valuemin="0"
          aria-valuemax="100"
          aria-valuetext="{confidencePercent}%"
        >
          <div
            class="h-full {confidence >= 0.8
              ? 'bg-[var(--color-success)]'
              : confidence >= 0.4
                ? 'bg-[var(--color-warning)]'
                : 'bg-[var(--color-error)]'}"
            style:width="{confidence * 100}%"
          ></div>
        </div>
        <span class="ml-1 font-semibold">{confidencePercent}%</span>
      </div>
    </div>
  </td>
  <td>
    <SourceBadge detection={{ source }} variant="inline" />
  </td>
  <td>
    <div class="flex gap-1 flex-wrap">
      <div
        class="status-badge {verified === 'correct'
          ? 'correct'
          : verified === 'false_positive'
            ? 'false'
            : 'unverified'}"
      >
        {verified === 'correct'
          ? t('search.statusBadges.verified')
          : verified === 'false_positive'
            ? t('common.review.status.falsePositive')
            : t('search.statusBadges.unverified')}
      </div>
      <div class="status-badge {locked ? 'locked' : 'unverified'}">
        {locked ? t('search.statusBadges.locked') : t('search.statusBadges.unlocked')}
      </div>
    </div>
  </td>
  <td class="row-actions-cell">
    <div class="flex gap-1">
      {@render actions?.()}
      <button
        class="btn btn-xs btn-square"
        onclick={onViewDetails}
        aria-label={t('search.detailsPanel.detectionDetailFor', { species: speciesLabel })}
        title={t('search.detailsPanel.detectionDetail')}
      >
        <Eye class="size-4" />
      </button>
      <button
        class="btn btn-xs btn-square expand-btn"
        onclick={e => {
          e.preventDefault();
          onToggleExpand();
        }}
        data-id={detectionId}
        aria-label={expandLabel}
        aria-expanded={expanded}
        aria-controls={rowId}
      >
        <span
          class="transition-transform duration-200"
          class:rotate-180={expanded}
          aria-hidden="true"
        >
          <ChevronDown class="size-4" />
        </span>
      </button>
    </div>
  </td>
</tr>
