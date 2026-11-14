<!--
  SpeciesChangeDialog - the model's other predictions for a recording, and the
  place where a reviewer sets another species.

  The other predictions come first, since the right species is often among them.
  A search covers the rest: by default over the species the range filter expects
  at the station, and over every species the models know once the reviewer turns
  that on. Nothing is saved until the reviewer confirms.

  A visitor who cannot change the species still gets the list of predictions;
  the search and the confirm button are then left out.

  The dialog keeps one size while it is used: the results area has a fixed
  height, and the footer buttons keep their labels and place.
-->
<script lang="ts">
  import { Check } from '@lucide/svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import ToggleField from '$lib/desktop/components/forms/ToggleField.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import { t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { ApiError, fetchWithCSRF } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { generateId } from '$lib/utils/uuid';
  import { formatScore } from './detailFormat';
  import type { AlternatePrediction, DetectionDetailData } from './detailTypes';
  import {
    loadSpeciesCatalog,
    searchSpecies,
    type CatalogScope,
    type CatalogSpecies,
    type IndexedSpecies,
  } from './speciesCatalog';

  interface Props {
    isOpen: boolean;
    detection: DetectionDetailData;
    speciesName: string;
    /** The model's other predictions for the recording, best first. */
    alternates: AlternatePrediction[];
    /** The model that made the predictions, for the list heading. */
    predictionModel: string;
    /** Whether the viewer may change the species now. */
    canChange: boolean;
    /** Why the species cannot be changed; shown when canChange is false. */
    blockedReason: string;
    onClose: () => void;
    /** Called after the species was changed, so the page can reload the detection. */
    onChanged: () => void;
  }

  let {
    isOpen,
    detection,
    speciesName,
    alternates,
    predictionModel,
    canChange,
    blockedReason,
    onClose,
    onChanged,
  }: Props = $props();

  const logger = loggers.ui;
  const MAX_RESULTS = 6;
  const MIN_QUERY_LENGTH = 2;
  const confirmHelpId = generateId('species-confirm-help');
  const searchId = generateId('species-search');
  // The server's answer for a species it has no label row for yet.
  const HTTP_BAD_REQUEST = 400;

  type CatalogState = 'loading' | 'ready' | 'failed';

  let query = $state('');
  let selected = $state<CatalogSpecies | null>(null);
  // False searches the range filter's species, true every species the models know.
  let allSpecies = $state(false);
  let catalogState = $state<CatalogState>('loading');
  let catalog = $state.raw<IndexedSpecies[]>([]);
  let saving = $state(false);
  let errorMessage = $state('');

  let scope = $derived<CatalogScope>(allSpecies ? 'all' : 'range');

  // Each opening starts clean, on the range filter's list.
  $effect(() => {
    if (!isOpen) return;
    query = '';
    selected = null;
    errorMessage = '';
    allSpecies = false;
  });

  // Loads the list for the chosen scope while the dialog is open. A list that
  // arrives after the scope changed again is dropped.
  $effect(() => {
    if (!isOpen || !canChange) return;
    const wanted = scope;
    let current = true;
    catalogState = 'loading';
    loadSpeciesCatalog(wanted)
      .then(species => {
        if (!current) return;
        catalog = species;
        catalogState = 'ready';
      })
      .catch((error: unknown) => {
        if (!current) return;
        logger.error('Failed to load species list', error, { scope: wanted });
        catalog = [];
        catalogState = 'failed';
      });
    return () => {
      current = false;
    };
  });

  let results = $derived(
    query.trim().length >= MIN_QUERY_LENGTH
      ? // The detection's own species is left out, since it cannot be changed to
        // itself. One extra result is asked for so that leaving it out does not
        // shorten the page.
        searchSpecies(catalog, query, MAX_RESULTS + 1)
          .filter(item => item.scientificName !== detection.scientificName)
          .slice(0, MAX_RESULTS)
      : []
  );

  function alternateSpecies(item: AlternatePrediction): CatalogSpecies {
    return {
      scientificName: item.scientificName,
      commonName: localizeSpeciesName(item.scientificName, item.commonName ?? item.scientificName),
    };
  }

  function pick(item: CatalogSpecies) {
    if (canChange) selected = item;
  }

  async function confirm() {
    if (!selected || saving || !canChange) return;
    saving = true;
    errorMessage = '';
    try {
      await fetchWithCSRF(`/api/v2/detections/${detection.id}/species`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ scientificName: selected.scientificName }),
      });
      toastActions.success(
        t('detections.detail.species.changed', { species: selected.commonName })
      );
      onChanged();
      onClose();
    } catch (error) {
      logger.error('Failed to change species', error, { action: 'changeSpecies' });
      errorMessage =
        error instanceof ApiError && error.status === HTTP_BAD_REQUEST
          ? t('detections.detail.species.notKnownHere', { species: selected.commonName })
          : t('detections.detail.species.changeFailed');
    } finally {
      saving = false;
    }
  }
</script>

{#snippet option(item: CatalogSpecies, detail: string, original: boolean)}
  {@const active = selected?.scientificName === item.scientificName}
  <li>
    <button
      type="button"
      class="dd-species-option"
      class:dd-species-option-active={active}
      class:dd-species-option-static={!canChange}
      aria-pressed={canChange ? active : undefined}
      aria-disabled={canChange ? undefined : 'true'}
      onclick={() => pick(item)}
    >
      {#if canChange}
        <span class="dd-species-check" aria-hidden="true">
          {#if active}<Check class="size-3.5" />{/if}
        </span>
      {/if}
      <span class="dd-species-names">
        <span class="dd-species-common">{item.commonName}</span>
        <span class="dd-species-scientific">{item.scientificName}</span>
        {#if original}
          <span class="dd-species-original">{t('detections.detail.species.modelPick')}</span>
        {/if}
      </span>
      {#if detail}
        <span class="dd-species-detail">{detail}</span>
      {/if}
    </button>
  </li>
{/snippet}

<Modal
  {isOpen}
  title={canChange
    ? t('detections.detail.species.dialogTitle')
    : t('detections.detail.species.predictionsTitle')}
  size="lg"
  scrollBody={true}
  loading={saving}
  {onClose}
>
  {#snippet children()}
    <p class="dd-species-current">
      {t('detections.detail.species.current')}
      <strong>{speciesName}</strong>
      <span class="dd-species-scientific">{detection.scientificName}</span>
    </p>

    <h3 class="dd-species-heading">
      {t('detections.detail.species.fromPredictions')}
      {#if predictionModel}
        <span class="dd-species-heading-detail">{predictionModel}</span>
      {/if}
    </h3>
    {#if alternates.length > 0}
      <ul class="dd-species-list dd-species-alternates">
        {#each alternates as item (item.scientificName)}
          {@render option(
            alternateSpecies(item),
            formatScore(item.confidence),
            item.original === true
          )}
        {/each}
      </ul>
    {:else}
      <p class="dd-species-note">{t('detections.detail.species.noPredictions')}</p>
    {/if}

    {#if canChange}
      <div class="dd-species-search">
        <div class="dd-species-search-head">
          <label class="dd-species-heading dd-species-search-label" for={searchId}>
            {t('detections.detail.species.search')}
          </label>
          <ToggleField
            variant="inline"
            label={t('detections.detail.species.allSpecies')}
            value={allSpecies}
            onUpdate={value => (allSpecies = value)}
          />
        </div>
        <TextInput
          id={searchId}
          bind:value={query}
          type="search"
          size="md"
          placeholder={allSpecies
            ? t('detections.detail.species.searchAllPlaceholder')
            : t('detections.detail.species.searchRangePlaceholder')}
          autocomplete="off"
        />
      </div>
      <div class="dd-species-results" aria-live="polite">
        {#if catalogState === 'loading'}
          <p class="dd-species-note">{t('detections.detail.species.loadingList')}</p>
        {:else if catalogState === 'failed'}
          <p class="dd-species-note dd-species-error">
            {t('detections.detail.species.listFailed')}
          </p>
        {:else if query.trim().length < MIN_QUERY_LENGTH}
          <p class="dd-species-note">
            {allSpecies
              ? t('detections.detail.species.searchHintAll', { count: catalog.length })
              : t('detections.detail.species.searchHintRange', { count: catalog.length })}
          </p>
        {:else if results.length === 0}
          <p class="dd-species-note">
            {allSpecies
              ? t('detections.detail.species.noMatches')
              : t('detections.detail.species.noMatchesInRange')}
          </p>
        {:else}
          <ul class="dd-species-list">
            {#each results as item (item.scientificName)}
              {@render option(item, '', false)}
            {/each}
          </ul>
        {/if}
      </div>

      <p class="dd-species-effect">{t('detections.detail.species.effect')}</p>
      {#if errorMessage}
        <p class="dd-species-error" role="alert">{errorMessage}</p>
      {/if}
    {:else}
      <p class="dd-species-effect">{blockedReason}</p>
    {/if}
  {/snippet}

  {#snippet footer()}
    {#if canChange}
      <!-- The hint and the picked species share this slot, and the button labels
           do not change, so picking a species moves neither button. -->
      <p
        id={confirmHelpId}
        class="dd-species-choice"
        class:dd-species-choice-picked={selected !== null}
        title={selected?.commonName}
      >
        {selected
          ? t('detections.detail.species.confirm', { species: selected.commonName })
          : t('detections.detail.species.pickFirst')}
      </p>
      <Button variant="ghost" size="md" onclick={onClose}>{t('common.buttons.cancel')}</Button>
      <Button
        variant="primary"
        size="md"
        aria-disabled={!selected || saving ? 'true' : undefined}
        aria-describedby={confirmHelpId}
        onclick={confirm}
      >
        {t('detections.detail.species.confirmEmpty')}
      </Button>
    {:else}
      <Button variant="default" size="md" onclick={onClose}>{t('common.close')}</Button>
    {/if}
  {/snippet}
</Modal>

<style>
  .dd-species-current {
    font-size: 0.875rem;
  }

  .dd-species-current strong {
    margin: 0 0.25rem;
  }

  .dd-species-heading {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
    margin: 1rem 0 0.375rem;
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-species-heading-detail {
    font-weight: 400;
    letter-spacing: 0;
    text-transform: none;
  }

  .dd-species-search {
    margin-top: 1rem;
  }

  .dd-species-search-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 0.375rem;
  }

  .dd-species-search-label {
    margin: 0;
  }

  .dd-species-list {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  /* A long list of predictions scrolls inside its own box instead of pushing the
     search field down. */
  .dd-species-alternates {
    max-height: 11rem;
    overflow-y: auto;
  }

  .dd-species-option {
    display: flex;
    align-items: center;
    gap: 0.625rem;
    width: 100%;
    padding: 0.4375rem 0.625rem;
    text-align: left;
    cursor: pointer;
    border: 1px solid var(--border-100);
    border-radius: var(--radius-field);
  }

  .dd-species-option:hover {
    background-color: var(--color-base-200);
  }

  /* A read-only row: the same look, without the affordances of a choice. */
  .dd-species-option-static {
    cursor: default;
  }

  .dd-species-option-static:hover {
    background-color: transparent;
  }

  .dd-species-option-active {
    border-color: var(--color-primary);
    background-color: color-mix(in srgb, var(--color-primary) 10%, transparent);
  }

  .dd-species-option:focus-visible {
    outline: 2px solid var(--color-primary);
    outline-offset: 1px;
  }

  .dd-species-check {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 1.125rem;
    height: 1.125rem;
    border: 1.5px solid var(--border-300);
    border-radius: 50%;
    color: var(--color-primary-content);
  }

  .dd-species-option-active .dd-species-check {
    border-color: var(--color-primary);
    background-color: var(--color-primary);
  }

  .dd-species-names {
    display: flex;
    flex: 1;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0 0.5rem;
    min-width: 0;
  }

  .dd-species-common {
    font-size: 0.875rem;
    font-weight: 500;
  }

  .dd-species-scientific {
    font-size: 0.8125rem;
    font-style: italic;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-species-original {
    padding: 0 0.375rem;
    font-size: 0.6875rem;
    border: 1px solid var(--border-200);
    border-radius: var(--radius-full);
  }

  .dd-species-detail {
    flex: 0 0 auto;
    font-size: 0.8125rem;
    font-variant-numeric: tabular-nums;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  /* Fixed height for a full page of results, so the dialog keeps its size and
     its controls keep their place while typing. */
  .dd-species-results {
    height: 16.5rem;
    margin-top: 0.5rem;
    overflow-y: auto;
  }

  .dd-species-note {
    font-size: 0.8125rem;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-species-effect {
    margin-top: 0.75rem;
    font-size: 0.8125rem;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-species-error {
    margin-top: 0.375rem;
    font-size: 0.8125rem;
    color: var(--text-error);
  }

  .dd-species-choice {
    flex: 1 1 0;
    align-self: center;
    min-width: 0;
    margin-right: auto;
    overflow: hidden;
    font-size: 0.8125rem;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: color-mix(in srgb, var(--color-base-content) 72%, transparent);
  }

  .dd-species-choice-picked {
    font-weight: 600;
    color: var(--color-base-content);
  }
</style>
