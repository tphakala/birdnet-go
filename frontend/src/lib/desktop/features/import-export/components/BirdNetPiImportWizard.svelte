<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { t } from '$lib/i18n';
  import { api, ApiError } from '$lib/utils/api';
  import type { ReconnectingEventSource } from '$lib/utils/ReconnectingEventSource';
  import { toastActions } from '$lib/stores/toast';
  import { loggers } from '$lib/utils/logger';
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import ProgressBar from '$lib/desktop/components/ui/ProgressBar.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { CheckCircle2, XCircle, ArrowLeft, ArrowRight } from '@lucide/svelte';
  import type {
    ImportSourcesResponse,
    SourceCandidate,
    SourceStepState,
    ValidateSourceResponse,
    ElevateResponse,
    StartImportRequest,
    StartImportResponse,
    ImportProgress,
    CancelResponse,
    ImportStatusResponse,
    WizardStep,
  } from '../types';
  import ImportCountTile from './ImportCountTile.svelte';
  import {
    buildDetectionsFilterUrl,
    connectImportProgressStream,
    deriveSourceStepState,
    importProgressPercent,
    isUnreadable,
  } from '../utils';
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

  // Source discovery state
  let sourcesResponse = $state<ImportSourcesResponse | null>(null);
  let sourceStepState = $state<SourceStepState | null>(null);
  let sourcesLoadError = $state<string | null>(null);

  // Manual entry state
  let showManualEntry = $state(false);
  let manualPath = $state('');
  let validateResp = $state<ValidateSourceResponse | null>(null);
  // C14: removed dead 'valid' variant
  let validateStatus = $state<'idle' | 'validating' | 'invalid'>('idle');

  // Elevation state (for unreadable candidates, panel lives at confirm step)
  let needsElevation = $state(false);
  let elevating = $state(false);
  let showPasswordPanel = $state(false);
  let sudoPassword = $state('');
  let fallbackCommands = $state<string[]>([]);
  let elevationError = $state<string | null>(null);

  // True when the page is served over plain HTTP (password warning required).
  // Use globalThis.location to avoid the no-undef lint rule while supporting SSR.
  const isPlainHttp = globalThis.location?.protocol === 'http:';

  // Selected source path (absolute path set by candidate selection or manual entry)
  let sourcePath = $state('');

  // Selected import mode
  let selectedMode = $state<'db-only' | 'db-audio'>('db-only');

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

  let progressPercent = $derived(importProgressPercent(importProgress));

  function applyFinalStatus(s: ImportStatusResponse) {
    if (s.progress) importProgress = s.progress;
    importCancelled = s.cancelled === true;
    importError = s.error ? t('system.importExport.errors.importFailed') : null;
    importComplete = !s.error && !importCancelled;
    currentStep = 'done';
  }

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
      if (statusResp.status === 'done') {
        applyFinalStatus(statusResp);
        isLoading = false;
        return;
      }
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

      // Discover source databases
      await loadSources();
      if (destroyed) return;
    } catch (err) {
      if (destroyed) return;
      if (err instanceof ApiError) {
        errorMessage = err.userMessage;
      } else {
        errorMessage = t('system.importExport.errors.loadFailed');
      }
    } finally {
      isLoading = false;
    }
  }

  function startAnotherImport() {
    jobId = null;
    importProgress = null;
    importComplete = false;
    importCancelled = false;
    importError = null;
    errorMessage = null;
    isCancelling = false;
    sourcePath = '';
    showManualEntry = false;
    manualPath = '';
    validateStatus = 'idle';
    validateResp = null;
    // C12: reset elevation state to prevent stale panel flashing back
    needsElevation = false;
    elevating = false;
    showPasswordPanel = false;
    fallbackCommands = [];
    elevationError = null;
    sourceStepState = null;
    currentStep = 'source';
    void loadSources();
  }

  async function loadSources() {
    sourcesLoadError = null;
    try {
      const resp = await api.get<ImportSourcesResponse>('/api/v2/import/sources');
      if (destroyed) return;
      sourcesResponse = resp;
      sourceStepState = deriveSourceStepState(resp);
    } catch (err) {
      if (destroyed) return;
      if (err instanceof ApiError) {
        sourcesLoadError = err.userMessage;
      } else {
        sourcesLoadError = t('system.importExport.errors.mediaLoadFailed');
      }
    }
  }

  async function recheckSources() {
    isLoading = true;
    try {
      await loadSources();
    } finally {
      isLoading = false;
    }
  }

  /**
   * Called when any candidate (readable or unreadable) is selected. Sets
   * `needsElevation` based on whether the candidate is permission-denied, then
   * advances to the mode step. No network call is made here.
   */
  function selectCandidate(cand: SourceCandidate) {
    sourcePath = cand.path;
    needsElevation = isUnreadable(cand);
    // Reset elevation sub-state for the new candidate
    showPasswordPanel = false;
    fallbackCommands = [];
    elevationError = null;
    elevating = false;
    goToStep('mode');
  }

  async function useManualPath() {
    if (!manualPath.trim()) return;
    validateStatus = 'validating';
    validateResp = null;
    try {
      const resp = await api.post<ValidateSourceResponse>('/api/v2/import/validate', {
        source_path: manualPath.trim(),
      });
      if (destroyed) return;
      validateResp = resp;
      if (resp.valid) {
        sourcePath = manualPath.trim();
        needsElevation = false;
        validateStatus = 'idle';
        goToStep('mode');
      } else {
        validateStatus = 'invalid';
      }
    } catch {
      if (destroyed) return;
      validateStatus = 'invalid';
    }
  }

  async function elevate(path: string, withPassword: boolean) {
    elevating = true;
    elevationError = null;
    fallbackCommands = [];
    try {
      const body: { source_path: string; mode: string; password?: string } = {
        source_path: path,
        mode: selectedMode,
      };
      if (withPassword && sudoPassword) {
        body.password = sudoPassword;
      }
      const resp = await api.post<ElevateResponse>('/api/v2/import/elevate', body);
      if (destroyed) return;

      if (resp.method === 'password_required') {
        // Passwordless elevation failed and no password was supplied; prompt the user.
        showPasswordPanel = true;
        return;
      }

      if (resp.method === 'fallback') {
        fallbackCommands = resp.fallback_commands ?? [];
        showPasswordPanel = false;
        return;
      }

      // direct or sudo: must have a job_id
      // C13: guard against a contract violation where job_id is absent
      if (!resp.job_id) {
        elevationError = t('system.importExport.source.elevation.failed');
        return;
      }

      sourcePath = path;
      jobId = resp.job_id;
      currentStep = 'progress';
      connectEventSource(resp.job_id);
    } catch (err) {
      if (destroyed) return;
      // Genuine HTTP/network error: show the error and allow retry.
      // NEVER log sudoPassword or any part of the request body containing it.
      elevationError = t('system.importExport.source.elevation.failed');
      logger.error('Elevation request failed', err instanceof ApiError ? err.status : 'unknown');
    } finally {
      elevating = false;
      sudoPassword = ''; // clear on every return path; single-use memory
    }
  }

  function connectEventSource(id: string) {
    closeEventSource();
    eventSource = connectImportProgressStream(id, {
      onProgress: p => {
        importProgress = p;
      },
      onComplete: p => {
        if (p) importProgress = p;
        importComplete = true;
        currentStep = 'done';
        closeEventSource();
      },
      onCancelled: p => {
        if (p) importProgress = p;
        importCancelled = true;
        currentStep = 'done';
        closeEventSource();
      },
      onError: p => {
        if (p) importProgress = p;
        // Always show the localized message, never the raw backend string.
        importError = t('system.importExport.errors.importFailed');
        currentStep = 'done';
        closeEventSource();
      },
    });
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
      mode: selectedMode,
      source_path: sourcePath.trim(),
    };

    try {
      const resp = await api.post<StartImportResponse>('/api/v2/import/birdnet-pi', body);
      if (destroyed) return;
      jobId = resp.job_id;
      currentStep = 'progress';
      connectEventSource(resp.job_id);
    } catch (err) {
      if (destroyed) return;
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
      const resp = await api.post<CancelResponse>(`/api/v2/import/jobs/${jobId}/cancel`);
      if (destroyed) return;
      if (resp.status === 'done') {
        closeEventSource();
        if (currentStep === 'progress') {
          try {
            const s = await api.get<ImportStatusResponse>('/api/v2/import/status');
            if (destroyed) return;
            applyFinalStatus(s);
          } catch (err) {
            logger.error('Failed to load final import status after cancel', err);
          }
        }
      }
      // For a 'cancelling' response, keep isCancelling true: the SSE 'cancelled' event
      // moves the wizard to the done step (which hides Cancel). Re-enabling Cancel here
      // would let the user fire redundant cancel requests while cancellation is in flight.
    } catch (err) {
      if (destroyed) return;
      logger.error('Cancel request failed', err);
      toastActions.error(t('system.importExport.errors.cancelFailed'));
      isCancelling = false; // re-enable Cancel so the user can retry after a failed request
    }
  }

  function goToStep(step: WizardStep) {
    errorMessage = null;
    currentStep = step;
  }

  /** Label for a candidate's location kind. */
  function kindLabel(cand: SourceCandidate): string {
    if (cand.kind === 'removable') return t('system.importExport.source.kindRemovable');
    if (cand.kind === 'network') return t('system.importExport.source.kindNetwork');
    return t('system.importExport.source.kindLocal');
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
      <span class="sr-only" aria-live="polite"
        >{t('system.importExport.stepAnnouncement', {
          current: String(currentStepIndex + 1),
          total: String(stepLabels.length),
          name: t(`system.importExport.steps.${currentStep}`),
        })}</span
      >
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
        <!-- Source discovery step -->
        {#snippet manualEntryForm()}
          <div class="space-y-3 pt-3 border-t border-[var(--color-base-300)]">
            <TextInput
              id="manual-source-path"
              label={t('system.importExport.source.manualEntryLabel')}
              bind:value={manualPath}
              placeholder="/home/pi/BirdNET-Pi/birds.db"
              aria-describedby={validateStatus === 'invalid' ? 'manual-path-error' : undefined}
            />
            <!-- C18: persistent aria-live container for validation status -->
            <div aria-live="polite">
              {#if validateStatus === 'validating'}
                <p class="text-sm text-[var(--color-base-content)]/60">
                  {t('system.importExport.source.manualValidating')}
                </p>
              {:else if validateStatus === 'invalid'}
                <p id="manual-path-error" role="alert" class="text-sm text-[var(--color-error)]">
                  {validateResp?.reason === 'not_found'
                    ? t('system.importExport.source.manualNotFound')
                    : validateResp?.reason === 'permission_denied'
                      ? t('system.importExport.source.manualUnreadable')
                      : t('system.importExport.source.manualInvalid')}
                </p>
              {/if}
            </div>
            <Button
              variant="default"
              onclick={useManualPath}
              disabled={!manualPath.trim() || validateStatus === 'validating'}
              title={!manualPath.trim()
                ? t('system.importExport.sourceAccess.pathRequiredReason')
                : validateStatus === 'validating'
                  ? t('system.importExport.source.manualValidating')
                  : undefined}
            >
              {t('system.importExport.source.useThisButton')}
            </Button>
          </div>
        {/snippet}

        {#if sourcesLoadError}
          <div class="space-y-3">
            <ErrorAlert message={sourcesLoadError} type="error" />
            <div>
              <Button
                variant="default"
                onclick={recheckSources}
                disabled={isLoading}
                title={isLoading ? t('system.importExport.loading') : undefined}
              >
                {t('system.importExport.source.checkAgainButton')}
              </Button>
            </div>
          </div>
        {:else if sourceStepState === 'candidates'}
          <!-- At least one candidate found -->
          <div class="space-y-4">
            <p class="text-sm font-medium text-[var(--color-base-content)]">
              {t('system.importExport.source.title')}
            </p>
            <p class="text-sm text-[var(--color-base-content)]/80">
              {t('system.importExport.source.candidatesIntro')}
            </p>
            {#each sourcesResponse?.candidates ?? [] as cand (cand.path)}
              <div class="space-y-2">
                <!-- Candidate card -->
                <div
                  class="flex items-start justify-between gap-4 p-4 rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-100)]"
                >
                  <div class="min-w-0 flex-1">
                    <!-- Kind label -->
                    <p class="text-xs text-[var(--color-base-content)]/50 mb-0.5">
                      {kindLabel(cand)}
                    </p>
                    <p class="font-mono text-sm text-[var(--color-base-content)] break-all">
                      {cand.path}
                    </p>
                    {#if cand.detection_count > 0}
                      <p class="text-xs text-[var(--color-base-content)]/60 mt-1">
                        {t('system.importExport.source.detectionsSummary', {
                          count: String(formatNumber(cand.detection_count)),
                          date: cand.latest_date,
                        })}
                      </p>
                    {/if}
                    {#if isUnreadable(cand)}
                      <p class="text-xs font-medium text-[var(--color-warning)] mt-1">
                        {t('system.importExport.source.unreadableTitle')}
                      </p>
                      <p class="text-xs text-[var(--color-base-content)]/60 mt-0.5">
                        {t('system.importExport.source.unreadableOwner', {
                          owner: cand.owner_name,
                        })}
                      </p>
                    {/if}
                  </div>
                  {#if isUnreadable(cand)}
                    {#if sourcesResponse?.containerized}
                      <!-- B3: container unreadable - no elevation button, show host hint -->
                      <p
                        class="text-xs text-[var(--color-base-content)]/70 max-w-[14rem] text-right"
                        aria-live="polite"
                      >
                        {t('system.importExport.source.containerUnreadableHint', {
                          uid: String(sourcesResponse.run_as_uid),
                        })}
                      </p>
                    {:else}
                      <!-- B3: native unreadable - show elevation entry button -->
                      <Button variant="default" onclick={() => selectCandidate(cand)}>
                        {t('system.importExport.source.useThisButton')}
                      </Button>
                    {/if}
                  {:else}
                    <Button variant="primary" onclick={() => selectCandidate(cand)}>
                      {t('system.importExport.source.selectButton')}
                    </Button>
                  {/if}
                </div>
              </div>
            {/each}
            <button
              type="button"
              class="text-sm text-[var(--color-primary)] hover:underline"
              onclick={() => (showManualEntry = !showManualEntry)}
            >
              {t('system.importExport.source.manualEntryLink')}
            </button>
            {#if showManualEntry}
              {@render manualEntryForm()}
            {/if}
          </div>
        {:else if sourceStepState === 'zero-candidates'}
          <!-- No candidates found -->
          <div class="space-y-4">
            <div class="text-center py-2">
              <p class="font-medium text-[var(--color-base-content)]">
                {t('system.importExport.source.zeroTitle')}
              </p>
              <p class="text-sm text-[var(--color-base-content)]/70 mt-1">
                {t('system.importExport.source.zeroDescription')}
              </p>
            </div>
            {#if sourcesResponse?.guidance?.steps && sourcesResponse.guidance.steps.length > 0}
              <ol class="space-y-2">
                {#each sourcesResponse.guidance.steps as step, i (i)}
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
            {/if}
            <Button
              variant="default"
              onclick={recheckSources}
              disabled={isLoading}
              title={isLoading ? t('system.importExport.loading') : undefined}
            >
              {#if isLoading}
                <LoadingSpinner size="xs" aria-hidden="true" />
              {/if}
              {t('system.importExport.source.checkAgainButton')}
            </Button>
            <button
              type="button"
              class="text-sm text-[var(--color-primary)] hover:underline"
              onclick={() => (showManualEntry = !showManualEntry)}
            >
              {t('system.importExport.source.manualEntryLink')}
            </button>
            {#if showManualEntry}
              {@render manualEntryForm()}
            {/if}
          </div>
        {:else}
          <!-- sourcesResponse not yet available -->
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
            <input
              type="radio"
              name="import-mode"
              value="db-only"
              checked={selectedMode === 'db-only'}
              onchange={() => (selectedMode = 'db-only')}
              class="mt-1"
            />
            <div>
              <span class="font-medium text-[var(--color-base-content)]"
                >{t('system.importExport.mode.dbOnly.label')}</span
              >
              <p class="text-sm text-[var(--color-base-content)]/70 mt-0.5">
                {t('system.importExport.mode.dbOnly.description')}
              </p>
            </div>
          </label>

          <!-- db-audio option -->
          <label
            class="flex items-start gap-3 p-4 rounded-lg border border-[var(--color-base-300)] cursor-pointer hover:border-[var(--color-primary)] hover:bg-[color-mix(in_srgb,var(--color-primary)_4%,transparent)] transition-colors"
          >
            <input
              type="radio"
              name="import-mode"
              value="db-audio"
              checked={selectedMode === 'db-audio'}
              onchange={() => (selectedMode = 'db-audio')}
              class="mt-1"
            />
            <div>
              <div class="flex items-center gap-2">
                <span class="font-medium text-[var(--color-base-content)]"
                  >{t('system.importExport.mode.dbAudio.label')}</span
                >
              </div>
              <p class="text-sm text-[var(--color-base-content)]/70 mt-0.5">
                {t('system.importExport.mode.dbAudio.description')}
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
                {selectedMode === 'db-audio'
                  ? t('system.importExport.mode.dbAudio.label')
                  : t('system.importExport.mode.dbOnly.label')}
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

          <div role="alert" aria-live="assertive" aria-atomic="true">
            {#if errorMessage}
              <ErrorAlert message={errorMessage} type="error" />
            {/if}
          </div>

          <!-- Elevation sub-panel (only when the selected source needs permission elevation) -->
          {#if needsElevation}
            <div
              class="p-4 rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] space-y-3"
            >
              {#if elevating}
                <!-- C18: role="status" so the loading announcement reaches screen readers -->
                <div role="status" class="flex items-center gap-2">
                  <LoadingSpinner size="sm" aria-hidden="true" />
                  <span class="text-sm text-[var(--color-base-content)]">
                    {t('system.importExport.source.elevation.copying')}
                  </span>
                </div>
              {:else if showPasswordPanel}
                {#if isPlainHttp}
                  <div
                    class="p-3 rounded-lg bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)] text-sm text-[var(--color-base-content)]/80"
                  >
                    {t('system.importExport.source.elevation.httpWarning')}
                  </div>
                {/if}
                <p class="font-medium text-[var(--color-base-content)]">
                  {t('system.importExport.source.elevation.passwordTitle')}
                </p>
                <p class="text-sm text-[var(--color-base-content)]/70">
                  {t('system.importExport.source.elevation.passwordDescription')}
                </p>
                <div>
                  <label
                    for="elevation-password"
                    class="block text-sm font-medium text-[var(--color-base-content)]/70 mb-1"
                  >
                    {t('system.importExport.source.elevation.passwordLabel')}
                  </label>
                  <input
                    id="elevation-password"
                    type="password"
                    bind:value={sudoPassword}
                    class="block w-full px-3 py-2 rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-100)] text-sm text-[var(--color-base-content)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]/30"
                    autocomplete="current-password"
                  />
                </div>
                {#if elevationError}
                  <ErrorAlert message={elevationError} type="error" />
                {/if}
                <!-- C17: use passwordRequiredReason instead of the wrong pathRequiredReason key -->
                <Button
                  variant="primary"
                  onclick={() => elevate(sourcePath, true)}
                  disabled={!sudoPassword.trim() || elevating}
                  title={!sudoPassword.trim()
                    ? t('system.importExport.source.elevation.passwordRequiredReason')
                    : undefined}
                >
                  {t('system.importExport.source.elevation.submitButton')}
                </Button>
              {:else if fallbackCommands.length > 0}
                <p class="font-medium text-[var(--color-base-content)]">
                  {t('system.importExport.source.elevation.fallbackTitle')}
                </p>
                <p class="text-sm text-[var(--color-base-content)]/70">
                  {t('system.importExport.source.elevation.fallbackDescription')}
                </p>
                <p class="text-sm text-[var(--color-base-content)]/60 italic">
                  {t('system.importExport.source.elevation.disabledNote')}
                </p>
                <!-- C19: showCommandsLabel as a <details> collapsible around fallback commands -->
                <details class="space-y-2">
                  <summary
                    class="cursor-pointer text-sm font-medium text-[var(--color-primary)] hover:underline"
                  >
                    {t('system.importExport.source.showCommandsLabel')}
                  </summary>
                  <ol class="space-y-2 mt-2">
                    {#each fallbackCommands as cmd (cmd)}
                      <li class="flex items-start gap-2">
                        <code
                          class="text-xs bg-[var(--color-base-300)] px-2 py-1 rounded text-[var(--color-base-content)] font-mono break-all select-all"
                          >{cmd}</code
                        >
                      </li>
                    {/each}
                  </ol>
                </details>
                <Button variant="default" onclick={recheckSources}>
                  {t('system.importExport.source.checkAgainButton')}
                </Button>
              {:else if elevationError}
                <ErrorAlert message={elevationError} type="error" />
              {/if}
            </div>
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
              <ImportCountTile
                value={importProgress.processed}
                label={t('system.importExport.progress.processed')}
                size="md"
              />
              <ImportCountTile
                value={importProgress.inserted}
                label={t('system.importExport.progress.inserted')}
                tone="success"
                size="md"
              />
              <ImportCountTile
                value={importProgress.skipped}
                label={t('system.importExport.progress.skipped')}
                tone="muted"
                size="md"
              />
              <ImportCountTile
                value={importProgress.errors}
                label={t('system.importExport.progress.errors')}
                tone={importProgress.errors > 0 ? 'error' : 'muted'}
                size="md"
              />
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
                <ImportCountTile
                  value={importProgress.inserted}
                  label={t('system.importExport.progress.inserted')}
                  tone="success"
                  size="lg"
                />
                <ImportCountTile
                  value={importProgress.skipped}
                  label={t('system.importExport.progress.skipped')}
                  tone="muted"
                  size="lg"
                />
                <ImportCountTile
                  value={importProgress.errors}
                  label={t('system.importExport.progress.errors')}
                  tone={importProgress.errors > 0 ? 'error' : 'muted'}
                  size="lg"
                />
              </div>
              <div class="mt-2">
                <a
                  href={buildDetectionsFilterUrl()}
                  onclick={() => onClose()}
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
          <Button
            variant="ghost"
            onclick={() => {
              // Reset elevation sub-state when going back (user may change mode and retry)
              showPasswordPanel = false;
              fallbackCommands = [];
              elevationError = null;
              goToStep('mode');
            }}
          >
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
          <!-- Candidate selection advances via the Select button; just offer Close here -->
          <Button variant="default" onclick={onClose}>
            {t('common.buttons.close')}
          </Button>
        {:else if currentStep === 'mode'}
          <Button variant="primary" onclick={() => goToStep('confirm')}>
            {t('common.buttons.next')}
            <ArrowRight class="size-4" />
          </Button>
        {:else if currentStep === 'confirm'}
          <Button variant="default" onclick={onClose}>
            {t('common.buttons.cancel')}
          </Button>
          <!-- Hide the start button when the password panel or fallback panel is active -->
          {#if !(needsElevation && (showPasswordPanel || fallbackCommands.length > 0))}
            <Button
              variant="primary"
              onclick={() => {
                if (needsElevation) {
                  void elevate(sourcePath, false);
                } else {
                  void startImport();
                }
              }}
              disabled={isLoading || elevating || !sourcePath.trim()}
              title={isLoading || elevating
                ? t('system.importExport.loading')
                : !sourcePath.trim()
                  ? t('system.importExport.sourceAccess.pathRequiredReason')
                  : undefined}
              aria-busy={isLoading || elevating}
            >
              {#if isLoading || elevating}
                <LoadingSpinner
                  size="xs"
                  color="text-[var(--color-primary-content)]"
                  aria-hidden="true"
                />
              {/if}
              {t('system.importExport.confirm.startButton')}
            </Button>
          {/if}
        {:else if currentStep === 'progress'}
          {#if !importComplete && !importCancelled && !importError}
            <Button
              variant="ghost"
              onclick={onClose}
              title={t('system.importExport.runInBackgroundTitle')}
            >
              {t('system.importExport.runInBackground')}
            </Button>
            <Button
              variant="error"
              onclick={cancelImport}
              disabled={isCancelling}
              title={isCancelling ? t('system.importExport.progress.cancellingLabel') : undefined}
            >
              {#if isCancelling}
                <LoadingSpinner size="xs" aria-hidden="true" />
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
          <Button variant="ghost" type="button" onclick={startAnotherImport}>
            {t('system.importExport.done.importAnother')}
          </Button>
          <Button variant="primary" onclick={onClose}>
            {t('common.buttons.close')}
          </Button>
        {/if}
      </div>
    </div>
  {/snippet}
</Modal>
