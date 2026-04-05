<!--
  SpeciesConfigEditor - Inline editor for species configuration rules.

  Composes Editor* primitives into a form matching AlertRuleEditor design.
  Form state is internal, initialized from config prop, emitted on save.

  @component
-->
<script lang="ts">
  import { ChevronRight, Check } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { SpeciesConfig, Action } from '$lib/stores/settings';
  import EditorCard from './editor/EditorCard.svelte';
  import EditorTextField from './editor/EditorTextField.svelte';
  import EditorSlider from './editor/EditorSlider.svelte';
  import EditorFooter from './editor/EditorFooter.svelte';
  import EditorSpeciesInput from './editor/EditorSpeciesInput.svelte';

  interface SavePayload {
    species: string;
    threshold: number;
    interval: number;
    actions: Action[];
  }

  interface Props {
    species: string | null;
    config: SpeciesConfig | null;
    predictions: string[];
    disabled?: boolean;
    saving?: boolean;
    onSave: (_payload: SavePayload) => void;
    onClose: () => void;
    onDelete?: (_species: string) => void;
    onInput: (_value: string) => void;
    onPredictionSelect: (_species: string) => void;
  }

  let {
    species,
    config,
    predictions,
    disabled = false,
    saving = false,
    onSave,
    onClose,
    onDelete,
    onInput,
    onPredictionSelect,
  }: Props = $props();

  // Form state initialized from props — parent uses {#key} to reset on species change
  const existingAction = config?.actions?.[0];
  let speciesName = $state(species ?? '');
  let threshold = $state(config?.threshold ?? 0.5);
  let interval = $state(config?.interval ?? 0);
  let showActions = $state(!!existingAction);
  let actionCommand = $state(existingAction?.command ?? '');
  let actionParameters = $state(
    Array.isArray(existingAction?.parameters) ? existingAction.parameters.join(',') : ''
  );
  let actionExecuteDefaults = $state(existingAction?.executeDefaults !== false);

  // Validation
  let isValid = $derived(speciesName.trim() !== '' && threshold >= 0 && threshold <= 1);

  let title = $derived(
    species
      ? t('settings.species.customConfiguration.editing', { species })
      : t('settings.species.customConfiguration.newConfiguration')
  );

  function handleSave() {
    if (!isValid) return;

    const actions: Action[] = [];
    if (actionCommand.trim()) {
      actions.push({
        type: 'ExecuteCommand' as const,
        command: actionCommand.trim(),
        parameters: actionParameters
          .split(',')
          .map(p => p.trim())
          .filter(p => p),
        executeDefaults: actionExecuteDefaults,
      });
    }

    onSave({
      species: speciesName.trim(),
      threshold,
      interval: Number(interval) || 0,
      actions,
    });
  }

  function handleDelete() {
    if (species) onDelete?.(species);
  }

  function addParameter(param: string) {
    actionParameters = actionParameters ? actionParameters + ',' + param : param;
  }

  function clearParameters() {
    actionParameters = '';
  }

  function handleSpeciesPicked(picked: string) {
    speciesName = picked;
    onPredictionSelect(picked);
  }

  function handleSpeciesInput(value: string) {
    speciesName = value;
    onInput(value);
  }
</script>

<EditorCard {title} {onClose}>
  <!-- Row 1: Species input (full width) -->
  <EditorSpeciesInput
    bind:value={speciesName}
    label={t('settings.species.customConfiguration.columnHeaders.species')}
    placeholder={t('settings.species.customConfiguration.searchPlaceholder')}
    {predictions}
    disabled={disabled || saving}
    onInput={handleSpeciesInput}
    onPredictionSelect={handleSpeciesPicked}
  />

  <!-- Row 2: Threshold + Interval -->
  <div class="grid grid-cols-2 gap-3">
    <EditorSlider
      label={t('settings.species.customConfiguration.labels.threshold')}
      bind:value={threshold}
      onUpdate={v => (threshold = v)}
      min={0}
      max={1}
      step={0.01}
      disabled={disabled || saving}
    />
    <EditorTextField
      label={t('settings.species.customConfiguration.labels.intervalSeconds')}
      bind:value={interval}
      onUpdate={v => (interval = Number(v) || 0)}
      type="number"
      min={0}
      max={3600}
      step={1}
      placeholder="0"
      helpText={t('settings.species.customConfiguration.helpText.interval')}
      disabled={disabled || saving}
    />
  </div>

  <!-- Row 3: Actions (collapsible) -->
  <div class="border-t border-[var(--color-base-300)] pt-3">
    <button
      type="button"
      class="flex items-center gap-2 text-xs font-medium text-[var(--color-base-content)]/60 hover:text-[var(--color-primary)] transition-colors cursor-pointer"
      onclick={() => (showActions = !showActions)}
      aria-expanded={showActions}
    >
      <span class="transition-transform duration-200" class:rotate-90={showActions}>
        <ChevronRight class="size-4" />
      </span>
      <span>{t('settings.species.customConfiguration.configureActions')}</span>
      {#if actionCommand}
        <span
          class="inline-flex items-center px-1.5 py-0.5 text-[10px] font-medium rounded-full bg-teal-500/15 text-teal-600 dark:text-teal-400"
        >
          {t('settings.species.customConfiguration.actionsConfigured')}
        </span>
      {/if}
    </button>
  </div>

  {#if showActions}
    <div class="space-y-3 pl-1 border-l-2 border-[var(--color-primary)]/20 ml-1">
      <!-- Command -->
      <EditorTextField
        label={t('settings.species.actionsModal.command.label')}
        bind:value={actionCommand}
        onUpdate={v => (actionCommand = String(v))}
        placeholder={t('settings.species.commandPathPlaceholder')}
        helpText={t('settings.species.actionsModal.command.helpText')}
        disabled={disabled || saving}
      />

      <!-- Parameters -->
      <EditorTextField
        label={t('settings.species.actionsModal.parameters.label')}
        bind:value={actionParameters}
        onUpdate={v => (actionParameters = String(v))}
        placeholder={t('settings.species.actionsModal.parameters.placeholder')}
        helpText={t('settings.species.actionsModal.parameters.helpText')}
        disabled={disabled || saving}
      />

      <!-- Parameter quick-add buttons -->
      <div>
        <div class="text-xs font-medium text-[var(--color-base-content)]/60 mb-1">
          {t('settings.species.actionsModal.parameters.availableTitle')}
        </div>
        <div class="flex flex-wrap gap-1">
          {#each ['CommonName', 'ScientificName', 'Confidence', 'Time', 'Source'] as param (param)}
            <button
              type="button"
              class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md border border-[var(--color-base-300)] bg-transparent hover:bg-[var(--color-base-200)] transition-colors cursor-pointer"
              onclick={() => addParameter(param)}
            >
              {t(
                `settings.species.actionsModal.parameters.buttons.${param.charAt(0).toLowerCase() + param.slice(1)}`
              )}
            </button>
          {/each}
          <button
            type="button"
            class="inline-flex items-center justify-center h-6 px-2 text-xs font-medium rounded-md bg-[var(--color-warning)] text-[var(--color-warning-content)] hover:opacity-90 transition-colors cursor-pointer"
            onclick={clearParameters}
          >
            {t('settings.species.actionsModal.parameters.buttons.clearParameters')}
          </button>
        </div>
      </div>

      <!-- Execute defaults checkbox -->
      <label class="flex items-center gap-2 cursor-pointer">
        <span class="relative inline-flex items-center justify-center w-4 h-4">
          <input
            type="checkbox"
            bind:checked={actionExecuteDefaults}
            disabled={disabled || saving}
            class="peer appearance-none w-4 h-4 border-2 border-[var(--color-base-300)] rounded bg-[var(--color-base-100)] cursor-pointer transition-all checked:bg-[var(--color-primary)] checked:border-[var(--color-primary)]"
          />
          <Check
            class="absolute w-2.5 h-2.5 text-[var(--color-primary-content)] pointer-events-none opacity-0 peer-checked:opacity-100 transition-opacity"
          />
        </span>
        <span class="text-xs text-[var(--color-base-content)]/60">
          {t('settings.species.actionsModal.executeDefaults.label')}
        </span>
      </label>
      <span class="text-xs text-[var(--color-base-content)]/40 block">
        {t('settings.species.actionsModal.executeDefaults.helpText')}
      </span>
    </div>
  {/if}

  {#snippet footer()}
    <EditorFooter
      onSave={handleSave}
      onCancel={onClose}
      onDelete={species ? handleDelete : undefined}
      saveLabel={species
        ? t('settings.species.customConfiguration.save')
        : t('settings.species.customConfiguration.labels.addButton')}
      saveDisabled={!isValid || disabled}
      {saving}
    />
  {/snippet}
</EditorCard>
