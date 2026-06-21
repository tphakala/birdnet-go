<!--
  SpeciesTable.svelte

  Purpose: Sortable table for displaying active species data with score bars,
  status badges, search, and CSV download. A thin wrapper over the shared
  SortableDataTable; the public props are unchanged.

  Props:
  - species: ActiveSpecies[] - Species data to display
  - loading: boolean - Show loading spinner
  - searchable: boolean - Show search input
  - onDownloadCsv: () => void - CSV download callback
  - title: string - Table title
  - description: string - Table description

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { Download, Bird } from '@lucide/svelte';
  import SortableDataTable from '$lib/desktop/components/data/SortableDataTable.svelte';
  import type { Column } from '$lib/desktop/components/data/DataTable.types';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';

  interface ActiveSpeciesItem {
    commonName: string;
    scientificName: string;
    score: number;
    isManuallyIncluded: boolean;
    hasCustomConfig: boolean;
  }

  /** A species row paired with the visitor-locale display name. */
  type LocalizedSpeciesItem = ActiveSpeciesItem & { displayName: string };

  interface Props {
    species: ActiveSpeciesItem[];
    loading?: boolean;
    searchable?: boolean;
    onDownloadCsv?: () => void;
    title?: string;
    description?: string;
  }

  let {
    species = [],
    loading = false,
    searchable = true,
    onDownloadCsv,
    title,
    description,
  }: Props = $props();

  // Pair each row with its visitor-locale display name. localizeSpeciesName reads
  // the dictionary store, so this re-runs on dictionary load and locale switch.
  let localizedSpecies = $derived.by((): LocalizedSpeciesItem[] =>
    species.map(s => ({
      ...s,
      displayName: localizeSpeciesName(s.scientificName, s.commonName),
    }))
  );

  // Derived so column headers re-evaluate t() on locale switch.
  let columns = $derived.by((): Column<LocalizedSpeciesItem>[] => [
    {
      key: 'commonName',
      header: t('settings.species.activeSpecies.columns.commonName'),
      sortable: true,
      sortValue: item => item.displayName,
      defaultDirection: 'asc',
    },
    {
      key: 'scientificName',
      header: t('settings.species.activeSpecies.columns.scientificName'),
      sortable: true,
      sortValue: item => item.scientificName,
      defaultDirection: 'asc',
    },
    {
      key: 'score',
      header: t('settings.species.activeSpecies.columns.score'),
      sortable: true,
      sortValue: item => item.score,
      defaultDirection: 'desc',
    },
    {
      key: 'status',
      header: t('settings.species.activeSpecies.columns.status'),
      align: 'right',
      width: '12rem',
    },
  ]);

  // Match the localized name the visitor sees, plus the server common name and
  // scientific name so search keeps working regardless of locale.
  function searchAccessor(item: LocalizedSpeciesItem): string {
    return `${item.displayName} ${item.commonName} ${item.scientificName}`;
  }
</script>

<SortableDataTable
  {columns}
  data={localizedSpecies}
  {loading}
  {title}
  {description}
  {searchable}
  searchPlaceholder={t('settings.species.activeSpecies.search.placeholder')}
  searchAccessor={searchable ? searchAccessor : undefined}
  defaultSortKey="score"
  emptyTitle={t('settings.species.activeSpecies.empty.title')}
  emptyDescription={t('settings.species.activeSpecies.empty.description')}
  noResultsMessage={t('settings.species.activeSpecies.noResults')}
  keyFn={item => `${item.scientificName}_${item.commonName}`}
>
  {#snippet icon()}
    <div class="p-1.5 rounded-lg bg-[color-mix(in_srgb,var(--color-success)_10%,transparent)]">
      <Bird class="w-4 h-4 text-[var(--color-success)]" />
    </div>
  {/snippet}

  {#snippet emptyIcon()}
    <Bird class="size-12 mx-auto mb-3 opacity-30" />
  {/snippet}

  {#snippet headerActions()}
    {#if onDownloadCsv}
      <button
        type="button"
        class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium rounded-lg transition-colors cursor-pointer hover:bg-black/[0.05] dark:hover:bg-white/[0.05] text-muted disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={onDownloadCsv}
        disabled={species.length === 0}
        title={t('settings.species.activeSpecies.downloadCsv')}
        aria-label={t('settings.species.activeSpecies.downloadCsv')}
      >
        <Download class="w-3.5 h-3.5" aria-hidden="true" />
        <span>CSV</span>
      </button>
    {/if}
  {/snippet}

  {#snippet renderCell({ column, item })}
    {#if column.key === 'commonName'}
      <span class="font-medium text-sm">{item.displayName}</span>
    {:else if column.key === 'scientificName'}
      <span class="text-xs text-muted italic">{item.scientificName}</span>
    {:else if column.key === 'score'}
      <div class="flex items-center gap-2">
        <div class="w-16 h-1.5 rounded-full overflow-hidden bg-[var(--surface-300)]">
          <div
            class="h-full rounded-full bg-primary transition-[width] duration-600 ease-out"
            style:width="{item.score * 100}%"
          ></div>
        </div>
        <span class="font-mono tabular-nums text-xs">{item.score.toFixed(2)}</span>
      </div>
    {:else if column.key === 'status'}
      <div class="flex items-center gap-1.5 justify-end flex-nowrap">
        {#if item.isManuallyIncluded}
          <span
            class="inline-flex items-center whitespace-nowrap px-2 py-0.5 rounded-full text-[10px] font-medium badge-status-success"
          >
            + {t('settings.species.activeSpecies.badges.included')}
          </span>
        {/if}
        {#if item.hasCustomConfig}
          <span
            class="inline-flex items-center whitespace-nowrap px-2 py-0.5 rounded-full text-[10px] font-medium bg-teal-500/15 text-teal-600 dark:text-teal-400"
          >
            &#9733; {t('settings.species.activeSpecies.badges.configured')}
          </span>
        {/if}
      </div>
    {/if}
  {/snippet}
</SortableDataTable>
