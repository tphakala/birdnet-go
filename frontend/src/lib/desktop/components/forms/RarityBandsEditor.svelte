<!--
  RarityBandsEditor.svelte

  Editable list of rarity filter bands. Each band pairs an occurrence upper bound
  (0-1) with the number of confirming detections a species below that bound needs.
  The tightest band a species falls under applies, so row order does not matter;
  the backend sorts the bands on save.

  Props:
  - id: Base id for the editor's elements
  - bands: Current bands (controlled)
  - onUpdate: Called with the complete new band list after any change
  - disabled: Disables every control
-->
<script lang="ts">
  import { Plus, Trash2 } from '@lucide/svelte';
  import NumberField from './NumberField.svelte';
  import { t } from '$lib/i18n';
  import { generateId } from '$lib/utils/uuid';
  import type { RarityBand } from '$lib/stores/settings';

  interface Props {
    id: string;
    bands: RarityBand[];
    onUpdate: (_bands: RarityBand[]) => void;
    disabled?: boolean;
  }

  let { id, bands, onUpdate, disabled = false }: Props = $props();

  // MAX_OCCURRENCE, MIN_DETECTIONS, MAX_DETECTIONS and MAX_BANDS mirror the backend
  // validation (internal/conf/validate_realtime.go). MIN_OCCURRENCE is stricter than
  // the backend's "greater than 0", matching the input's step; see occurrenceMin for
  // how a saved value below it is kept.
  const MIN_OCCURRENCE = 0.01;
  const MAX_OCCURRENCE = 1;
  const OCCURRENCE_STEP = 0.01;
  const MIN_DETECTIONS = 1;
  const MAX_DETECTIONS = 10;
  const DETECTIONS_STEP = 1;
  const MAX_BANDS = 10;
  const NEW_BAND: RarityBand = { maxOccurrence: 0.5, minDetections: 2 };

  const hintId = $derived(`${id}-hint`);
  const addHelpId = $derived(`${id}-add-help`);
  const removeHelpId = $derived(`${id}-remove-help`);

  // Bands carry no id of their own, so keep a parallel list of stable row keys for
  // {#each}. It is plain (non-reactive) bookkeeping, padded or truncated to the band
  // count and spliced alongside add/remove so keys follow their rows.
  const rowIds: string[] = [];
  const rows = $derived.by(() => {
    while (rowIds.length < bands.length) rowIds.push(generateId('rarity-band'));
    rowIds.length = bands.length;
    // rowIds was just padded to bands.length, so the fallback is never used.
    return bands.map((band, index) => ({
      key: rowIds.at(index) ?? `${id}-band-${index}`,
      band,
      index,
    }));
  });

  const canAdd = $derived(!disabled && bands.length < MAX_BANDS);
  const atMaxBands = $derived(bands.length >= MAX_BANDS);
  // The enabled filter needs at least one band (the backend rejects an empty list), so
  // the last band cannot be removed; disabling the filter is the way to drop it.
  const isLastBand = $derived(bands.length === 1);

  // The input's lower bound for a band. A saved value the backend accepts (above 0)
  // but below MIN_OCCURRENCE lowers the bound to itself, so focusing and leaving the
  // field without an edit does not clamp it up to MIN_OCCURRENCE.
  function occurrenceMin(maxOccurrence: number): number {
    return maxOccurrence > 0 && maxOccurrence < MIN_OCCURRENCE ? maxOccurrence : MIN_OCCURRENCE;
  }

  function updateBand(index: number, patch: Partial<RarityBand>) {
    onUpdate(bands.map((band, i) => (i === index ? { ...band, ...patch } : band)));
  }

  function removeBand(event: MouseEvent, index: number) {
    if (isLastBand) {
      event.preventDefault();
      return;
    }
    rowIds.splice(index, 1);
    onUpdate(bands.filter((_, i) => i !== index));
  }

  function addBand(event: MouseEvent) {
    if (!canAdd) {
      event.preventDefault();
      return;
    }
    onUpdate([...bands, { ...NEW_BAND }]);
  }
</script>

<div {id} class="space-y-3" role="group" aria-describedby={hintId}>
  <p id={hintId} class="text-sm text-[var(--color-base-content)]/70">
    {t('components.forms.rarityBands.orderHint')}
  </p>

  {#if bands.length === 0}
    <p class="text-sm text-[var(--color-warning)]" role="status">
      {t('components.forms.rarityBands.emptyState')}
    </p>
  {/if}

  {#each rows as row (row.key)}
    <fieldset
      class="grid grid-cols-1 md:grid-cols-[1fr_1fr_auto] gap-4 items-start rounded-lg border border-[var(--color-base-300)] p-3"
    >
      <legend class="px-1 text-sm font-medium">
        {t('components.forms.rarityBands.bandLabel', { number: row.index + 1 })}
      </legend>
      <NumberField
        label={t('components.forms.rarityBands.maxOccurrence.label')}
        helpText={t('components.forms.rarityBands.maxOccurrence.helpText')}
        inputId={`${row.key}-max-occurrence`}
        value={row.band.maxOccurrence}
        min={occurrenceMin(row.band.maxOccurrence)}
        max={MAX_OCCURRENCE}
        step={OCCURRENCE_STEP}
        {disabled}
        onUpdate={value => updateBand(row.index, { maxOccurrence: value })}
      />
      <NumberField
        label={t('components.forms.rarityBands.minDetections.label')}
        helpText={t('components.forms.rarityBands.minDetections.helpText')}
        inputId={`${row.key}-min-detections`}
        value={row.band.minDetections}
        min={MIN_DETECTIONS}
        max={MAX_DETECTIONS}
        step={DETECTIONS_STEP}
        {disabled}
        onUpdate={value => updateBand(row.index, { minDetections: Math.round(value) })}
      />
      <button
        type="button"
        class="btn btn-ghost btn-sm btn-square md:mt-9 text-[var(--color-error)] aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
        aria-label={t('components.forms.rarityBands.removeBand', { number: row.index + 1 })}
        aria-disabled={isLastBand ? 'true' : undefined}
        aria-describedby={isLastBand ? removeHelpId : undefined}
        {disabled}
        onclick={event => removeBand(event, row.index)}
      >
        <Trash2 class="size-4" aria-hidden="true" />
      </button>
    </fieldset>
  {/each}

  {#if isLastBand}
    <p id={removeHelpId} class="text-sm text-[var(--color-base-content)]/70">
      {t('components.forms.rarityBands.lastBandRequired')}
    </p>
  {/if}

  <div>
    <button
      type="button"
      class="btn btn-sm btn-outline aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
      aria-disabled={!canAdd ? 'true' : undefined}
      aria-describedby={atMaxBands ? addHelpId : undefined}
      onclick={addBand}
    >
      <Plus class="size-4" aria-hidden="true" />
      {t('components.forms.rarityBands.addBand')}
    </button>
    {#if atMaxBands}
      <p id={addHelpId} class="mt-1 text-sm text-[var(--color-base-content)]/70">
        {t('components.forms.rarityBands.maxBandsReached', { max: MAX_BANDS })}
      </p>
    {/if}
  </div>
</div>
