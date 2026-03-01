<!--
  Species List Editor Component

  Purpose: Reusable species list with inline add/edit/delete functionality.
  Used by FilterSettingsPage for both dog bark and daylight filter species management.

  Props:
  - species: Current species array
  - disabled: Whether the parent section is disabled
  - predictions: Autocomplete suggestions for the add input
  - predictionsLoading: Whether predictions are still loading
  - listLabel: Label shown above the species list
  - addLabel: Label for the add species input
  - addPlaceholder: Placeholder for the add input
  - addHelpText: Help text for the add input
  - addButtonText: Button text for adding
  - hasChanges: Whether there are unsaved changes (shows indicator)
  - caseInsensitive: Use case-insensitive duplicate checks (default: true)
  - onSpeciesChange: Callback when the species array changes

  @component
-->
<script lang="ts">
  import SpeciesInput from '$lib/desktop/components/forms/SpeciesInput.svelte';
  import { X, Check, SquarePen, Trash2, Info } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { safeArrayAccess } from '$lib/utils/security';

  interface Props {
    species: string[];
    disabled: boolean;
    predictions: string[];
    predictionsLoading: boolean;
    listLabel: string;
    addLabel: string;
    addPlaceholder: string;
    addHelpText: string;
    addButtonText: string;
    hasChanges: boolean;
    caseInsensitive?: boolean;
    onSpeciesChange: (_updatedSpecies: string[]) => void;
  }

  let {
    species,
    disabled,
    predictions,
    predictionsLoading,
    listLabel,
    addLabel,
    addPlaceholder,
    addHelpText,
    addButtonText,
    hasChanges,
    caseInsensitive = true,
    onSpeciesChange,
  }: Props = $props();

  // Internal edit state
  let newSpecies = $state('');
  let editIndex = $state<number | null>(null);
  let editSpecies = $state('');

  function isDuplicate(value: string, excludeIndex?: number): boolean {
    return species.some((s: string, i: number) => {
      if (excludeIndex !== undefined && i === excludeIndex) return false;
      return caseInsensitive ? s.toLowerCase() === value.toLowerCase() : s === value;
    });
  }

  function handleInput(value: string) {
    newSpecies = value;
  }

  function addEntry(value: string) {
    if (!value.trim()) return;
    const trimmed = value.trim();
    if (isDuplicate(trimmed)) return;
    onSpeciesChange([...species, trimmed]);
  }

  function removeEntry(index: number) {
    onSpeciesChange(species.filter((_: string, i: number) => i !== index));
  }

  function startEdit(index: number) {
    editIndex = index;
    editSpecies = safeArrayAccess(species, index) || '';
  }

  function saveEdit() {
    if (editIndex === null || !editSpecies.trim()) return;
    const trimmed = editSpecies.trim();
    if (isDuplicate(trimmed, editIndex)) return;

    const updated = [...species];
    if (editIndex >= 0 && editIndex < updated.length) {
      updated.splice(editIndex, 1, trimmed);
    }
    onSpeciesChange(updated);
    cancelEdit();
  }

  function cancelEdit() {
    editIndex = null;
    editSpecies = '';
  }

  function focusAndSelect(node: HTMLInputElement) {
    node.focus();
    node.select();
  }

  function handleEditKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      saveEdit();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      cancelEdit();
    }
  }
</script>

<div class="mt-6">
  <div class="flex justify-start mb-1">
    <span class="text-sm text-[var(--color-base-content)]">{listLabel}</span>
  </div>

  <!-- Species List -->
  {#if species.length > 0}
    <div class="space-y-2 mb-4">
      {#each species as entry, index (entry)}
        <div class="flex items-center gap-2 p-3 bg-[var(--color-base-200)] rounded-lg">
          {#if editIndex === index}
            <input
              use:focusAndSelect
              type="text"
              bind:value={editSpecies}
              aria-label={t('settings.filters.speciesNamePlaceholder')}
              class="flex-1 h-8 px-3 text-sm bg-[var(--color-base-100)] border border-[var(--border-200)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
              onkeydown={handleEditKeydown}
              placeholder={t('settings.filters.speciesNamePlaceholder')}
            />
            <button
              type="button"
              class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-success)] text-[var(--color-success-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-success)] focus-visible:ring-offset-2 transition-colors"
              onclick={saveEdit}
              aria-label={t('common.aria.saveChanges')}
            >
              <Check class="size-4" />
            </button>
            <button
              type="button"
              class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-black/5 dark:hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-base-content)] focus-visible:ring-offset-2 transition-colors"
              onclick={cancelEdit}
              aria-label={t('common.aria.cancelEdit')}
            >
              <X class="size-4" />
            </button>
          {:else}
            <span class="flex-1 text-sm">{entry}</span>
            <button
              type="button"
              class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-black/5 dark:hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-base-content)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={() => startEdit(index)}
              disabled={disabled || editIndex !== null}
              aria-label={t('common.aria.editSpecies')}
            >
              <SquarePen class="size-4" />
            </button>
            <button
              type="button"
              class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-error)] text-[var(--color-error-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-error)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={() => removeEntry(index)}
              disabled={disabled || editIndex !== null}
              aria-label={t('common.aria.removeSpecies')}
            >
              <Trash2 class="size-4" />
            </button>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  <!-- Add New Species -->
  <SpeciesInput
    bind:value={newSpecies}
    label={addLabel}
    placeholder={addPlaceholder}
    helpText={addHelpText}
    disabled={disabled || predictionsLoading}
    {predictions}
    size="sm"
    buttonText={addButtonText}
    buttonIcon={true}
    onInput={handleInput}
    onAdd={addEntry}
  />

  <!-- Unsaved Changes Indicator -->
  {#if hasChanges}
    <div class="mt-2 text-xs text-[var(--color-info)] flex items-center gap-1">
      <Info class="size-4" />
      <span>{t('settings.actions.unsavedChanges')}</span>
    </div>
  {/if}
</div>
