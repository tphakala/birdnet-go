<!--
  Source Model Badges Component

  Purpose: The classifier badges in the header of an audio source card (a sound
  card, a stream): a warning badge when a selected model is missing or not
  loaded, and a summary of the models the source runs. An empty selection means
  the source follows the server defaults; the summary names them when the
  classifier reports them, says no model is loaded when that is the verdict,
  and hedges otherwise.

  Props:
  - selectedModels: The source's saved aliases; empty means the default models
  - defaultModels: Aliases the source runs while it follows the defaults
  - availableModels: Models the server lists for the source editors
  - modelOptions: Labels of the listed models
  - availability: Classifier verdict
  - listLoading: True while the model list loads (no warning then)
  - listFailed: True when the model list could not be loaded (no warning then)

  @component
-->
<script lang="ts">
  import { AlertTriangle } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import type { AcousticModelAvailability } from '$lib/types/models';
  import type { BackendModel } from '$lib/stores/models.svelte';

  interface Props {
    selectedModels: string[];
    defaultModels: string[];
    availableModels: BackendModel[];
    modelOptions: Array<{ value: string; label: string }>;
    availability: AcousticModelAvailability;
    listLoading: boolean;
    listFailed: boolean;
  }

  let {
    selectedModels,
    defaultModels,
    availableModels,
    modelOptions,
    availability,
    listLoading,
    listFailed,
  }: Props = $props();

  const BADGE_CLASS = 'px-2 py-0.5 rounded text-xs font-semibold';
  const WARNING_CLASS = 'bg-[var(--color-warning)]/15 text-[var(--text-warning)]';

  let hasExplicitModels = $derived(selectedModels.length > 0);

  function modelLabels(ids: readonly string[]): string {
    return ids.map(id => modelOptions.find(m => m.value === id)?.label ?? id).join(', ');
  }

  let summary = $derived.by(() => {
    if (hasExplicitModels) return modelLabels(selectedModels);
    if (availability.kind === 'none') return t('settings.audio.models.noneBadge');
    if (availability.kind === 'ready' && defaultModels.length > 0) {
      return t('settings.audio.models.defaultBadge', { models: modelLabels(defaultModels) });
    }
    return t('settings.audio.models.defaultPendingBadge');
  });

  let summaryIsWarning = $derived(!hasExplicitModels && availability.kind === 'none');

  let summaryTitle = $derived.by(() => {
    if (hasExplicitModels || availability.kind !== 'none') return undefined;
    return availability.reason === 'load_failed'
      ? t('settings.audio.models.loadFailedWarning')
      : t('settings.audio.models.noneEnabledHelp');
  });

  // A selected model that the server no longer lists, or that is not loaded.
  // Unknown while the list loads or after a failed fetch, so no warning then.
  let needsAttention = $derived(
    !listFailed &&
      !listLoading &&
      selectedModels.some(id => {
        const model = availableModels.find(m => m.id === id);
        return !model || model.state === 'failed' || model.state === 'pending';
      })
  );
</script>

{#if needsAttention}
  <span class={cn(BADGE_CLASS, 'inline-flex items-center gap-1', WARNING_CLASS)}>
    <AlertTriangle class="size-3" aria-hidden="true" />
    {t('settings.audio.models.warningBadge')}
  </span>
{/if}
<span
  class={cn(
    BADGE_CLASS,
    'max-w-64 truncate',
    summaryIsWarning ? WARNING_CLASS : 'bg-[var(--color-info)]/15 text-[var(--text-info)]'
  )}
  title={summaryTitle ?? summary}
>
  {summary}
</span>
