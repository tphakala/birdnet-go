<script lang="ts">
  import { onDestroy } from 'svelte';
  import { t } from '$lib/i18n';
  import { api, ApiError } from '$lib/utils/api';
  import type { ReconnectingEventSource } from '$lib/utils/ReconnectingEventSource';
  import { loggers } from '$lib/utils/logger';
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import ProgressBar from '$lib/desktop/components/ui/ProgressBar.svelte';
  import { Activity, CheckCircle2, XCircle, CircleSlash } from '@lucide/svelte';
  import ImportCountTile from './ImportCountTile.svelte';
  import {
    buildDetectionsFilterUrl,
    connectImportProgressStream,
    importProgressPercent,
  } from '../utils';
  import type { ImportProgress, ImportStatusResponse } from '../types';

  const logger = loggers.ui;

  interface Props {
    /**
     * Bump to trigger a status refetch (e.g. after the import wizard closes,
     * so an import started or cancelled inside the wizard shows up here).
     */
    refreshSignal?: number;
    onOpenWizard: () => void;
  }

  let { refreshSignal = 0, onOpenWizard }: Props = $props();

  type FinalKind = 'success' | 'cancelled' | 'error';

  let status = $state<ImportStatusResponse | null>(null);
  let progress = $state<ImportProgress | null>(null);
  let finalKind = $state<FinalKind | null>(null);
  let loadError = $state<string | null>(null);

  let eventSource: ReconnectingEventSource | null = null;
  let connectedJobId: string | null = null;
  let destroyed = false;
  // Monotonic id so an out-of-order response cannot overwrite newer state
  // (e.g. a stale 'idle' snapshot closing a live stream).
  let loadGeneration = 0;

  type ActivityView = 'loading' | 'loadError' | 'running' | 'done' | 'idle';

  let view = $derived.by((): ActivityView => {
    if (finalKind !== null) return 'done';
    if (status?.running) return 'running';
    if (status === null) return loadError !== null ? 'loadError' : 'loading';
    return 'idle';
  });

  let progressPercent = $derived(importProgressPercent(progress));

  // Refetch on mount and whenever the parent bumps refreshSignal.
  $effect(() => {
    void refreshSignal;
    void loadStatus();
  });

  async function loadStatus() {
    const generation = ++loadGeneration;
    loadError = null;
    try {
      const resp = await api.get<ImportStatusResponse>('/api/v2/import/status');
      if (destroyed || generation !== loadGeneration) return;
      // A response snapshotted while the job still ran can arrive after the
      // SSE stream delivered the terminal event; a finished job id never runs
      // again, so ignore the stale 'running' snapshot.
      if (resp.running && resp.job_id === connectedJobId && finalKind !== null) {
        return;
      }
      status = resp;
      if (resp.progress) {
        progress = resp.progress;
      }
      if (resp.running && resp.job_id) {
        finalKind = null;
        connectEventSource(resp.job_id);
        return;
      }
      closeEventSource();
      if (resp.status === 'done') {
        // Keep the SSE-derived terminal kind for the job we watched; derive
        // from the response otherwise.
        if (finalKind === null || resp.job_id !== connectedJobId) {
          finalKind = resp.cancelled ? 'cancelled' : resp.error ? 'error' : 'success';
        }
      } else {
        finalKind = null;
        progress = null;
      }
    } catch (err) {
      if (destroyed || generation !== loadGeneration) return;
      logger.error('Failed to load import status', err);
      loadError =
        err instanceof ApiError && err.userMessage
          ? err.userMessage
          : t('system.importExport.errors.loadFailed');
    }
  }

  function connectEventSource(id: string) {
    if (eventSource && connectedJobId === id) return;
    closeEventSource();
    connectedJobId = id;
    eventSource = connectImportProgressStream(id, {
      onProgress: p => {
        progress = p;
      },
      onComplete: p => {
        if (p) progress = p;
        finalKind = 'success';
        closeEventSource();
      },
      onCancelled: p => {
        if (p) progress = p;
        finalKind = 'cancelled';
        closeEventSource();
      },
      onError: p => {
        if (p) progress = p;
        finalKind = 'error';
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

  onDestroy(() => {
    destroyed = true;
    closeEventSource();
  });

  function doneTitleKey(kind: FinalKind): string {
    switch (kind) {
      case 'success':
        return 'system.importExport.done.successTitle';
      case 'cancelled':
        return 'system.importExport.done.cancelledTitle';
      case 'error':
        return 'system.importExport.done.errorTitle';
    }
  }
</script>

<Card>
  <h3
    id="import-activity-heading"
    class="text-xs font-semibold uppercase tracking-wider text-[var(--color-base-content)]/60 mb-3"
  >
    {t('system.importExport.activity.sectionTitle')}
  </h3>

  {#if view === 'loading'}
    <div class="flex items-center gap-2 text-sm text-[var(--color-base-content)]/60">
      <LoadingSpinner size="sm" />
      {t('system.importExport.loading')}
    </div>
  {:else if view === 'loadError'}
    <ErrorAlert message={loadError ?? ''} type="error" />
  {:else if view === 'running'}
    <div class="space-y-4">
      <div class="flex items-center gap-2" role="status" aria-live="polite">
        <LoadingSpinner size="sm" />
        <span class="text-sm font-medium text-[var(--color-base-content)]">
          {t('system.importExport.progress.runningLabel')}
          {#if progress?.phase && progress.phase !== 'done'}
            <span class="text-[var(--color-base-content)]/60">
              - {t(`system.importExport.progress.phase.${progress.phase}`)}
            </span>
          {/if}
        </span>
      </div>
      <ProgressBar
        value={progressPercent}
        max={100}
        showLabel={true}
        striped={true}
        animated={true}
      />
      {#if progress}
        <div class="grid grid-cols-2 gap-2">
          <ImportCountTile
            value={progress.processed}
            label={t('system.importExport.progress.processed')}
          />
          <ImportCountTile
            value={progress.inserted}
            label={t('system.importExport.progress.inserted')}
            tone="success"
          />
          <ImportCountTile
            value={progress.skipped}
            label={t('system.importExport.progress.skipped')}
            tone="muted"
          />
          <ImportCountTile
            value={progress.errors}
            label={t('system.importExport.progress.errors')}
            tone={progress.errors > 0 ? 'error' : 'muted'}
          />
        </div>
      {/if}
      <Button variant="default" size="sm" onclick={onOpenWizard}>
        {t('system.importExport.activity.openWizard')}
      </Button>
    </div>
  {:else if view === 'done' && finalKind !== null}
    <div class="space-y-4">
      <div class="flex items-center gap-2">
        {#if finalKind === 'success'}
          <CheckCircle2 class="size-5 text-[var(--color-success)]" aria-hidden="true" />
        {:else if finalKind === 'cancelled'}
          <CircleSlash class="size-5 text-[var(--color-base-content)]/50" aria-hidden="true" />
        {:else}
          <XCircle class="size-5 text-[var(--color-error)]" aria-hidden="true" />
        {/if}
        <span class="text-sm font-medium text-[var(--color-base-content)]">
          {t(doneTitleKey(finalKind))}
        </span>
      </div>
      {#if progress}
        <div class="grid grid-cols-2 gap-2">
          <ImportCountTile
            value={progress.inserted}
            label={t('system.importExport.progress.inserted')}
            tone="success"
          />
          <ImportCountTile
            value={progress.skipped}
            label={t('system.importExport.progress.skipped')}
            tone="muted"
          />
        </div>
      {/if}
      {#if finalKind === 'success' && (progress?.inserted ?? 0) > 0}
        <a
          href={buildDetectionsFilterUrl()}
          class="inline-flex items-center gap-1 text-sm font-medium underline text-[var(--color-primary)] hover:opacity-80"
        >
          {t('system.importExport.done.viewDetectionsLink')}
        </a>
      {/if}
    </div>
  {:else}
    <div class="flex flex-col items-center text-center py-6 gap-2">
      <Activity class="size-8 text-[var(--color-base-content)]/25" aria-hidden="true" />
      <p class="text-sm font-medium text-[var(--color-base-content)]/70">
        {t('system.importExport.activity.empty.title')}
      </p>
      <p class="text-xs text-[var(--color-base-content)]/50 max-w-48">
        {t('system.importExport.activity.empty.description')}
      </p>
    </div>
  {/if}
</Card>
