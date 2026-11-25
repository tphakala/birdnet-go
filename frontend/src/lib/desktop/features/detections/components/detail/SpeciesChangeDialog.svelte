<!--
  SpeciesChangeDialog - the model's other predictions for a recording, and the
  place where a reviewer sets another species.

  The other predictions come first, since the right species is often among them.
  A search covers the rest: by default over the species the range filter expects
  at the station, and over every species the models know once the reviewer turns
  that on. Both lists are one radio group: picking a species in one clears the
  other. Nothing is saved until the reviewer confirms.

  A visitor who cannot change the species still gets the list of predictions, as
  plain rows; the search and the confirm button are then left out.

  The dialog keeps one size while it is used: the results area has a fixed
  height, and the footer buttons keep their labels and place.
-->
<script lang="ts">
  import { Check } from '@lucide/svelte';
  import { onDestroy } from 'svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import ToggleField from '$lib/desktop/components/forms/ToggleField.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import { t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { api, ApiError } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { normalizeForLookup } from '$lib/utils/speciesNames';
  import { rankPredictions, toLocalizedPredictions } from '$lib/utils/speciesPredictions';
  import { generateId } from '$lib/utils/uuid';
  import { formatScore } from './detailFormat';
  import type { AlternatePrediction, DetectionDetailData, SavedChange } from './detailTypes';
  import {
    forgetSpeciesCatalogs,
    loadSpeciesCatalog,
    type CatalogScope,
    type CatalogSpecies,
  } from './speciesCatalog';

  interface Props {
    isOpen: boolean;
    detection: DetectionDetailData;
    speciesName: string;
    /** The model's other predictions for the recording, best first. */
    alternates: AlternatePrediction[];
    /** The model that made the predictions, for the list heading. */
    predictionModel: string;
    /** Why the species cannot be changed now; empty when the viewer may change it. */
    blockedReason: string;
    onClose: () => void;
    /**
     * Called after a change to a detection was saved, so the page can reload it. The page
     * may answer with a promise that settles when that reload does: false when it failed.
     */
    onChanged: (_change: SavedChange) => Promise<boolean> | void;
  }

  let {
    isOpen,
    detection,
    speciesName,
    alternates,
    predictionModel,
    blockedReason,
    onClose,
    onChanged,
  }: Props = $props();

  const logger = loggers.ui;
  const MAX_RESULTS = 6;
  const MIN_QUERY_LENGTH = 2;
  // Wait this long after the last keystroke before announcing the number of matches.
  const ANNOUNCE_DELAY_MS = 300;
  const confirmHelpId = generateId('species-confirm-help');
  const searchId = generateId('species-search');
  const choiceName = generateId('species-choice');
  // The answer when the legacy database is in use and when the detection is locked.
  const HTTP_CONFLICT = 409;

  type CatalogState = 'loading' | 'ready' | 'failed';

  let query = $state('');
  let selected = $state<CatalogSpecies | null>(null);

  // The radio is visually hidden, so the browser scrolls its 1 px box into view and not its
  // row: arrowing through a long list would leave the checked row out of sight. Arrow keys
  // move focus with the check, so following focus covers both.
  function revealRow(event: FocusEvent) {
    if (event.currentTarget instanceof window.HTMLElement) {
      event.currentTarget.closest('label')?.scrollIntoView({ block: 'nearest' });
    }
  }
  // False searches the range filter's species, true every species the models know.
  let allSpecies = $state(false);
  let catalogState = $state<CatalogState>('loading');
  let catalog = $state.raw<CatalogSpecies[]>([]);
  let saving = $state(false);
  let errorMessage = $state('');
  let announcement = $state('');

  let canChange = $derived(blockedReason === '');
  let scope = $derived<CatalogScope>(allSpecies ? 'all' : 'range');
  let searching = $derived(query.trim().length >= MIN_QUERY_LENGTH);

  // Counts the openings, so a save can tell whether the opening it was started in is the
  // one on screen when it answers.
  let opening = 0;

  // Each opening starts clean, on the range filter's list as the settings now define it.
  $effect.pre(() => {
    if (!isOpen) return;
    opening += 1;
    forgetSpeciesCatalogs();
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

  // The catalog with each species named in the UI language, the same way as the
  // predictions above it; the value is the scientific name.
  let options = $derived.by(() => {
    const commonNames = new Map(catalog.map(item => [item.scientificName, item.commonName]));
    return toLocalizedPredictions(
      catalog.map(item => item.scientificName),
      scientificName => localizeSpeciesName(scientificName, commonNames.get(scientificName))
    );
  });

  // The species the predictions list above already offers; a search does not list them twice.
  let predicted = $derived(new Set(alternates.map(item => item.scientificName)));

  // The detection's own species is left out, since it cannot be changed to itself.
  // A name the reviewer sees that starts with the query ranks first (rankPredictions'
  // "local" tier): typing "ta" lists "tavi" before a species whose Latin name starts
  // with Ta, and both before names that only contain "ta".
  let results = $derived.by<CatalogSpecies[]>(() => {
    if (!searching) return [];
    const needle = normalizeForLookup(query.trim());
    return rankPredictions(options, query, {
      limit: MAX_RESULTS,
      isLocal: option => (option.normalizedLabel ?? '').startsWith(needle),
      exclude: option => option.value === detection.scientificName || predicted.has(option.value),
    }).map(option => ({ scientificName: option.value, commonName: option.label }));
  });

  // What the results area says, announced once typing pauses instead of on every
  // keystroke; only the count or the reason there are none, never the whole list.
  let resultsSummary = $derived.by(() => {
    if (catalogState !== 'ready' || !searching) return '';
    if (results.length === 0) {
      return allSpecies
        ? t('detections.detail.species.noMatches')
        : t('detections.detail.species.noMatchesInRange');
    }
    return results.length === 1
      ? t('search.resultsCountOne')
      : t('search.resultsCountOther', { count: results.length });
  });

  $effect(() => {
    const text = resultsSummary;
    const timer = globalThis.setTimeout(() => {
      announcement = text;
    }, ANNOUNCE_DELAY_MS);
    return () => globalThis.clearTimeout(timer);
  });

  // The model's other predictions as rows: the species in the UI language, the score,
  // and whether it was the model's own pick.
  let alternateRows = $derived(
    alternates.map((item: AlternatePrediction) => ({
      species: {
        scientificName: item.scientificName,
        commonName: localizeSpeciesName(item.scientificName, item.commonName),
      },
      detail: formatScore(item.confidence),
      original: item.original === true,
    }))
  );

  // The dialog is keyed by detection: stepping away destroys it while a save may still be
  // out, and the page's close handler would then close the next detection's dialog.
  let destroyed = false;
  onDestroy(() => {
    destroyed = true;
  });

  function failureMessage(error: unknown, chosen: CatalogSpecies): string {
    if (
      error instanceof ApiError &&
      error.errorKey === 'detections.detail.species.unknownSpecies'
    ) {
      return t('detections.detail.species.unknownSpecies', { species: chosen.commonName });
    }
    if (error instanceof ApiError && error.status === HTTP_CONFLICT) {
      return t('detections.detail.species.conflict');
    }
    return t('detections.detail.species.changeFailed');
  }

  async function confirm() {
    // The species sent is the one named afterwards, whatever is picked while it saves.
    const chosen = selected;
    if (!chosen || saving || !canChange) return;
    // What the save is made on, read once: the page may show another detection by the time it answers.
    const id = detection.id;
    const previousSpecies = detection.scientificName;
    const startedIn = opening;
    // The opening the save was started in is still the one on screen.
    const stillShown = () => !destroyed && isOpen && opening === startedIn;
    saving = true;
    errorMessage = '';
    try {
      await api.put(`/api/v2/detections/${id}/species`, {
        scientificName: chosen.scientificName,
      });
    } catch (error) {
      logger.error('Failed to change species', error, { action: 'changeSpecies' });
      const message = failureMessage(error, chosen);
      // A dialog gone, closed or opened again since has nowhere to show it.
      if (stillShown()) errorMessage = message;
      else toastActions.error(message);
      saving = false;
      return;
    }
    toastActions.success(t('detections.detail.species.changed', { species: chosen.commonName }));
    // The detection moved from one species to another, so both species' statistics
    // changed, whichever detection the page shows by now.
    const reload = onChanged({
      detectionId: id,
      scientificName: previousSpecies,
      changedTo: chosen.scientificName,
      speciesStats: true,
    });
    if (stillShown()) onClose();
    // The dialog is closed by now, so a reload that fails is reported as a toast.
    try {
      if ((await reload) === false) toastActions.error(t('detections.detail.review.reloadFailed'));
    } finally {
      saving = false;
    }
  }
</script>

<!-- The names and score of one row, shared by the plain rows and the choices. -->
{#snippet names(item: CatalogSpecies, detail: string, original: boolean)}
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
{/snippet}

<!-- A row a reviewer can pick: one radio of the dialog's single group. -->
{#snippet choice(item: CatalogSpecies, detail: string, original: boolean)}
  {@const active = selected?.scientificName === item.scientificName}
  <label class="dd-species-option" class:dd-species-option-active={active}>
    <input
      type="radio"
      class="sr-only"
      name={choiceName}
      value={item.scientificName}
      checked={active}
      onchange={() => (selected = item)}
      onfocus={revealRow}
    />
    <span class="dd-species-check" aria-hidden="true">
      {#if active}<Check class="size-3.5" />{/if}
    </span>
    {@render names(item, detail, original)}
  </label>
{/snippet}

{#snippet predictionsHeading()}
  {t('detections.detail.species.fromPredictions')}
  {#if predictionModel}
    <span class="dd-species-heading-detail">{predictionModel}</span>
  {/if}
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
    <div class="dd-species">
      <p class="dd-species-current">
        {t('detections.detail.species.current')}
        <strong>{speciesName}</strong>
        <span class="dd-species-scientific">{detection.scientificName}</span>
      </p>

      {#if !canChange}
        <h4 class="dd-species-heading">{@render predictionsHeading()}</h4>
        {#if alternateRows.length > 0}
          <ul class="dd-species-list dd-species-alternates">
            {#each alternateRows as row (row.species.scientificName)}
              <li class="dd-species-option dd-species-option-static">
                {@render names(row.species, row.detail, row.original)}
              </li>
            {/each}
          </ul>
        {:else}
          <p class="dd-species-note">{t('detections.detail.species.noPredictions')}</p>
        {/if}
        <p class="dd-species-effect">{blockedReason}</p>
      {:else}
        <fieldset class="dd-species-group">
          <legend class="dd-species-heading">{@render predictionsHeading()}</legend>
          {#if alternateRows.length > 0}
            <div class="dd-species-list dd-species-alternates">
              {#each alternateRows as row (row.species.scientificName)}
                {@render choice(row.species, row.detail, row.original)}
              {/each}
            </div>
          {:else}
            <p class="dd-species-note">{t('detections.detail.species.noPredictions')}</p>
          {/if}
        </fieldset>

        <search class="dd-species-search">
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
          <p class="sr-only" role="status">{announcement}</p>
        </search>
        <fieldset class="dd-species-group dd-species-results" style:--rows={MAX_RESULTS}>
          <legend class="sr-only">{t('detections.detail.species.search')}</legend>
          {#if catalogState === 'loading'}
            <p class="dd-species-note">{t('detections.detail.species.loadingList')}</p>
          {:else if catalogState === 'failed'}
            <p class="dd-species-note dd-species-error">
              {t('detections.detail.species.listFailed')}
            </p>
          {:else if !searching}
            <p class="dd-species-note">
              {allSpecies
                ? t('detections.detail.species.searchHintAll', { count: catalog.length })
                : t('detections.detail.species.searchHintRange', { count: catalog.length })}
            </p>
          {:else if results.length === 0}
            <p class="dd-species-note">{resultsSummary}</p>
          {:else}
            <div class="dd-species-list">
              {#each results as item (item.scientificName)}
                {@render choice(item, '', false)}
              {/each}
            </div>
          {/if}
        </fieldset>

        <p class="dd-species-effect">{t('detections.detail.species.effect')}</p>
        {#if errorMessage}
          <p class="dd-species-error" role="alert">{errorMessage}</p>
        {/if}
      {/if}
    </div>
  {/snippet}

  {#snippet footer()}
    {#if canChange}
      <!-- The hint and the picked species share this slot, and the button labels
           do not change, so picking a species moves neither button. The species is
           also shown in full in its row above, so cutting it here hides nothing. -->
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
  /* One species row is a single line of names in a bordered box. */
  .dd-species {
    --row-height: 2.375rem;
    --row-gap: 0.25rem;
  }

  .dd-species-current {
    font-size: 0.875rem;
  }

  .dd-species-current strong {
    margin: 0 0.25rem;
  }

  /* The legend is the group's heading; a fieldset ignores a legend's margins, so the
     space above it is the fieldset's and the space below is padding. */
  .dd-species-group {
    min-width: 0;
    margin: 1rem 0 0;
    padding: 0;
    border: 0;
  }

  .dd-species-heading {
    display: block;
    padding: 0 0 0.375rem;
    margin: 1rem 0 0;
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-subtle);
  }

  .dd-species-heading-detail {
    margin-left: 0.5rem;
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
    padding: 0;
    margin: 0;
  }

  .dd-species-list {
    display: flex;
    flex-direction: column;
    gap: var(--row-gap);
  }

  /* A long list of predictions scrolls inside its own box instead of pushing the
     search field down. */
  .dd-species-alternates {
    max-height: 11rem;
    overflow-y: auto;
  }

  .dd-species-option {
    display: flex;
    min-height: var(--row-height);
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

  /* The radio itself is hidden; its row shows the focus ring. */
  .dd-species-option:has(input:focus-visible) {
    outline: 2px solid var(--color-primary);
    outline-offset: 2px;
  }

  .dd-species-option-active {
    border-color: var(--color-primary);
    background-color: color-mix(in srgb, var(--color-primary) 10%, transparent);
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
    color: var(--text-subtle);
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
    color: var(--text-subtle);
  }

  /* Fixed height for a full page of results (--rows, the most results shown), so the
     dialog keeps its size and its controls keep their place while typing. Each row is
     --row-height high; rows are --row-gap apart. */
  .dd-species-results {
    height: calc(var(--rows) * var(--row-height) + (var(--rows) - 1) * var(--row-gap));
    margin-top: 0.5rem;
    overflow-y: auto;
  }

  .dd-species-note {
    font-size: 0.8125rem;
    color: var(--text-subtle);
  }

  .dd-species-effect {
    margin-top: 0.75rem;
    font-size: 0.8125rem;
    color: var(--text-subtle);
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
    color: var(--text-subtle);
  }

  .dd-species-choice-picked {
    font-weight: 600;
    color: var(--color-base-content);
  }
</style>
