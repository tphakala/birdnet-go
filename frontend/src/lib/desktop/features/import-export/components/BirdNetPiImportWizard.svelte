<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { t } from '$lib/i18n';
  import { api, ApiError } from '$lib/utils/api';
  import { ReconnectingEventSource } from '$lib/utils/ReconnectingEventSource';
  import { toastActions } from '$lib/stores/toast';
  import { loggers } from '$lib/utils/logger';
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import ProgressBar from '$lib/desktop/components/ui/ProgressBar.svelte';
  import Badge from '$lib/desktop/components/ui/Badge.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { CheckCircle2, XCircle, AlertTriangle, ArrowLeft, ArrowRight } from '@lucide/svelte';
  import type {
    ExternalMediaResponse,
    SourceAccessState,
    StartImportRequest,
    StartImportResponse,
    ImportProgress,
    ImportErrorEvent,
    CancelResponse,
    ImportStatusResponse,
    WizardStep,
  } from '../types';
  import { deriveSourceAccessState, buildDetectionsFilterUrl } from '../utils';
  import { formatNumber } from '$lib/utils/formatters';

  const logger = loggers.ui;

  interface Props {
    onClose: () => void;
  }

  let { onClose }: Props = $props();

  // Wizard state
  let currentStep = $state<WizardStep>('source');
  let isLoading = $state(false);
  let errorMessage = $state<string | null>(null);

  // Source access state
  let mediaResponse = $state<ExternalMediaResponse | null>(null);
  let sourceAccessState = $state<SourceAccessState | null>(null);
  let mediaLoadError = $state<string | null>(null);

  // Path input (relative to mount root)
  let sourcePath = $state('birdnet-pi/birds.db');

  // Progress / run state
  let jobId = $state<string | null>(null);
  let importProgress = $state<ImportProgress | null>(null);
  let importComplete = $state(false);
  let importCancelled = $state(false);
  let importError = $state<string | null>(null);
  let isCancelling = $state(false);
  let eventSource: ReconnectingEventSource | null = null;
  let destroyed = false;

  // Step labels for the indicator
  const stepLabels: WizardStep[] = ['source', 'mode', 'confirm', 'progress', 'done'];

  let currentStepIndex = $derived(stepLabels.indexOf(currentStep));

  let canProceedFromSource = $derived(
    sourceAccessState === 'container-mount' && sourcePath.trim().length > 0
  );

  let progressPercent = $derived.by(() => {
    if (!importProgress || importProgress.total === 0) return 0;
    return Math.min(100, Math.round((importProgress.processed / importProgress.total) * 100));
  });

  // On mount: check for in-progress import and discover external media
  onMount(() => {
    void loadInitialData();
  });

  async function loadInitialData() {
    isLoading = true;
    errorMessage = null;
    try {
      // Check for already-running import
      const statusResp = await api.get<ImportStatusResponse>('/api/v2/import/status');
      if (destroyed) return;
      if (statusResp.running && statusResp.job_id) {
        jobId = statusResp.job_id;
        if (statusResp.progress) {
          importProgress = statusResp.progress;
        }
        currentStep = 'progress';
        isLoading = false;
        connectEventSource(statusResp.job_id);
        return;
      }

      // Discover external media
      await loadExternalMedia();
      if (destroyed) return;
    } catch (err) {
      if (err instanceof ApiError) {
        errorMessage = err.userMessage;
      } else {
        errorMessage = t('system.importExport.errors.loadFailed');
      }
    } finally {
      isLoading = false;
    }
  }

  async function loadExternalMedia() {
    mediaLoadError = null;
    try {
      const resp = await api.get<ExternalMediaResponse>('/api/v2/system/external-media');
      mediaResponse = resp;
      sourceAccessState = deriveSourceAccessState(resp);
    } catch (err) {
      if (err instanceof ApiError) {
        mediaLoadError = err.userMessage;
      } else {
        mediaLoadError = t('system.importExport.errors.mediaLoadFailed');
      }
    }
  }

  async function recheckMedia() {
    isLoading = true;
    try {
      await loadExternalMedia();
    } finally {
      isLoading = false;
    }
  }

  function connectEventSource(id: string) {
    closeEventSource();
    const es = new ReconnectingEventSource(`/api/v2/import/jobs/${id}/progress`);

    es.addEventListener('progress', (event: Event) => {
      try {
        const data = JSON.parse((event as MessageEvent).data) as ImportProgress;
        importProgress = data;
      } catch (e) {
        logger.error('Failed to parse progress event', e);
      }
    });

    es.addEventListener('complete', (event: Event) => {
      try {
        const data = JSON.parse((event as MessageEvent).data) as ImportProgress;
        importProgress = data;
      } catch (e) {
        logger.error('Failed to parse complete event', e);
      }
      importComplete = true;
      currentStep = 'done';
      closeEventSource();
    });

    es.addEventListener('cancelled', (event: Event) => {
      try {
        const data = JSON.parse((event as MessageEvent).data) as ImportProgress;
        importProgress = data;
      } catch {
        // ignore parse errors for cancelled event
      }
      importCancelled = true;
      currentStep = 'done';
      closeEventSource();
    });

    es.addEventListener('error', (event: Event) => {
      // EventSource also fires 'error' for native transport drops (no .data);
      // ReconnectingEventSource reconnects those, so do not terminate the job on them.
      if (!(event instanceof MessageEvent) || typeof event.data !== 'string') {
        return;
      }
      try {
        const data = JSON.parse(event.data) as ImportErrorEvent;
        importProgress = {
          total: data.total,
          processed: data.processed,
          inserted: data.inserted,
          skipped: data.skipped,
          errors: data.errors,
          phase: data.phase,
        };
      } catch (e) {
        logger.error('Failed to parse import error event', e);
      }
      // Always show the localized message, never the raw backend string.
      importError = t('system.importExport.errors.importFailed');
      currentStep = 'done';
      closeEventSource();
    });

    // heartbeat: keep-alive only, no-op
    es.addEventListener('heartbeat', (_event: Event) => {});

    eventSource = es;
  }

  function closeEventSource() {
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
  }

  async function startImport() {
    if (!sourcePath.trim()) return;
    isLoading = true;
    errorMessage = null;

    const body: StartImportRequest = {
      mode: 'db-only',
      source_path: sourcePath.trim(),
    };

    try {
      const resp = await api.post<StartImportResponse>('/api/v2/import/birdnet-pi', body);
      if (destroyed) return;
      jobId = resp.job_id;
      currentStep = 'progress';
      connectEventSource(resp.job_id);
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 409) {
          errorMessage = t('system.importExport.errors.alreadyRunning');
        } else {
          errorMessage = err.userMessage;
        }
      } else {
        errorMessage = t('system.importExport.errors.startFailed');
      }
    } finally {
      isLoading = false;
    }
  }

  async function cancelImport() {
    if (!jobId || isCancelling) return;
    isCancelling = true;
    try {
      await api.post<CancelResponse>(`/api/v2/import/jobs/${jobId}/cancel`);
    } catch (err) {
      logger.error('Cancel request failed', err);
      toastActions.error(t('system.importExport.errors.cancelFailed'));
    } finally {
      isCancelling = false;
    }
  }

  function goToStep(step: WizardStep) {
    errorMessage = null;
    currentStep = step;
  }

  onDestroy(() => {
    destroyed = true;
    closeEventSource();
  });
</script>

<Modal
  isOpen={true}
  size="2xl"
  title={t('system.importExport.birdnetPi.wizardTitle')}
  {onClose}
  closeOnBackdrop={currentStep !== 'progress'}
  closeOnEsc={currentStep !== 'progress'}
  showCloseButton={false}
>
  {#snippet header()}
    <div class="flex flex-col gap-3">
      <h3 id="modal-title" class="font-bold text-lg text-[var(--color-base-content)]">
        {t('system.importExport.birdnetPi.wizardTitle')}
      </h3>

      <!-- Step indicator -->
      <div
        class="flex items-center gap-1"
        role="group"
        aria-label={t('system.importExport.wizard.stepsLabel')}
      >
        {#each stepLabels as step, i (step)}
          <div class="flex items-center gap-1">
            <div
              class="flex items-center justify-center size-6 rounded-full text-xs font-medium transition-colors {i <
              currentStepIndex
                ? 'bg-[var(--color-primary)] text-[var(--color-primary-content)]'
                : i === currentStepIndex
                  ? 'bg-[var(--color-primary)] text-[var(--color-primary-content)] ring-2 ring-[var(--color-primary)]/30'
                  : 'bg-[var(--color-base-300)] text-[var(--color-base-content)]/60'}"
              aria-label={t(`system.importExport.steps.${step}`)}
              aria-current={i === currentStepIndex ? 'step' : undefined}
            >
              {i + 1}
            </div>
            {#if i < stepLabels.length - 1}
              <div
                class="w-8 h-px {i < currentStepIndex
                  ? 'bg-[var(--color-primary)]'
                  : 'bg-[var(--color-base-300)]'}"
              ></div>
            {/if}
          </div>
        {/each}
      </div>
    </div>
  {/snippet}

  {#snippet children()}
    <div class="min-h-[12rem]">
      {#if isLoading && currentStep === 'source'}
        <div class="flex items-center justify-center py-8">
          <LoadingSpinner label={t('system.importExport.loading')} />
        </div>
      {:else if errorMessage && currentStep !== 'progress' && currentStep !== 'confirm'}
        <ErrorAlert message={errorMessage} type="error" />
      {:else if currentStep === 'source'}
        <!-- Source access step -->
        {#if mediaLoadError}
          <div class="space-y-3">
            <ErrorAlert message={mediaLoadError} type="error" />
            <div>
              <Button variant="default" onclick={recheckMedia} disabled={isLoading}>
                {t('system.importExport.sourceAccess.recheckButton')}
              </Button>
            </div>
          </div>
        {:else if sourceAccessState === 'native'}
          <!-- Native state: informational panel only -->
          <div
            class="space-y-3"
            role="region"
            aria-label={t('system.importExport.sourceAccess.nativeTitle')}
          >
            <div
              class="flex items-start gap-3 p-4 rounded-lg bg-[color-mix(in_srgb,var(--color-info)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-info)_30%,transparent)]"
            >
              <AlertTriangle class="size-5 shrink-0 mt-0.5 text-[var(--color-info)]" />
              <div class="space-y-2">
                <p class="font-medium text-[var(--color-base-content)]">
                  {t('system.importExport.sourceAccess.nativeTitle')}
                </p>
                <p class="text-sm text-[var(--color-base-content)]/80">
                  {t('system.importExport.sourceAccess.nativeDescription')}
                </p>
                <p class="text-sm text-[var(--color-base-content)]/80">
                  {t('system.importExport.sourceAccess.nativeHowTo')}
                </p>
              </div>
            </div>
          </div>
        {:else if sourceAccessState === 'container-missing'}
          <!-- Container + missing mount: guided setup -->
          <div class="space-y-3">
            <div
              class="flex items-start gap-3 p-4 rounded-lg bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]"
            >
              <AlertTriangle class="size-5 shrink-0 mt-0.5 text-[var(--color-warning)]" />
              <div>
                <p class="font-medium text-[var(--color-base-content)]">
                  {t('system.importExport.sourceAccess.missingTitle')}
                </p>
                <p class="text-sm text-[var(--color-base-content)]/80 mt-1">
                  {t('system.importExport.sourceAccess.missingDescription')}
                </p>
              </div>
            </div>

            {#if mediaResponse?.guidance?.steps && mediaResponse.guidance.steps.length > 0}
              <div>
                <p class="text-sm font-medium text-[var(--color-base-content)] mb-2">
                  {t('system.importExport.sourceAccess.setupStepsLabel')}
                </p>
                <ol class="space-y-2">
                  {#each mediaResponse.guidance.steps as step, i (i)}
                    <li class="flex items-start gap-2">
                      <span
                        class="flex-shrink-0 text-xs font-medium text-[var(--color-base-content)]/60 mt-0.5"
                        >{i + 1}.</span
                      >
                      <code
                        class="text-xs bg-[var(--color-base-300)] px-2 py-1 rounded text-[var(--color-base-content)] font-mono break-all select-all"
                        >{step}</code
                      >
                    </li>
                  {/each}
                </ol>
              </div>
            {/if}

            <div class="flex items-center gap-2 pt-2">
              <Button variant="default" onclick={recheckMedia} disabled={isLoading}>
                {#if isLoading}
                  <LoadingSpinner size="xs" />
                {/if}
                {t('system.importExport.sourceAccess.recheckButton')}
              </Button>
              <span class="text-xs text-[var(--color-base-content)]/60">
                {t('system.importExport.sourceAccess.recheckHint')}
              </span>
            </div>
          </div>
        {:else if sourceAccessState === 'container-mount'}
          <!-- Container + mount present: path entry -->
          <div class="space-y-4">
            <p class="text-sm text-[var(--color-base-content)]/80">
              {t('system.importExport.sourceAccess.mountDescription')}
            </p>

            {#if mediaResponse}
              <div class="text-sm bg-[var(--color-base-200)] rounded px-3 py-2 font-mono">
                <span class="text-[var(--color-base-content)]/60"
                  >{t('system.importExport.sourceAccess.mountRoot')}:</span
                >
                <span class="ml-1 text-[var(--color-base-content)]">{mediaResponse.mount_path}</span
                >
              </div>
            {/if}

            <TextInput
              id="birds-db-path"
              label={t('system.importExport.sourceAccess.pathLabel')}
              bind:value={sourcePath}
              placeholder="birdnet-pi/birds.db"
              helpText={t('system.importExport.sourceAccess.pathHelpText')}
              required={true}
            />
            {#if !sourcePath.trim()}
              <p
                id="source-path-required"
                class="text-sm text-[var(--color-base-content)]/60"
                aria-live="polite"
              >
                {t('system.importExport.sourceAccess.pathRequiredReason')}
              </p>
            {/if}
          </div>
        {:else}
          <!-- mediaResponse not loaded yet -->
          <div class="flex items-center justify-center py-8">
            <LoadingSpinner label={t('system.importExport.loading')} />
          </div>
        {/if}
      {:else if currentStep === 'mode'}
        <!-- Mode selection step -->
        <div class="space-y-3" role="radiogroup" aria-labelledby="mode-group-label">
          <p
            id="mode-group-label"
            class="text-sm font-medium text-[var(--color-base-content)] mb-2"
          >
            {t('system.importExport.mode.label')}
          </p>

          <!-- db-only option -->
          <label
            class="flex items-start gap-3 p-4 rounded-lg border cursor-pointer transition-colors border-[var(--color-primary)] bg-[color-mix(in_srgb,var(--color-primary)_5%,transparent)]"
          >
            <input type="radio" name="import-mode" value="db-only" checked={true} class="mt-1" />
            <div>
              <span class="font-medium text-[var(--color-base-content)]"
                >{t('system.importExport.mode.dbOnly.label')}</span
              >
              <p class="text-sm text-[var(--color-base-content)]/70 mt-0.5">
                {t('system.importExport.mode.dbOnly.description')}
              </p>
            </div>
          </label>

          <!-- db-audio option (disabled) -->
          <label
            class="flex items-start gap-3 p-4 rounded-lg border border-[var(--color-base-300)] opacity-60 cursor-not-allowed"
            aria-disabled="true"
          >
            <input
              type="radio"
              name="import-mode"
              value="db-audio"
              disabled={true}
              aria-describedby="db-audio-disabled-reason"
              class="mt-1"
            />
            <div>
              <div class="flex items-center gap-2">
                <span class="font-medium text-[var(--color-base-content)]/70"
                  >{t('system.importExport.mode.dbAudio.label')}</span
                >
                <Badge
                  variant="warning"
                  size="xs"
                  text={t('system.importExport.mode.dbAudio.badge')}
                />
              </div>
              <p class="text-sm text-[var(--color-base-content)]/50 mt-0.5">
                {t('system.importExport.mode.dbAudio.description')}
              </p>
              <p id="db-audio-disabled-reason" class="text-xs text-[var(--color-warning)] mt-1">
                {t('system.importExport.mode.dbAudio.disabledReason')}
              </p>
            </div>
          </label>
        </div>
      {:else if currentStep === 'confirm'}
        <!-- Confirm/preview step -->
        <div class="space-y-4">
          <p class="text-sm text-[var(--color-base-content)]/80">
            {t('system.importExport.confirm.description')}
          </p>

          <dl class="space-y-3">
            <div class="flex gap-3">
              <dt class="text-sm font-medium text-[var(--color-base-content)]/60 w-28 shrink-0">
                {t('system.importExport.confirm.source')}:
              </dt>
              <dd class="text-sm font-mono text-[var(--color-base-content)] break-all">
                {sourcePath}
              </dd>
            </div>
            <div class="flex gap-3">
              <dt class="text-sm font-medium text-[var(--color-base-content)]/60 w-28 shrink-0">
                {t('system.importExport.confirm.mode')}:
              </dt>
              <dd class="text-sm text-[var(--color-base-content)]">
                {t('system.importExport.mode.dbOnly.label')}
              </dd>
            </div>
            <div class="flex gap-3">
              <dt class="text-sm font-medium text-[var(--color-base-content)]/60 w-28 shrink-0">
                {t('system.importExport.confirm.tagging')}:
              </dt>
              <dd class="text-sm text-[var(--color-base-content)]">
                {t('system.importExport.confirm.taggingValue')}
              </dd>
            </div>
          </dl>

          <div
            class="p-3 rounded-lg bg-[color-mix(in_srgb,var(--color-info)_8%,transparent)] border border-[color-mix(in_srgb,var(--color-info)_25%,transparent)] text-sm text-[var(--color-base-content)]/80"
          >
            {t('system.importExport.confirm.deduplicationNote')}
          </div>

          {#if errorMessage}
            <ErrorAlert message={errorMessage} type="error" />
          {/if}
        </div>
      {:else if currentStep === 'progress'}
        <!-- Progress/run step -->
        <div class="space-y-4" role="region" aria-label={t('system.importExport.progress.label')}>
          {#if !importComplete && !importCancelled && !importError}
            <div class="flex items-center gap-2">
              <LoadingSpinner size="sm" />
              <span class="text-sm font-medium text-[var(--color-base-content)]">
                {t('system.importExport.progress.runningLabel')}
                {#if importProgress?.phase && importProgress.phase !== 'done'}
                  <span class="text-[var(--color-base-content)]/60">
                    - {t(`system.importExport.progress.phase.${importProgress.phase}`)}
                  </span>
                {/if}
              </span>
            </div>
          {/if}

          <div
            role="status"
            aria-live="polite"
            aria-label={t('system.importExport.progress.progressLabel', {
              percent: String(progressPercent),
            })}
          >
            <ProgressBar
              value={progressPercent}
              max={100}
              showLabel={true}
              striped={!importComplete && !importCancelled && !importError}
              animated={!importComplete && !importCancelled && !importError}
              variant={importError ? 'error' : importCancelled ? 'warning' : 'primary'}
            />
          </div>

          {#if importProgress}
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3">
              <div class="text-center p-2 rounded bg-[var(--color-base-200)]">
                <div class="text-lg font-semibold text-[var(--color-base-content)]">
                  {formatNumber(importProgress.processed)}
                </div>
                <div class="text-xs text-[var(--color-base-content)]/60">
                  {t('system.importExport.progress.processed')}
                </div>
              </div>
              <div class="text-center p-2 rounded bg-[var(--color-base-200)]">
                <div class="text-lg font-semibold text-[var(--color-success)]">
                  {formatNumber(importProgress.inserted)}
                </div>
                <div class="text-xs text-[var(--color-base-content)]/60">
                  {t('system.importExport.progress.inserted')}
                </div>
              </div>
              <div class="text-center p-2 rounded bg-[var(--color-base-200)]">
                <div class="text-lg font-semibold text-[var(--color-base-content)]/60">
                  {formatNumber(importProgress.skipped)}
                </div>
                <div class="text-xs text-[var(--color-base-content)]/60">
                  {t('system.importExport.progress.skipped')}
                </div>
              </div>
              <div class="text-center p-2 rounded bg-[var(--color-base-200)]">
                <div
                  class="text-lg font-semibold {importProgress.errors > 0
                    ? 'text-[var(--color-error)]'
                    : 'text-[var(--color-base-content)]/60'}"
                >
                  {formatNumber(importProgress.errors)}
                </div>
                <div class="text-xs text-[var(--color-base-content)]/60">
                  {t('system.importExport.progress.errors')}
                </div>
              </div>
            </div>
          {/if}

          {#if importError}
            <div role="alert">
              <ErrorAlert message={importError} type="error" />
            </div>
          {/if}

          {#if importCancelled}
            <ErrorAlert
              message={t('system.importExport.progress.cancelledMessage')}
              type="warning"
            />
          {/if}
        </div>
      {:else if currentStep === 'done'}
        <!-- Done step -->
        <div class="space-y-4 text-center">
          {#if importComplete && !importError && !importCancelled}
            <CheckCircle2 class="size-12 mx-auto text-[var(--color-success)]" />
            <div>
              <h4 class="text-lg font-semibold text-[var(--color-base-content)]">
                {t('system.importExport.done.successTitle')}
              </h4>
              <p class="text-sm text-[var(--color-base-content)]/70 mt-1">
                {t('system.importExport.done.successDescription')}
              </p>
            </div>
            {#if importProgress}
              <div class="grid grid-cols-3 gap-3 text-left">
                <div class="p-3 rounded-lg bg-[var(--color-base-200)] text-center">
                  <div class="text-xl font-bold text-[var(--color-success)]">
                    {formatNumber(importProgress.inserted)}
                  </div>
                  <div class="text-xs text-[var(--color-base-content)]/60">
                    {t('system.importExport.progress.inserted')}
                  </div>
                </div>
                <div class="p-3 rounded-lg bg-[var(--color-base-200)] text-center">
                  <div class="text-xl font-bold text-[var(--color-base-content)]/60">
                    {formatNumber(importProgress.skipped)}
                  </div>
                  <div class="text-xs text-[var(--color-base-content)]/60">
                    {t('system.importExport.progress.skipped')}
                  </div>
                </div>
                <div class="p-3 rounded-lg bg-[var(--color-base-200)] text-center">
                  <div
                    class="text-xl font-bold {importProgress.errors > 0
                      ? 'text-[var(--color-error)]'
                      : 'text-[var(--color-base-content)]/60'}"
                  >
                    {formatNumber(importProgress.errors)}
                  </div>
                  <div class="text-xs text-[var(--color-base-content)]/60">
                    {t('system.importExport.progress.errors')}
                  </div>
                </div>
              </div>
              <div class="mt-2">
                <a
                  href={buildDetectionsFilterUrl()}
                  class="inline-flex items-center gap-1.5 text-sm text-[var(--color-primary)] hover:underline"
                >
                  {t('system.importExport.done.viewDetectionsLink')}
                </a>
              </div>
            {/if}
          {:else if importCancelled}
            <XCircle class="size-12 mx-auto text-[var(--color-warning)]" />
            <div>
              <h4 class="text-lg font-semibold text-[var(--color-base-content)]">
                {t('system.importExport.done.cancelledTitle')}
              </h4>
              <p class="text-sm text-[var(--color-base-content)]/70 mt-1">
                {t('system.importExport.done.cancelledDescription')}
              </p>
            </div>
            {#if importProgress && importProgress.inserted > 0}
              <p class="text-sm text-[var(--color-base-content)]/70">
                {t('system.importExport.done.partialInserted', {
                  count: String(importProgress.inserted),
                })}
              </p>
            {/if}
          {:else if importError}
            <XCircle class="size-12 mx-auto text-[var(--color-error)]" />
            <div>
              <h4 class="text-lg font-semibold text-[var(--color-base-content)]">
                {t('system.importExport.done.errorTitle')}
              </h4>
              <p class="text-sm text-[var(--color-base-content)]/70 mt-1">{importError}</p>
            </div>
          {/if}
        </div>
      {/if}
    </div>
  {/snippet}

  {#snippet footer()}
    <div class="flex items-center justify-between w-full">
      <!-- Left: back button -->
      <div>
        {#if currentStep === 'mode'}
          <Button variant="ghost" onclick={() => goToStep('source')}>
            <ArrowLeft class="size-4" />
            {t('common.buttons.back')}
          </Button>
        {:else if currentStep === 'confirm'}
          <Button variant="ghost" onclick={() => goToStep('mode')}>
            <ArrowLeft class="size-4" />
            {t('common.buttons.back')}
          </Button>
        {:else}
          <div></div>
        {/if}
      </div>

      <!-- Right: forward/action buttons -->
      <div class="flex items-center gap-2">
        {#if currentStep === 'source'}
          {#if sourceAccessState === 'native' || sourceAccessState === 'container-missing'}
            <Button variant="default" onclick={onClose}>
              {t('common.buttons.close')}
            </Button>
          {:else if sourceAccessState === 'container-mount'}
            <Button
              variant="primary"
              onclick={() => goToStep('mode')}
              disabled={!canProceedFromSource}
              title={!canProceedFromSource
                ? t('system.importExport.sourceAccess.pathRequiredReason')
                : undefined}
              aria-describedby={!canProceedFromSource ? 'source-path-required' : undefined}
            >
              {t('common.buttons.next')}
              <ArrowRight class="size-4" />
            </Button>
          {:else}
            <!-- still loading -->
            <Button variant="default" onclick={onClose}>
              {t('common.buttons.close')}
            </Button>
          {/if}
        {:else if currentStep === 'mode'}
          <Button variant="primary" onclick={() => goToStep('confirm')}>
            {t('common.buttons.next')}
            <ArrowRight class="size-4" />
          </Button>
        {:else if currentStep === 'confirm'}
          <Button variant="default" onclick={onClose}>
            {t('common.buttons.cancel')}
          </Button>
          <Button
            variant="primary"
            onclick={startImport}
            disabled={isLoading || !sourcePath.trim()}
            title={!sourcePath.trim()
              ? t('system.importExport.sourceAccess.pathRequiredReason')
              : undefined}
          >
            {#if isLoading}
              <LoadingSpinner size="xs" color="text-[var(--color-primary-content)]" />
            {/if}
            {t('system.importExport.confirm.startButton')}
          </Button>
        {:else if currentStep === 'progress'}
          {#if !importComplete && !importCancelled && !importError}
            <Button variant="ghost" onclick={onClose}>
              {t('system.importExport.runInBackground')}
            </Button>
            <Button
              variant="error"
              onclick={cancelImport}
              disabled={isCancelling}
              title={isCancelling ? t('system.importExport.progress.cancellingLabel') : undefined}
            >
              {#if isCancelling}
                <LoadingSpinner size="xs" />
              {/if}
              {isCancelling
                ? t('system.importExport.progress.cancellingLabel')
                : t('system.importExport.progress.cancelButton')}
            </Button>
          {:else}
            <Button variant="default" onclick={onClose}>
              {t('common.buttons.close')}
            </Button>
          {/if}
        {:else if currentStep === 'done'}
          <Button variant="primary" onclick={onClose}>
            {t('common.buttons.close')}
          </Button>
        {/if}
      </div>
    </div>
  {/snippet}
</Modal>
