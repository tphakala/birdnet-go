<script lang="ts">
  import { onDestroy } from 'svelte';
  import { t } from '$lib/i18n';
  import { api, ApiError } from '$lib/utils/api';
  import { ReconnectingEventSource } from '$lib/utils/ReconnectingEventSource';
  import { loggers } from '$lib/utils/logger';
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import ProgressBar from '$lib/desktop/components/ui/ProgressBar.svelte';
  import { Activity, CheckCircle2, XCircle, CircleSlash } from '@lucide/svelte';
  import { formatNumber } from '$lib/utils/formatters';
  import { buildDetectionsFilterUrl } from '../utils';
  import type { ImportErrorEvent, ImportProgress, ImportStatusResponse } from '../types';

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
  let isLoading = $state(true);
  let loadError = $state<string | null>(null);

  let eventSource: ReconnectingEventSource | null = null;
  let connectedJobId: string | null = null;
  let destroyed = false;

  type ActivityView = 'loading' | 'loadError' | 'running' | 'done' | 'idle';

  let view = $derived.by((): ActivityView => {
    if (isLoading && status === null) return 'loading';
    if (loadError !== null && status === null) return 'loadError';
    if (finalKind !== null) return 'done';
    if (status?.running) return 'running';
    return 'idle';
  });

  let progressPercent = $derived.by(() => {
    if (
      !progress ||
      typeof progress.total !== 'number' ||
      typeof progress.processed !== 'number' ||
      progress.total <= 0
    ) {
      return 0;
    }
    return Math.max(0, Math.min(100, Math.round((progress.processed / progress.total) * 100)));
  });

  // Refetch on mount and whenever the parent bumps refreshSignal.
  $effect(() => {
    void refreshSignal;
    void loadStatus();
  });

  async function loadStatus() {
    loadError = null;
    try {
      const resp = await api.get<ImportStatusResponse>('/api/v2/import/status');
      if (destroyed) return;
      status = resp;
      if (resp.progress) {
        progress = resp.progress;
      }
      if (resp.running && resp.job_id) {
        // A new (or still-running) job: clear any previous terminal state.
        finalKind = null;
        connectEventSource(resp.job_id);
        return;
      }
      closeEventSource();
      if (resp.status === 'done') {
        // Keep the more precise SSE-derived terminal kind (e.g. 'cancelled')
        // for the job we watched; derive from the response otherwise.
        if (finalKind === null || resp.job_id !== connectedJobId) {
          finalKind = resp.error ? 'error' : 'success';
        }
      } else {
        finalKind = null;
        progress = null;
      }
    } catch (err) {
      if (destroyed) return;
      logger.error('Failed to load import status', err);
      loadError =
        err instanceof ApiError ? err.userMessage : t('system.importExport.errors.loadFailed');
    } finally {
      isLoading = false;
    }
  }

  function connectEventSource(id: string) {
    if (eventSource && connectedJobId === id) return;
    closeEventSource();
    connectedJobId = id;
    const es = new ReconnectingEventSource(`/api/v2/import/jobs/${id}/progress`);

    es.addEventListener('progress', (event: Event) => {
      try {
        progress = JSON.parse((event as MessageEvent).data) as ImportProgress;
      } catch (e) {
        logger.error('Failed to parse progress event', e);
      }
    });

    es.addEventListener('complete', (event: Event) => {
      try {
        progress = JSON.parse((event as MessageEvent).data) as ImportProgress;
      } catch (e) {
        logger.error('Failed to parse complete event', e);
      }
      finalKind = 'success';
      closeEventSource();
    });

    es.addEventListener('cancelled', (event: Event) => {
      try {
        progress = JSON.parse((event as MessageEvent).data) as ImportProgress;
      } catch {
        // ignore parse errors for cancelled event
      }
      finalKind = 'cancelled';
      closeEventSource();
    });

    es.addEventListener('error', (event: Event) => {
      // Transport drops fire 'error' without data; ReconnectingEventSource
      // handles those, so only treat data-carrying events as terminal.
      if (!(event instanceof MessageEvent) || typeof event.data !== 'string') {
        return;
      }
      try {
        const data = JSON.parse(event.data) as ImportErrorEvent;
        progress = {
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
      finalKind = 'error';
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

<Card className="h-full">
  <h3
    id="import-activity-heading"
    class="text-xs font-semibold uppercase tracking-wider text-[var(--color-base-content)]/60 mb-3"
  >
    {t('system.importExport.activity.title')}
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
          <div class="text-center p-2 rounded bg-[var(--color-base-200)]">
            <div class="text-base font-semibold text-[var(--color-base-content)]">
              {formatNumber(progress.processed)}
            </div>
            <div class="text-xs text-[var(--color-base-content)]/60">
              {t('system.importExport.progress.processed')}
            </div>
          </div>
          <div class="text-center p-2 rounded bg-[var(--color-base-200)]">
            <div class="text-base font-semibold text-[var(--color-success)]">
              {formatNumber(progress.inserted)}
            </div>
            <div class="text-xs text-[var(--color-base-content)]/60">
              {t('system.importExport.progress.inserted')}
            </div>
          </div>
          <div class="text-center p-2 rounded bg-[var(--color-base-200)]">
            <div class="text-base font-semibold text-[var(--color-base-content)]/60">
              {formatNumber(progress.skipped)}
            </div>
            <div class="text-xs text-[var(--color-base-content)]/60">
              {t('system.importExport.progress.skipped')}
            </div>
          </div>
          <div class="text-center p-2 rounded bg-[var(--color-base-200)]">
            <div
              class="text-base font-semibold {progress.errors > 0
                ? 'text-[var(--color-error)]'
                : 'text-[var(--color-base-content)]/60'}"
            >
              {formatNumber(progress.errors)}
            </div>
            <div class="text-xs text-[var(--color-base-content)]/60">
              {t('system.importExport.progress.errors')}
            </div>
          </div>
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
          <div class="text-center p-2 rounded bg-[var(--color-base-200)]">
            <div class="text-base font-semibold text-[var(--color-success)]">
              {formatNumber(progress.inserted)}
            </div>
            <div class="text-xs text-[var(--color-base-content)]/60">
              {t('system.importExport.progress.inserted')}
            </div>
          </div>
          <div class="text-center p-2 rounded bg-[var(--color-base-200)]">
            <div class="text-base font-semibold text-[var(--color-base-content)]/60">
              {formatNumber(progress.skipped)}
            </div>
            <div class="text-xs text-[var(--color-base-content)]/60">
              {t('system.importExport.progress.skipped')}
            </div>
          </div>
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
        {t('system.importExport.activity.emptyTitle')}
      </p>
      <p class="text-xs text-[var(--color-base-content)]/50 max-w-48">
        {t('system.importExport.activity.emptyDescription')}
      </p>
    </div>
  {/if}
</Card>
