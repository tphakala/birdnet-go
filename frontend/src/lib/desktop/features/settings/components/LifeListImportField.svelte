<!--
  LifeListImportField.svelte

  Purpose: Upload an eBird life-list CSV export, parse it client-side, and
  report import results. Deliberately simple: a new upload *replaces* the
  whole life list (see onImport), so there's no per-species table, search,
  or delete to maintain — to fix a bad entry, fix the CSV (or your eBird
  list) and re-upload. Entries are not validated against BirdNET's own
  species catalog (an eBird export can legitimately contain species BirdNET
  doesn't classify) — matching against detections happens continuously on
  the backend by scientific name, not at import time.

  Props:
  - speciesCount: number - Current number of life-list entries, for the count badge
  - disabled: boolean - Disable all interactions
  - onImport: (entries: string[]) => void - Called with the parsed entries; replaces the whole list
  - onClear: () => void - Called (after confirmation) to empty the whole list

  @component
-->
<script lang="ts">
  import { Upload, TriangleAlert, Trash2, ExternalLink } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { readFileAsText } from '$lib/utils/fileHelpers';
  import {
    parseLifeListCsv,
    EBIRD_TAXONOMY_HELP_URL,
    type LifeListParseRejection,
  } from '$lib/utils/lifeListCsv';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import Badge from '$lib/desktop/components/ui/Badge.svelte';
  import EmptyState from '$lib/desktop/features/settings/components/EmptyState.svelte';

  // eBird's own direct CSV export link for a visitor's world life list —
  // saves the user from hunting through eBird's UI for the export option.
  const EBIRD_LIFE_LIST_EXPORT_URL = 'https://ebird.org/lifelist?r=world&time=life&fmt=csv';

  interface Props {
    speciesCount: number;
    disabled?: boolean;
    onImport: (_entries: string[]) => void;
    onClear: () => void;
  }

  let { speciesCount, disabled = false, onImport, onClear }: Props = $props();

  let fileInput: HTMLInputElement;
  let confirmClearOpen = $state(false);

  function handleConfirmClear() {
    onClear();
    importSummary = null;
    confirmClearOpen = false;
  }

  // Last import's summary, cleared when the user dismisses it or imports again.
  let importSummary = $state<{
    acceptedCount: number;
    totalRows: number;
    rejected: LifeListParseRejection[];
  } | null>(null);
  let showRejectedRows = $state(false);

  function triggerFileBrowse() {
    if (!disabled) fileInput?.click();
  }

  async function handleFileSelect(event: Event) {
    if (disabled) return;
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;

    try {
      const text = await readFileAsText(file, 5_242_880); // 5MB — generous for even a large life list
      const result = parseLifeListCsv(text);

      if (result.accepted.length === 0 && result.rejected.length === 0) {
        toastActions.error(t('settings.species.lifeList.emptyFile'));
        return;
      }

      importSummary = {
        acceptedCount: result.accepted.length,
        totalRows: result.accepted.length + result.rejected.length,
        rejected: result.rejected,
      };
      showRejectedRows = false;

      // Replaces the whole life list — see component doc comment.
      onImport(result.accepted);
    } catch (err) {
      toastActions.error(
        err instanceof Error ? err.message : t('settings.species.lifeList.fileReadError')
      );
    } finally {
      input.value = '';
    }
  }
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl shadow-sm">
  <!-- Header -->
  <div class="flex items-center gap-2 px-4 py-3 border-b border-[var(--border-100)]">
    <div class="p-1.5 rounded-lg bg-[color-mix(in_srgb,var(--color-success)_10%,transparent)]">
      <Upload class="w-4 h-4 text-[var(--color-success)]" />
    </div>
    <h3 class="text-xs font-semibold uppercase tracking-wider text-muted">
      {t('settings.species.lifeList.title')}
    </h3>
    <Badge
      variant="neutral"
      size="sm"
      text={t('settings.species.lifeList.speciesCount', { count: speciesCount })}
    />
  </div>

  <!-- Import summary, shown after a CSV import until dismissed -->
  {#if importSummary}
    <div class="px-4 py-3 border-b border-[var(--border-100)] bg-[var(--color-base-200)] text-sm">
      <p>
        {t('settings.species.lifeList.importSummary', {
          accepted: importSummary.acceptedCount,
          total: importSummary.totalRows,
        })}
      </p>
      {#if importSummary.rejected.length > 0}
        <button
          type="button"
          class="mt-1 text-xs text-[var(--color-warning)] underline cursor-pointer"
          onclick={() => (showRejectedRows = !showRejectedRows)}
        >
          {showRejectedRows
            ? t('settings.species.lifeList.hideRejectedRows')
            : t('settings.species.lifeList.showRejectedRows', {
                count: importSummary.rejected.length,
              })}
        </button>
        {#if showRejectedRows}
          <ul class="mt-2 space-y-1 text-xs text-muted max-h-40 overflow-y-auto">
            {#each importSummary.rejected as rejection (rejection.rowNumber)}
              <li class="flex items-start gap-1.5">
                <TriangleAlert class="w-3 h-3 shrink-0 mt-0.5 text-[var(--color-warning)]" />
                <span>
                  {t('settings.species.lifeList.rejectedRow', { row: rejection.rowNumber })}: {rejection.reason}
                </span>
              </li>
            {/each}
          </ul>
          <a
            href={EBIRD_TAXONOMY_HELP_URL}
            target="_blank"
            rel="noopener noreferrer"
            class="mt-2 inline-block text-xs text-[var(--color-info)] hover:underline"
          >
            {t('settings.species.lifeList.learnAboutTaxonomy')}
          </a>
        {/if}
      {/if}
      <button
        type="button"
        class="mt-2 text-xs text-muted underline cursor-pointer"
        onclick={() => (importSummary = null)}
      >
        {t('common.ui.dismiss')}
      </button>
    </div>
  {/if}

  {#if speciesCount === 0 && !importSummary}
    <EmptyState
      icon={Upload}
      title={t('settings.species.lifeList.emptyMessage')}
      description={t('settings.species.lifeList.emptyMessageHint')}
    />
  {/if}

  <!-- Import control -->
  <div
    class="px-4 py-3 {speciesCount > 0 || importSummary
      ? 'border-t border-[var(--border-100)]'
      : ''}"
  >
    <div class="flex flex-wrap items-center gap-2">
      <button
        type="button"
        class="inline-flex items-center gap-1.5 px-3.5 py-2 text-sm font-medium rounded-lg bg-[var(--color-base-200)] border border-[var(--color-base-300)] transition-all disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer hover:bg-[var(--color-base-300)]"
        {disabled}
        onclick={triggerFileBrowse}
      >
        <Upload class="w-4 h-4" aria-hidden="true" />
        {t('settings.species.lifeList.uploadCsv')}
      </button>
      {#if speciesCount > 0}
        <button
          type="button"
          class="inline-flex items-center gap-1.5 px-3.5 py-2 text-sm font-medium rounded-lg bg-[color-mix(in_srgb,var(--color-error)_10%,transparent)] text-[var(--color-error)] border border-[color-mix(in_srgb,var(--color-error)_20%,transparent)] transition-all disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer hover:bg-[color-mix(in_srgb,var(--color-error)_20%,transparent)]"
          {disabled}
          onclick={() => (confirmClearOpen = true)}
        >
          <Trash2 class="w-4 h-4" aria-hidden="true" />
          {t('settings.species.lifeList.clearList')}
        </button>
      {/if}
    </div>
    <input
      bind:this={fileInput}
      type="file"
      accept=".csv"
      class="hidden"
      onchange={handleFileSelect}
      tabindex={-1}
    />
    <p class="mt-1.5 text-xs text-muted">
      {t('settings.species.lifeList.uploadHelp')}
      <a
        href={EBIRD_LIFE_LIST_EXPORT_URL}
        target="_blank"
        rel="noopener noreferrer"
        class="inline-flex items-center gap-0.5 text-[var(--color-info)] hover:underline"
      >
        {t('settings.species.lifeList.getFromEbird')}
        <ExternalLink class="size-3" aria-hidden="true" />
      </a>
    </p>
  </div>
</div>

<ConfirmModal
  isOpen={confirmClearOpen}
  title={t('settings.species.lifeList.clearConfirmTitle')}
  message={t('settings.species.lifeList.clearConfirmMessage')}
  confirmVariant="error"
  onClose={() => (confirmClearOpen = false)}
  onConfirm={handleConfirmClear}
/>
