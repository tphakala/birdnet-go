<!--
  Model Checkbox List Component

  Purpose: Reusable model selection with checkboxes and sample rate
  compatibility badges. Uses the shared Checkbox component for visual
  consistency. Used in sound card and stream source configuration.

  Below the list it explains the empty and degraded states: the list still
  loading, no detection model enabled (N=0, the source can still be saved and
  runs the defaults once a model is enabled) and a model that failed to load.

  @component
-->
<script lang="ts">
  import { AlertTriangle, Info, Loader2 } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { modelLabel } from '$lib/utils/modelLabel';
  import { SETTINGS_ROUTES, SYSTEM_ROUTES } from '$lib/utils/settingsRoutes';
  import { handleAppLinkClick } from '$lib/stores/navigation.svelte';
  import type { AcousticModelAvailability } from '$lib/types/models';
  import type { ModelLoadState } from '$lib/stores/models.svelte';
  import Checkbox from './Checkbox.svelte';

  interface ModelOption {
    id: string;
    /** Classifier registry ID; the join key for default-target mapping. Absent on an older server. */
    registryId?: string;
    name: string;
    category: string;
    minSampleRate?: number;
    recommendedSampleRate?: number;
    /** Live execution backend of the loaded model; absent when not loaded. */
    backend?: string;
    /** Load state; absent counts as loaded. */
    state?: ModelLoadState;
  }

  interface Props {
    models: ModelOption[];
    selectedModels: string[];
    sourceSampleRate?: number;
    isStream?: boolean;
    disabled?: boolean;
    /** True while the model list is being fetched; renders a labelled loading line when the list is empty. */
    loading?: boolean;
    /** Classifier verdict driving the empty and degraded explanations; unknown adds nothing. */
    availability?: AcousticModelAvailability;
    onToggle: (_models: string[]) => void;
  }

  let {
    models,
    selectedModels,
    sourceSampleRate = 48000,
    isStream = false,
    disabled = false,
    loading = false,
    availability = { kind: 'unknown' },
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

  // Empty / degraded explanations, in priority order. Only one renders.
  let showLoading = $derived(loading && models.length === 0);
  let noModelReason = $derived(availability.kind === 'none' ? availability.reason : null);
  let showNoneEnabled = $derived(!showLoading && noModelReason === 'none_installed');
  let showLoadFailed = $derived(!showLoading && noModelReason === 'load_failed');
  // Fetched empty without a classifier verdict (older server, guest, or the
  // status fetch failed): still say why the list is empty.
  let showNoneAvailable = $derived(noModelReason === null && !loading && models.length === 0);

  // Selected models that can run; the last of them stays selected.
  let selectedLoadedCount = $derived(
    models.filter(
      model =>
        selectedModels.includes(model.id) && model.state !== 'failed' && model.state !== 'pending'
    ).length
  );

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
    {@const notLoaded = model.state === 'failed' || model.state === 'pending'}
    {@const blockedNotLoaded = notLoaded && !isChecked}
    <!-- The last-selection guard keeps one working model; a model that is not
         loaded can always be removed, even when it is the only one. -->
    <Checkbox
      checked={isChecked}
      disabled={disabled ||
        blockedNotLoaded ||
        (isChecked && !notLoaded && selectedLoadedCount === 1)}
      helpText={blockedNotLoaded ? t('settings.audio.models.notLoadedHelp') : undefined}
      size="sm"
      onchange={checked => handleToggle(model.id, checked)}
    >
      {#snippet children()}
        <span class="flex items-center gap-2 min-w-0">
          <span class="text-sm text-[var(--color-base-content)]">{modelLabel(model)}</span>
          {#if model.state === 'pending'}
            <span
              class="inline-flex items-center gap-1 text-xs font-medium px-1.5 py-0.5 rounded bg-[var(--color-info)]/15 text-[var(--text-info)]"
            >
              <Loader2 class="size-3 animate-spin motion-reduce:animate-none" aria-hidden="true" />
              {t('settings.audio.models.state.pending')}
            </span>
          {:else if model.state === 'failed'}
            <span
              class="inline-flex items-center gap-1 text-xs font-medium px-1.5 py-0.5 rounded bg-[var(--color-warning)]/15 text-[var(--text-warning)]"
            >
              <AlertTriangle class="size-3" aria-hidden="true" />
              {t('settings.audio.models.state.failed')}
            </span>
          {/if}
          {#if belowMin}
            <span
              class="inline-flex items-center gap-1 text-xs font-medium px-1.5 py-0.5 rounded bg-[var(--color-error)]/15 text-[var(--text-error)]"
            >
              <AlertTriangle class="size-3" />
              {t('settings.audio.soundCards.compatibility.minSampleRate', {
                rate: String((model.minSampleRate ?? 0) / 1000),
              })}
            </span>
          {:else if belowRecommended}
            <span
              class="inline-flex items-center gap-1 text-xs font-medium px-1.5 py-0.5 rounded bg-[var(--color-warning)]/15 text-[var(--text-warning)]"
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

  {#if showLoading}
    <p
      class="flex items-center gap-2 py-1 text-xs text-[var(--color-base-content)]/70"
      role="status"
    >
      <Loader2 class="size-3.5 shrink-0 animate-spin" aria-hidden="true" />
      {t('settings.audio.models.loading')}
    </p>
  {:else if showNoneEnabled}
    <div
      class="flex items-start gap-2 mt-2 p-2.5 rounded-lg text-xs leading-relaxed bg-[color-mix(in_srgb,var(--color-info)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-info)_30%,transparent)]"
      role="status"
    >
      <Info class="size-3.5 shrink-0 mt-0.5 text-[var(--text-info)]" aria-hidden="true" />
      <span class="text-[var(--color-base-content)]">
        <span class="font-medium">{t('settings.audio.models.noneEnabledTitle')}</span>
        {t('settings.audio.models.noneEnabledHelp')}
        <a
          href={buildAppUrl(SETTINGS_ROUTES.analysisModels)}
          onclick={handleAppLinkClick}
          class="underline text-[var(--color-primary)]"
        >
          {t('settings.audio.models.noneEnabledLink')}
        </a>
      </span>
    </div>
  {:else if showLoadFailed}
    <div
      class="flex items-start gap-2 mt-2 p-2.5 rounded-lg text-xs leading-relaxed bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]"
      role="status"
    >
      <AlertTriangle
        class="size-3.5 shrink-0 mt-0.5 text-[var(--text-warning)]"
        aria-hidden="true"
      />
      <span class="text-[var(--color-base-content)]">
        {t('settings.audio.models.loadFailedWarning')}
        <a
          href={buildAppUrl(SYSTEM_ROUTES.inference)}
          onclick={handleAppLinkClick}
          class="underline text-[var(--color-primary)]"
        >
          {t('settings.audio.models.loadFailedLink')}
        </a>
      </span>
    </div>
  {:else if showNoneAvailable}
    <div
      class="flex items-start gap-2 mt-2 p-2.5 rounded-lg text-xs leading-relaxed bg-[color-mix(in_srgb,var(--color-info)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-info)_30%,transparent)]"
      role="status"
    >
      <Info class="size-3.5 shrink-0 mt-0.5 text-[var(--text-info)]" aria-hidden="true" />
      <span class="text-[var(--color-base-content)]">
        {t('settings.audio.models.noneAvailable')}
        <a
          href={buildAppUrl(SETTINGS_ROUTES.analysisModels)}
          onclick={handleAppLinkClick}
          class="underline text-[var(--color-primary)]"
        >
          {t('settings.audio.models.noneEnabledLink')}
        </a>
      </span>
    </div>
  {/if}

  {#if showPerchOnlyWarning}
    <div
      class="flex items-start gap-2 mt-2 p-2.5 rounded-lg text-xs leading-relaxed bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]"
      role="status"
    >
      <AlertTriangle class="size-3.5 shrink-0 mt-0.5 text-[var(--text-warning)]" />
      <span class="text-[var(--color-base-content)]">
        {t('settings.audio.models.perchOnlyWarning')}
      </span>
    </div>
  {:else if showRecommendBoth}
    <div
      class="flex items-start gap-2 mt-2 p-2.5 rounded-lg text-xs leading-relaxed bg-[color-mix(in_srgb,var(--color-info)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-info)_30%,transparent)]"
      role="status"
    >
      <Info class="size-3.5 shrink-0 mt-0.5 text-[var(--text-info)]" />
      <span class="text-[var(--color-base-content)]">
        {t('settings.audio.models.recommendBoth')}
      </span>
    </div>
  {/if}

  {#if showBatStreamWarning}
    <div
      class="flex items-start gap-2 mt-2 p-2.5 rounded-lg text-xs leading-relaxed bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]"
      role="status"
    >
      <AlertTriangle class="size-3.5 shrink-0 mt-0.5 text-[var(--text-warning)]" />
      <span class="text-[var(--color-base-content)]">
        {t('settings.audio.streams.batWarning')}
      </span>
    </div>
  {/if}
</fieldset>
