<!--
  SpeciesConfigTable - Sortable table for species configuration rules.

  Replaces the former SpeciesConfigList row-list. Renders the custom species
  configurations as a proper table (Species, Scientific Name, Threshold,
  Interval, Actions, plus edit/delete), consistent with the Active tab, by
  wrapping the shared SortableDataTable.

  @component
-->
<script lang="ts">
  import { Pencil, Trash2, Clock, Settings2, Plus } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { SpeciesConfig } from '$lib/stores/settings';
  import { normalizeForLookup } from '$lib/utils/speciesNames';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import SortableDataTable from '$lib/desktop/components/data/SortableDataTable.svelte';
  import type { Column } from '$lib/desktop/components/data/DataTable.types';

  interface Props {
    configs: Record<string, SpeciesConfig>;
    /**
     * Normalized common name -> scientific name map (the parent's
     * speciesNameMaps.commonToScientific). Resolves the scientific name for the
     * localized display label and the scientific-name column.
     */
    scientificNameMap: Map<string, string>;
    editingSpecies: string | null;
    disabled?: boolean;
    onAdd: () => void;
    onEdit: (_species: string) => void;
    onDelete: (_species: string) => void;
  }

  let {
    configs = {},
    scientificNameMap = new Map<string, string>(),
    editingSpecies = null,
    disabled = false,
    onAdd,
    onEdit,
    onDelete,
  }: Props = $props();

  interface SpeciesConfigRow {
    /** Raw config-map key, preserved for edit/delete and search. */
    species: string;
    displayName: string;
    scientificName: string;
    threshold: number;
    interval: number;
    hasActions: boolean;
  }

  let editorOpen = $derived(editingSpecies !== null);

  // localizeSpeciesName reads the dictionary store, so this re-runs on dictionary
  // load and locale switch.
  let rows = $derived.by((): SpeciesConfigRow[] =>
    Object.entries(configs).map(([species, config]) => {
      const scientificName = scientificNameMap.get(normalizeForLookup(species)) ?? '';
      return {
        species,
        displayName: localizeSpeciesName(scientificName || undefined, species),
        scientificName,
        threshold: config.threshold ?? 0,
        interval: config.interval ?? 0,
        hasActions: (config.actions?.length ?? 0) > 0,
      };
    })
  );

  // Derived so column headers re-evaluate t() on locale switch.
  let columns = $derived.by((): Column<SpeciesConfigRow>[] => [
    {
      key: 'species',
      header: t('settings.species.customConfiguration.columnHeaders.species'),
      sortable: true,
      sortValue: item => item.displayName,
      defaultDirection: 'asc',
    },
    {
      key: 'scientificName',
      header: t('settings.species.customConfiguration.table.columnScientificName'),
      sortable: true,
      sortValue: item => item.scientificName,
      defaultDirection: 'asc',
    },
    {
      key: 'threshold',
      header: t('settings.species.customConfiguration.labels.threshold'),
      sortable: true,
      sortValue: item => item.threshold,
      defaultDirection: 'desc',
      width: '8rem',
    },
    {
      key: 'interval',
      header: t('settings.species.customConfiguration.labels.interval'),
      sortable: true,
      sortValue: item => item.interval,
      defaultDirection: 'desc',
      width: '6rem',
    },
    {
      key: 'actions',
      header: t('settings.species.customConfiguration.columnHeaders.actions'),
      sortable: true,
      sortValue: item => (item.hasActions ? 1 : 0),
      defaultDirection: 'desc',
      width: '7rem',
    },
    {
      key: 'rowActions',
      header: '',
      align: 'right',
      width: '5rem',
    },
  ]);

  function searchAccessor(item: SpeciesConfigRow): string {
    return `${item.displayName} ${item.species} ${item.scientificName}`;
  }

  function rowClass(item: SpeciesConfigRow): string {
    return item.species === editingSpecies
      ? 'bg-[var(--color-primary)]/5 ring-1 ring-inset ring-[var(--color-primary)]/30'
      : '';
  }

  // Edit is blocked while a different species' editor is open: the inline editor
  // is single-instance. Surface the reason so the disabled state is never silent.
  function editDisabled(species: string): boolean {
    return disabled || (editorOpen && editingSpecies !== species);
  }

  // Every disabled state must explain itself: page-busy takes precedence over the
  // single-editor lock, which takes precedence over the normal edit label.
  function editTitle(species: string): string {
    if (disabled) {
      return t('settings.species.customConfiguration.table.actionsDisabledBusy');
    }
    if (editorOpen && editingSpecies !== species) {
      return t('settings.species.customConfiguration.table.editDisabledReason');
    }
    return t('settings.species.customConfiguration.list.editTitle');
  }

  function deleteTitle(): string {
    if (disabled) {
      return t('settings.species.customConfiguration.table.actionsDisabledBusy');
    }
    return t('settings.species.customConfiguration.list.removeTitle');
  }
</script>

<SortableDataTable
  {columns}
  data={rows}
  {rowClass}
  searchable
  searchPlaceholder={t('settings.species.customConfiguration.table.searchPlaceholder')}
  {searchAccessor}
  defaultSortKey="species"
  title={t('settings.species.customConfiguration.title')}
  emptyTitle={t('settings.species.customConfiguration.emptyState.title')}
  emptyDescription={t('settings.species.customConfiguration.emptyState.description')}
  noResultsMessage={t('settings.species.customConfiguration.table.noResults')}
  resizable={false}
  keyFn={item => item.species}
>
  {#snippet icon()}
    <div class="p-1.5 rounded-lg bg-teal-500/10">
      <Settings2 class="w-4 h-4 text-teal-500" />
    </div>
  {/snippet}

  {#snippet emptyIcon()}
    <Settings2 class="size-12 mx-auto mb-3 opacity-20 text-[var(--color-base-content)]" />
  {/snippet}

  {#snippet headerActions()}
    {#if !editorOpen}
      <button
        type="button"
        class="inline-flex items-center justify-center gap-2 h-8 px-3 text-xs font-medium rounded-lg bg-teal-500 text-white hover:bg-teal-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        data-testid="add-configuration-button"
        onclick={onAdd}
        {disabled}
      >
        <Plus class="size-3.5" />
        {t('settings.species.customConfiguration.addConfiguration')}
      </button>
    {/if}
  {/snippet}

  {#snippet renderCell({ column, item })}
    {#if column.key === 'species'}
      <span class="text-sm font-medium text-[var(--color-base-content)]">{item.displayName}</span>
    {:else if column.key === 'scientificName'}
      {#if item.scientificName}
        <span class="text-xs text-muted italic">{item.scientificName}</span>
      {/if}
    {:else if column.key === 'threshold'}
      <div class="flex items-center gap-2">
        <div class="w-20 h-1.5 rounded-full bg-[var(--color-base-300)] overflow-hidden">
          <div
            class="h-full rounded-full bg-teal-500 transition-all"
            style:width="{(item.threshold * 100).toFixed(0)}%"
          ></div>
        </div>
        <span class="font-mono tabular-nums text-xs">{item.threshold.toFixed(2)}</span>
      </div>
    {:else if column.key === 'interval'}
      {#if item.interval > 0}
        <span class="inline-flex items-center gap-1 text-xs">
          <Clock class="size-3" aria-hidden="true" />
          {item.interval}s
        </span>
      {:else}
        <span
          class="text-xs text-muted"
          title={t('settings.species.customConfiguration.table.intervalDefaultTooltip')}
        >
          {t('settings.species.customConfiguration.table.intervalDefault')}
        </span>
      {/if}
    {:else if column.key === 'actions'}
      {#if item.hasActions}
        <span
          class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-teal-500/10 text-teal-600 dark:text-teal-400"
        >
          {t('settings.species.customConfiguration.badges.customAction')}
        </span>
      {:else}
        <span class="text-xs text-muted"
          >{t('settings.species.customConfiguration.table.noActions')}</span
        >
      {/if}
    {:else if column.key === 'rowActions'}
      <div class="flex items-center gap-1 justify-end">
        <button
          type="button"
          class="inline-flex items-center justify-center size-7 rounded-md text-[var(--color-base-content)]/70 hover:bg-[var(--color-base-200)] hover:text-[var(--color-base-content)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          title={editTitle(item.species)}
          aria-label={editTitle(item.species)}
          disabled={editDisabled(item.species)}
          onclick={() => onEdit(item.species)}
        >
          <Pencil class="size-3.5" />
        </button>
        <button
          type="button"
          class="inline-flex items-center justify-center size-7 rounded-md text-[var(--color-base-content)]/70 hover:bg-[var(--color-error)]/10 hover:text-[var(--color-error)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          title={deleteTitle()}
          aria-label={deleteTitle()}
          {disabled}
          onclick={() => onDelete(item.species)}
        >
          <Trash2 class="size-3.5" />
        </button>
      </div>
    {/if}
  {/snippet}
</SortableDataTable>
