<!--
  ModelCardGrid.svelte

  The model gallery grid: one ModelCard per catalog entry, in as many columns as the grid's own
  width holds (no viewport breakpoints, so the open or collapsed sidebar is accounted for).

  Props:
  - entries: Catalog entries, in display order; renders nothing when empty
  - mode: 'installed' or 'available', passed to every card
  - regionNames: Region slug to localized region name
  - activeAction: The gallery action running, with the entry it runs on, or null
  - progress: Download progress of the running install, reinstall or swap
  - pausedReasonId: id of the visible status line saying why actions are paused
  - optimizeEntryIds: Installed entries that have a better variant for this host
  - onAction: Called with the action and the entry when a card's action button is used
  - onCancel: Called with the entry when its Cancel button is used; Cancel is offered only on a card whose install or update runs, never for a reinstall
  - cancellingId: id of the entry whose cancel was requested and not yet settled
  - className: Classes for the root element

  Layout: cards are direct children of the grid and share its rows through subgrid. The grid
  has no row gap, since every empty region track of a card would cost one; each card carries a
  bottom margin instead, and the grid's negative bottom margin takes the last one back. The
  flow-root wrapper keeps that negative margin from eating the spacing a parent gives the grid.
-->
<script lang="ts">
  import ModelCard, {
    type ModelCardAction,
    type ModelCardMode,
  } from '$lib/desktop/features/settings/components/ModelCard.svelte';
  import type { CatalogEntry, DownloadProgress } from '$lib/types/models';
  import { cn } from '$lib/utils/cn';

  interface Props {
    entries: CatalogEntry[];
    mode: ModelCardMode;
    regionNames: ReadonlyMap<string, string>;
    activeAction: { entryId: string; action: ModelCardAction } | null;
    progress: DownloadProgress | null;
    pausedReasonId: string;
    optimizeEntryIds?: ReadonlySet<string>;
    // eslint-disable-next-line no-unused-vars -- the param names document the callback contract
    onAction: (action: ModelCardAction, entry: CatalogEntry) => void;
    // eslint-disable-next-line no-unused-vars -- the param names document the callback contract
    onCancel?: (entry: CatalogEntry) => void;
    cancellingId?: string | null;
    className?: string;
  }

  let {
    entries,
    mode,
    regionNames,
    activeAction,
    progress,
    pausedReasonId,
    optimizeEntryIds,
    onAction,
    onCancel,
    cancellingId = null,
    className,
  }: Props = $props();

  function busyFor(entry: CatalogEntry): ModelCardAction | null {
    return activeAction?.entryId === entry.id ? activeAction.action : null;
  }

  // Only a download (install, reinstall, swap) has progress; a remove has none
  function progressFor(entry: CatalogEntry): DownloadProgress | null {
    const busy = busyFor(entry);
    return busy === 'install' || busy === 'reinstall' ? progress : null;
  }
</script>

{#if entries.length > 0}
  <div class={cn('model-card-grid-root', className)}>
    <div class="model-card-grid">
      {#each entries as entry (entry.id)}
        <ModelCard
          {entry}
          {mode}
          {regionNames}
          busy={busyFor(entry)}
          actionInFlight={activeAction !== null}
          {pausedReasonId}
          progress={progressFor(entry)}
          optimizeAvailable={optimizeEntryIds?.has(entry.id) ?? false}
          onCancel={onCancel && busyFor(entry) === 'install' ? () => onCancel(entry) : undefined}
          cancelling={cancellingId === entry.id}
          {onAction}
        />
      {/each}
    </div>
  </div>
{/if}

<style>
  .model-card-grid-root {
    display: flow-root;
  }

  /* 19rem keeps two columns at 1024 px with the sidebar open and a classic scrollbar */
  .model-card-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(19rem, 100%), 1fr));
    gap: 0 1rem;
    margin-block-end: -1rem;
  }
</style>
