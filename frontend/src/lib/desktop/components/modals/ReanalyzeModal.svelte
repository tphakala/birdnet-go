<script lang="ts">
  /**
   * ReanalyzeModal — "second opinion" inference on a saved detection clip.
   *
   * Sends the clip back through a user-chosen loaded classifier model and
   * shows the top-N predictions transiently. Nothing is persisted server-side;
   * this is a read-only sanity-check feature.
   *
   * The picker lists loaded models from /api/v2/models (the running orchestrator's
   * enabled set). It excludes the model that originally produced the detection
   * — re-running the same model against its own output is rarely interesting
   * and would waste compute.
   */
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import { reanalyzeDetection, type ReanalyzeResult } from '$lib/utils/reanalyzeDetection';
  import { api } from '$lib/utils/api';
  import { t } from '$lib/i18n';
  import { Sparkles, AlertCircle } from '@lucide/svelte';

  interface Props {
    isOpen: boolean;
    detectionId: number | null;
    /**
     * The model the original detection was produced by (catalog ID such as
     * "birdnet" or "perch_v2"). Excluded from the picker. Optional — when
     * omitted, all loaded models are listed.
     */
    originalModelId?: string;
    onClose: () => void;
  }

  interface ModelOption {
    id: string;
    name: string;
  }

  let { isOpen = false, detectionId = null, originalModelId, onClose }: Props = $props();

  let modelOptions = $state<ModelOption[]>([]);
  let selectedModelId = $state<string>('');
  let loadingModels = $state(false);
  let modelsLoadError = $state<string | null>(null);

  let isRunning = $state(false);
  let result = $state<ReanalyzeResult | null>(null);
  let errorMessage = $state<string | null>(null);

  // Refetch the loaded-models list every time the modal opens, so the picker
  // reflects any models the user enabled in Settings while the page stayed open.
  $effect(() => {
    if (!isOpen) return;
    // Reset prior state for a fresh run.
    result = null;
    errorMessage = null;
    selectedModelId = '';
    loadModelOptions();
  });

  async function loadModelOptions() {
    loadingModels = true;
    modelsLoadError = null;
    try {
      const list = await api.get<ModelOption[]>('/api/v2/models');
      modelOptions = (list ?? []).filter(m => {
        if (!originalModelId) return true;
        return m.id.toLowerCase() !== originalModelId.toLowerCase();
      });
      if (modelOptions.length > 0) {
        selectedModelId = modelOptions[0].id;
      }
    } catch (err) {
      modelsLoadError = err instanceof Error ? err.message : String(err);
    } finally {
      loadingModels = false;
    }
  }

  async function runReanalysis() {
    if (!detectionId || !selectedModelId || isRunning) return;
    isRunning = true;
    errorMessage = null;
    result = null;
    try {
      const res = await reanalyzeDetection(detectionId, selectedModelId);
      if (res === null) {
        // A duplicate request was already in flight; surface a hint but don't
        // treat it as a hard failure.
        errorMessage = t('detections.reanalyze.duplicateInFlight');
        return;
      }
      result = res;
    } catch (err) {
      errorMessage = err instanceof Error ? err.message : String(err);
    } finally {
      isRunning = false;
    }
  }

  function formatConfidencePercent(c: number): string {
    return `${(c * 100).toFixed(1)}%`;
  }

  function formatDuration(sec: number): string {
    if (sec >= 60) {
      const m = Math.floor(sec / 60);
      const s = (sec % 60).toFixed(1);
      return `${m}m ${s}s`;
    }
    return `${sec.toFixed(1)}s`;
  }
</script>

<Modal {isOpen} title={t('detections.reanalyze.title')} {onClose}>
  <div class="space-y-4">
    <p class="text-sm text-base-content/70">
      {t('detections.reanalyze.description')}
    </p>

    <!-- Model picker -->
    <div class="form-control">
      <label class="label py-1" for="reanalyze-model-select">
        <span class="label-text font-medium">{t('detections.reanalyze.modelLabel')}</span>
      </label>
      {#if loadingModels}
        <div class="text-sm text-base-content/60">{t('detections.reanalyze.loadingModels')}</div>
      {:else if modelsLoadError}
        <div role="alert" class="alert alert-error text-sm">
          <AlertCircle class="h-4 w-4" />
          <span>{modelsLoadError}</span>
        </div>
      {:else if modelOptions.length === 0}
        <div class="text-sm text-base-content/70">
          {t('detections.reanalyze.noOtherModels')}
        </div>
      {:else}
        <select
          id="reanalyze-model-select"
          class="select select-bordered w-full"
          bind:value={selectedModelId}
          disabled={isRunning}
        >
          {#each modelOptions as option (option.id)}
            <option value={option.id}>{option.name}</option>
          {/each}
        </select>
      {/if}
    </div>

    <!-- Run button -->
    <button
      type="button"
      class="btn btn-primary w-full"
      onclick={runReanalysis}
      disabled={isRunning || !selectedModelId || modelOptions.length === 0}
    >
      <Sparkles class="h-4 w-4" />
      {#if isRunning}
        {t('detections.reanalyze.running')}
      {:else}
        {t('detections.reanalyze.run')}
      {/if}
    </button>

    <!-- Error -->
    {#if errorMessage}
      <div role="alert" class="alert alert-error text-sm">
        <AlertCircle class="h-4 w-4" />
        <span>{errorMessage}</span>
      </div>
    {/if}

    <!-- Results -->
    {#if result}
      <div class="space-y-2">
        <div class="text-xs text-base-content/60">
          {t('detections.reanalyze.summary', {
            model: result.modelName,
            duration: formatDuration(result.clipDurationSec),
            windows: result.windowCount,
          })}
        </div>

        {#if result.predictions.length === 0}
          <div class="text-sm italic text-base-content/70">
            {t('detections.reanalyze.noPredictions')}
          </div>
        {:else}
          <table class="table table-sm w-full">
            <thead>
              <tr>
                <th class="text-left">{t('detections.reanalyze.colSpecies')}</th>
                <th class="text-right">{t('detections.reanalyze.colConfidence')}</th>
              </tr>
            </thead>
            <tbody>
              {#each result.predictions as pred, idx (idx)}
                <tr>
                  <td class="font-mono text-sm">{pred.species}</td>
                  <td class="text-right tabular-nums">
                    {formatConfidencePercent(pred.confidence)}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </div>
    {/if}
  </div>
</Modal>
