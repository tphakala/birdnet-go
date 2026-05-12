<script lang="ts">
  /**
   * ReanalyzeModal — second-opinion inference + correction loop on a saved
   * detection clip.
   *
   * On open: runs every currently-loaded compatible classifier in parallel
   * against the clip and renders a species × model confidence grid.
   *
   * Per-row "Use this": applies the chosen species as a correction to the
   * detection (updates label/model/confidence in place, marks verified) and
   * reloads the detail page to reflect the new state.
   *
   * Nothing is written to the database until the user explicitly clicks
   * "Use this" and confirms — the reanalysis itself is read-only.
   */
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import {
    reanalyzeDetection,
    correctDetectionSpecies,
    type ReanalyzeResult,
    type ReanalyzePrediction,
  } from '$lib/utils/reanalyzeDetection';
  import { t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { loggers } from '$lib/utils/logger';
  import { Sparkles, AlertCircle, Check } from '@lucide/svelte';

  interface Props {
    isOpen: boolean;
    detectionId: number | null;
    onClose: () => void;
  }

  let { isOpen = false, detectionId = null, onClose }: Props = $props();

  const logger = loggers.ui;

  let isRunning = $state(false);
  let result = $state<ReanalyzeResult | null>(null);
  let errorMessage = $state<string | null>(null);

  // Correction-confirm state. We don't use a separate modal for this — a
  // single confirmation row inline below the table keeps the user's eye on
  // the prediction they're about to apply.
  let pendingCorrection = $state<ReanalyzePrediction | null>(null);
  let isCorrecting = $state(false);

  // Auto-run reanalysis every time the modal opens. Re-runs on reopen so the
  // results reflect any settings changes that happened while the modal was
  // closed (e.g. user enabled an extra model in the gallery).
  $effect(() => {
    if (!isOpen || detectionId === null) return;
    result = null;
    errorMessage = null;
    pendingCorrection = null;
    runReanalysis();
  });

  async function runReanalysis() {
    if (!detectionId || isRunning) return;
    isRunning = true;
    errorMessage = null;
    try {
      const res = await reanalyzeDetection(detectionId);
      if (res === null) {
        // Duplicate in-flight; the original call will populate result.
        return;
      }
      result = res;
    } catch (err) {
      errorMessage = err instanceof Error ? err.message : String(err);
    } finally {
      isRunning = false;
    }
  }

  function startCorrection(pred: ReanalyzePrediction) {
    pendingCorrection = pred;
  }

  function cancelCorrection() {
    pendingCorrection = null;
  }

  /**
   * Pick which model's read to attribute the correction to. Strategy:
   * highest-confidence model wins, because that's the model that "sees" the
   * species most clearly — and its label vocabulary is most likely to
   * contain the species (the backend rejects corrections where the model's
   * vocabulary doesn't include the chosen species).
   */
  function chooseModelForCorrection(
    pred: ReanalyzePrediction
  ): { modelId: string; confidence: number } | null {
    let bestModel = '';
    let bestConf = -1;
    for (const [modelId, conf] of Object.entries(pred.byModel)) {
      if (conf > bestConf) {
        bestConf = conf;
        bestModel = modelId;
      }
    }
    if (bestModel === '') return null;
    return { modelId: bestModel, confidence: bestConf };
  }

  async function confirmCorrection() {
    if (!detectionId || !pendingCorrection || isCorrecting) return;
    const choice = chooseModelForCorrection(pendingCorrection);
    if (!choice) {
      errorMessage = t('detections.reanalyze.noModelForCorrection');
      return;
    }
    isCorrecting = true;
    try {
      const applied = await correctDetectionSpecies(detectionId, {
        scientificName: pendingCorrection.scientificName,
        modelId: choice.modelId,
        confidence: choice.confidence,
      });
      if (applied === null) {
        // Duplicate in-flight; just close.
        onClose();
        return;
      }
      toastActions.success(
        t('detections.reanalyze.correctionApplied', {
          species: applied.commonName || applied.scientificName,
        })
      );
      onClose();
      // Reload the page so every widget on the detail view reflects the new
      // species label/confidence. A targeted refetch would be lighter, but
      // the detection detail page reads from many derived endpoints
      // (taxonomy, history, rarity) that would all need invalidating
      // individually.
      window.location.reload();
    } catch (err) {
      errorMessage = err instanceof Error ? err.message : String(err);
      logger.error('Correction failed', err, {
        detectionId,
        scientific: pendingCorrection.scientificName,
      });
    } finally {
      isCorrecting = false;
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

  // Friendly model summary line: e.g. "BirdNET v2.4 + Google Perch v2 • 45s of audio".
  let summaryLine = $derived.by(() => {
    if (!result) return '';
    const modelNames = result.modelsRun.map(m => m.name).join(' + ');
    return t('detections.reanalyze.multiModelSummary', {
      models: modelNames,
      duration: formatDuration(result.clipDurationSec),
    });
  });
</script>

<Modal {isOpen} title={t('detections.reanalyze.title')} {onClose}>
  <div class="space-y-4">
    <p class="text-sm text-base-content/70">
      {t('detections.reanalyze.descriptionMulti')}
    </p>

    {#if isRunning}
      <div class="flex items-center gap-2 text-sm text-base-content/70">
        <span class="loading loading-spinner loading-sm"></span>
        <span>{t('detections.reanalyze.running')}</span>
      </div>
    {/if}

    {#if errorMessage}
      <div role="alert" class="alert alert-error text-sm">
        <AlertCircle class="h-4 w-4" />
        <span>{errorMessage}</span>
      </div>
    {/if}

    {#if result && !isRunning}
      <div class="space-y-2">
        <div class="text-xs text-base-content/60">
          {summaryLine}
        </div>

        {#if result.predictions.length === 0}
          <div class="text-sm italic text-base-content/70">
            {t('detections.reanalyze.noPredictions')}
          </div>
        {:else}
          <div class="overflow-x-auto">
            <table class="table table-sm w-full">
              <thead>
                <tr>
                  <th class="text-left">{t('detections.reanalyze.colSpecies')}</th>
                  {#each result.modelsRun as m (m.id)}
                    <th class="text-right whitespace-nowrap" title={m.name}>{m.name}</th>
                  {/each}
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {#each result.predictions as pred, idx (idx)}
                  <tr>
                    <td class="text-sm">
                      {#if pred.commonName}
                        <div class="font-medium">{pred.commonName}</div>
                        <div class="font-mono text-xs italic text-base-content/60">
                          {pred.scientificName}
                        </div>
                      {:else}
                        <div class="font-mono">{pred.scientificName}</div>
                      {/if}
                    </td>
                    {#each result.modelsRun as m (m.id)}
                      {@const conf = pred.byModel[m.id]}
                      <td class="text-right tabular-nums whitespace-nowrap">
                        {#if conf !== undefined}
                          {formatConfidencePercent(conf)}
                        {:else}
                          <span class="text-base-content/30">—</span>
                        {/if}
                      </td>
                    {/each}
                    <td class="text-right">
                      <button
                        type="button"
                        class="btn btn-xs btn-ghost"
                        onclick={() => startCorrection(pred)}
                        disabled={isCorrecting}
                        aria-label={t('detections.reanalyze.useThisAriaLabel', {
                          species: pred.commonName || pred.scientificName,
                        })}
                      >
                        <Check class="h-3.5 w-3.5" />
                        {t('detections.reanalyze.useThis')}
                      </button>
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </div>
    {/if}

    <!-- Inline confirmation block. Shows below the table once the user has
         chosen a row; double-confirm avoids accidental corrections. -->
    {#if pendingCorrection}
      <div class="rounded-md border border-warning/40 bg-warning/10 p-3 text-sm">
        <div class="mb-2 font-medium">
          {t('detections.reanalyze.confirmTitle', {
            species: pendingCorrection.commonName || pendingCorrection.scientificName,
          })}
        </div>
        <p class="mb-3 text-base-content/70">
          {t('detections.reanalyze.confirmBody')}
        </p>
        <div class="flex justify-end gap-2">
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            onclick={cancelCorrection}
            disabled={isCorrecting}
          >
            {t('common.cancel')}
          </button>
          <button
            type="button"
            class="btn btn-sm btn-primary"
            onclick={confirmCorrection}
            disabled={isCorrecting}
          >
            <Sparkles class="h-3.5 w-3.5" />
            {#if isCorrecting}
              {t('detections.reanalyze.applying')}
            {:else}
              {t('detections.reanalyze.confirmApply')}
            {/if}
          </button>
        </div>
      </div>
    {/if}
  </div>
</Modal>
