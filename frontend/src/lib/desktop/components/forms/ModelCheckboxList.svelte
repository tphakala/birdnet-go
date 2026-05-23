<!--
  Model Checkbox List Component

  Purpose: Reusable model selection with checkboxes and sample rate
  compatibility badges. Uses the shared Checkbox component for visual
  consistency. Used in sound card and stream source configuration.

  @component
-->
<script lang="ts">
  import { AlertTriangle, Info } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import Checkbox from './Checkbox.svelte';

  interface ModelOption {
    id: string;
    name: string;
    category: string;
    minSampleRate?: number;
    recommendedSampleRate?: number;
  }

  interface Props {
    models: ModelOption[];
    selectedModels: string[];
    sourceSampleRate?: number;
    isStream?: boolean;
    disabled?: boolean;
    onToggle: (_models: string[]) => void;
  }

  let {
    models,
    selectedModels,
    sourceSampleRate = 48000,
    isStream = false,
    disabled = false,
    onToggle,
  }: Props = $props();

  let availableBirdnet = $derived(
    models.filter(m => m.id.startsWith('birdnet') && m.category === 'bird')
  );
  let availablePerch = $derived(
    models.filter(m => m.id.startsWith('perch') && m.category === 'bird')
  );
  let hasBothFamilies = $derived(availableBirdnet.length > 0 && availablePerch.length > 0);

  let selectedHasBirdnet = $derived(
    selectedModels.some(id => availableBirdnet.some(m => m.id === id))
  );
  let selectedHasPerch = $derived(selectedModels.some(id => availablePerch.some(m => m.id === id)));

  let showPerchOnlyWarning = $derived(hasBothFamilies && selectedHasPerch && !selectedHasBirdnet);
  let showRecommendBoth = $derived(hasBothFamilies && selectedHasBirdnet && !selectedHasPerch);

  let selectedHasBatModel = $derived(
    selectedModels.some(id => {
      const model = models.find(m => m.id === id);
      return model?.category === 'bat';
    })
  );
  let showBatStreamWarning = $derived(isStream && selectedHasBatModel);

  function handleToggle(modelId: string, checked: boolean) {
    if (checked) {
      onToggle([...selectedModels, modelId]);
    } else {
      onToggle(selectedModels.filter(id => id !== modelId));
    }
  }
</script>

<fieldset class="space-y-0.5">
  <legend class="text-xs font-medium text-[var(--color-base-content)] pb-1">
    {t('settings.audio.soundCards.modelLabel')}
  </legend>
  {#each models as model (model.id)}
    {@const isChecked = selectedModels.includes(model.id)}
    {@const belowMin =
      (model.minSampleRate ?? 0) > 0 && sourceSampleRate < (model.minSampleRate ?? 0)}
    {@const belowRecommended =
      !belowMin &&
      (model.recommendedSampleRate ?? 0) > 0 &&
      sourceSampleRate < (model.recommendedSampleRate ?? 0)}
    <Checkbox
      checked={isChecked}
      disabled={disabled || (isChecked && selectedModels.length === 1)}
      size="sm"
      onchange={checked => handleToggle(model.id, checked)}
    >
      {#snippet children()}
        <span class="flex items-center gap-2 min-w-0">
          <span class="text-sm text-[var(--color-base-content)]">{model.name}</span>
          {#if belowMin}
            <span
              class="inline-flex items-center gap-1 text-xs font-medium px-1.5 py-0.5 rounded bg-[var(--color-error)]/15 text-[var(--color-error)]"
            >
              <AlertTriangle class="size-3" />
              {t('settings.audio.soundCards.compatibility.minSampleRate', {
                rate: String((model.minSampleRate ?? 0) / 1000),
              })}
            </span>
          {:else if belowRecommended}
            <span
              class="inline-flex items-center gap-1 text-xs font-medium px-1.5 py-0.5 rounded bg-[var(--color-warning)]/15 text-[var(--color-warning)]"
            >
              <AlertTriangle class="size-3" />
              {t('settings.audio.soundCards.compatibility.recommendedSampleRate', {
                rate: String((model.recommendedSampleRate ?? 0) / 1000),
              })}
            </span>
          {/if}
        </span>
      {/snippet}
    </Checkbox>
  {/each}

  {#if showPerchOnlyWarning}
    <div
      class="flex items-start gap-2 mt-2 p-2.5 rounded-lg text-xs leading-relaxed bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]"
      role="status"
    >
      <AlertTriangle class="size-3.5 shrink-0 mt-0.5 text-[var(--color-warning)]" />
      <span class="text-[var(--color-base-content)]">
        {t('settings.audio.models.perchOnlyWarning')}
      </span>
    </div>
  {:else if showRecommendBoth}
    <div
      class="flex items-start gap-2 mt-2 p-2.5 rounded-lg text-xs leading-relaxed bg-[color-mix(in_srgb,var(--color-info)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-info)_30%,transparent)]"
      role="status"
    >
      <Info class="size-3.5 shrink-0 mt-0.5 text-[var(--color-info)]" />
      <span class="text-[var(--color-base-content)]">
        {t('settings.audio.models.recommendBoth')}
      </span>
    </div>
  {/if}

  {#if showBatStreamWarning}
    <div
      class="flex items-start gap-2 mt-2 p-2.5 rounded-lg text-xs leading-relaxed bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]"
      role="alert"
    >
      <AlertTriangle class="size-3.5 shrink-0 mt-0.5 text-[var(--color-warning)]" />
      <span class="text-[var(--color-base-content)]">
        {t('settings.audio.streams.batWarning')}
      </span>
    </div>
  {/if}
</fieldset>
