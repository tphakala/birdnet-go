<!--
  Model Checkbox List Component

  Purpose: Reusable model selection with checkboxes and sample rate
  compatibility badges. Used in sound card and stream source configuration.

  @component
-->
<script lang="ts">
  import { AlertTriangle } from '@lucide/svelte';
  import { t } from '$lib/i18n';

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
    disabled?: boolean;
    onToggle: (_models: string[]) => void;
  }

  let {
    models,
    selectedModels,
    sourceSampleRate = 48000,
    disabled = false,
    onToggle,
  }: Props = $props();
</script>

<fieldset class="space-y-1.5">
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
    <label
      class="flex items-center gap-2.5 px-2.5 py-1.5 rounded-md cursor-pointer transition-colors hover:bg-[var(--color-base-content)]/5 {isChecked
        ? 'bg-[var(--color-primary)]/5'
        : ''}"
    >
      <input
        type="checkbox"
        checked={isChecked}
        disabled={disabled || (isChecked && selectedModels.length === 1)}
        onchange={() => {
          if (isChecked) {
            onToggle(selectedModels.filter(id => id !== model.id));
          } else {
            onToggle([...selectedModels, model.id]);
          }
        }}
        class="size-4 rounded border-[var(--border-200)] text-[var(--color-primary)] focus:ring-[var(--color-primary)]"
      />
      <span class="text-sm text-[var(--color-base-content)]">{model.name}</span>
      {#if belowMin}
        <span
          class="ml-auto inline-flex items-center gap-1 text-[10px] font-medium px-1.5 py-0.5 rounded bg-[var(--color-error)]/15 text-[var(--color-error)]"
        >
          <AlertTriangle class="size-3" />
          {t('settings.audio.soundCards.compatibility.minSampleRate', {
            rate: String((model.minSampleRate ?? 0) / 1000),
          })}
        </span>
      {:else if belowRecommended}
        <span
          class="ml-auto inline-flex items-center gap-1 text-[10px] font-medium px-1.5 py-0.5 rounded bg-[var(--color-warning)]/15 text-[var(--color-warning)]"
        >
          <AlertTriangle class="size-3" />
          {t('settings.audio.soundCards.compatibility.recommendedSampleRate', {
            rate: String((model.recommendedSampleRate ?? 0) / 1000),
          })}
        </span>
      {/if}
    </label>
  {/each}
</fieldset>
