<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { api, ApiError } from '$lib/utils/api';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { ReconnectingEventSource } from '$lib/utils/ReconnectingEventSource';
  import { connectionState } from '$lib/stores/connectionState.svelte';
  import type { DatabaseOverviewResponse } from '$lib/types/database';
  import type { MigrationStatus, ApiState, PrerequisitesResponse } from '$lib/types/migration';
  import { failedRunMessage } from '$lib/utils/migrationPrerequisites';
  import type { LegacyStatus } from '$lib/types/legacy';

  // Sub-components
  import DatabaseMetricStrip from '$lib/desktop/features/system/components/DatabaseMetricStrip.svelte';
  import DatabaseSqliteDetails from '$lib/desktop/features/system/components/DatabaseSqliteDetails.svelte';
  import DatabaseLocksWalCard from '$lib/desktop/features/system/components/DatabaseLocksWalCard.svelte';
  import DatabaseMysqlConnectionPool from '$lib/desktop/features/system/components/DatabaseMysqlConnectionPool.svelte';
  import DatabaseMysqlInnodbCard from '$lib/desktop/features/system/components/DatabaseMysqlInnodbCard.svelte';
  import DatabaseDetectionRateCard from '$lib/desktop/features/system/components/DatabaseDetectionRateCard.svelte';
  import DatabaseTableBreakdown from '$lib/desktop/features/system/components/DatabaseTableBreakdown.svelte';

  // Migration view components
  import DatabaseMigrationMetricStrip from '$lib/desktop/features/system/components/DatabaseMigrationMetricStrip.svelte';
  import DatabaseMigrationProgress from '$lib/desktop/features/system/components/DatabaseMigrationProgress.svelte';
  import DatabaseMigrationDetails from '$lib/desktop/features/system/components/DatabaseMigrationDetails.svelte';
  import DatabasePrerequisites from '$lib/desktop/features/system/components/DatabasePrerequisites.svelte';
  import DatabaseBackupHistory from '$lib/desktop/features/system/components/DatabaseBackupHistory.svelte';

  // Existing migration components (reused for dialogs & cleanup)
  import MigrationConfirmDialog from './MigrationConfirmDialog.svelte';
  import LegacyCleanupCard from './LegacyCleanupCard.svelte';
  import LegacyCleanupConfirmDialog from './LegacyCleanupConfirmDialog.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';

  import type { DatabaseStats } from '$lib/types/migration';

  const logger = loggers.ui;

  const MAX_HISTORY_POINTS = 60;
  const OVERVIEW_REFRESH_INTERVAL_MS = 30_000;
  const STATUS_POLL_INTERVAL_MS = 2000;
  const PREREQUISITES_POLL_INTERVAL_MS = 2000;
  /** Error key of a start refused because no usable prerequisites run exists. */
  const PREREQUISITES_NOT_READY_KEY = 'errors.migration.prerequisitesNotReady';
  /** Error key of a start refused because a check failed when it was re-run. */
  const PREFLIGHT_FAILED_KEY = 'errors.migration.preFlightFailed';

  // --- Overview state ---
  let overview = $state<DatabaseOverviewResponse | null>(null);
  let overviewError = $state<string | null>(null);
  let overviewLoading = $state(true);

  // --- Sparkline history ---
  let readLatencyHistory = $state<number[]>([]);
  let writeLatencyHistory = $state<number[]>([]);
  let queriesPerSecHistory = $state<number[]>([]);

  // --- Migration state (reused from DatabaseManagement) ---
  /** A start refusal rests on the run it was based on; undefined ids mean there was none. */
  interface ActionRefusal {
    runId: number | undefined;
    startedAt: string | undefined;
  }
  let migrationStatus = $state<ApiState<MigrationStatus>>({
    loading: true,
    error: null,
    data: null,
  });
  // Failure of the last migration action. Kept apart from migrationStatus.error because
  // status polling clears that on every successful fetch, which would wipe an action
  // failure before it can be read.
  let actionError = $state<{ message: string; refusal?: ActionRefusal } | null>(null);
  let legacyStats = $state<ApiState<DatabaseStats>>({ loading: true, error: null, data: null });
  let v2Stats = $state<ApiState<DatabaseStats>>({ loading: true, error: null, data: null });
  let prerequisites = $state<ApiState<PrerequisitesResponse>>({
    loading: true,
    error: null,
    data: null,
  });
  let legacyStatus = $state<ApiState<LegacyStatus>>({ loading: false, error: null, data: null });
  let showConfirmDialog = $state(false);
  let startLoading = $state(false);
  let showCleanupDialog = $state(false);
  let cleanupLoading = $state(false);

  // --- Derived state ---
  let isV2OnlyMode = $derived(migrationStatus.data?.is_v2_only_mode === true);
  const ACTIVE_MIGRATION_STATES = [
    'initializing',
    'dual_write',
    'migrating',
    'migrating_predictions',
    'validating',
    'cutover',
  ];
  let isActive = $derived(ACTIVE_MIGRATION_STATES.includes(migrationStatus.data?.state ?? ''));
  // No status has loaded and the last attempt failed
  let statusUnknown = $derived(migrationStatus.data === null && migrationStatus.error !== null);
  let prerequisitesRunning = $derived(prerequisites.data?.status === 'running');
  /** True until the first snapshot arrives and while a run is in progress. */
  let prerequisitesBusy = $derived(prerequisites.loading || prerequisitesRunning);
  /** The request error, or the message of a run that did not finish. */
  let prerequisitesError = $derived(prerequisites.error ?? failedRunMessage(prerequisites.data));
  let isCleanupActive = $derived(migrationStatus.data?.cleanup_state === 'in_progress');

  // SSE connection
  let metricsSSE: ReconnectingEventSource | null = null;

  // --- Helper ---
  function appendHistory(arr: number[], value: number): number[] {
    const next = [...arr, value];
    return next.length > MAX_HISTORY_POINTS ? next.slice(next.length - MAX_HISTORY_POINTS) : next;
  }

  // --- Fetch functions ---
  async function fetchOverview(): Promise<void> {
    if (!connectionState.isOnline) {
      if (!overview) overviewLoading = false;
      return;
    }
    try {
      overview = await api.get<DatabaseOverviewResponse>('/api/v2/system/database/overview');
      overviewError = null;
    } catch (e) {
      overviewError =
        e instanceof ApiError ? e.message : t('system.database.dashboard.fetchFailed');
      logger.debug('Failed to fetch database overview', {
        error: e instanceof Error ? e.message : 'Unknown',
      });
    } finally {
      overviewLoading = false;
    }
  }

  /**
   * Counts the status requests. A response that arrives after a newer request was sent describes
   * an older state; applying it could fake a change of migration state, which reruns the checks.
   */
  let migrationStatusSeq = 0;

  async function fetchMigrationStatus(): Promise<void> {
    const seq = ++migrationStatusSeq;
    try {
      const status = await api.get<MigrationStatus>('/api/v2/system/database/migration/status');
      if (seq !== migrationStatusSeq) return;
      migrationStatus.data = status;
      migrationStatus.error = null;
    } catch (e) {
      if (seq !== migrationStatusSeq) return;
      migrationStatus.error =
        e instanceof ApiError ? e.message : t('system.database.migration.errors.fetchFailed');
    } finally {
      // A newer request is still in flight and owns the loading flag
      if (seq === migrationStatusSeq) migrationStatus.loading = false;
    }
  }

  // Retry after a failed first status fetch. Marks the button busy and ignores clicks
  // meanwhile, so a slow or still-failing server does not collect stacked requests.
  let statusRetrying = $state(false);

  // Wrapper around the alerts and views; see the template
  let viewAnchor = $state<HTMLDivElement>();

  async function retryMigrationStatus(): Promise<void> {
    if (statusRetrying) return;
    statusRetrying = true;
    try {
      await fetchMigrationStatus();
    } finally {
      statusRetrying = false;
    }
    if (migrationStatus.error) return;
    // Success removes the alert and with it the focused Retry button, which would
    // drop focus to the document body
    await tick();
    if (!document.activeElement || document.activeElement === document.body) {
      viewAnchor?.focus();
    }
  }

  async function fetchLegacyStats(): Promise<void> {
    try {
      legacyStats.data = await api.get<DatabaseStats>('/api/v2/system/database/stats');
      legacyStats.error = null;
    } catch (e) {
      legacyStats.error =
        e instanceof ApiError ? e.message : t('system.database.stats.fetchFailed');
    } finally {
      legacyStats.loading = false;
    }
  }

  async function fetchV2Stats(): Promise<void> {
    try {
      v2Stats.data = await api.get<DatabaseStats>('/api/v2/system/database/v2/stats');
      v2Stats.error = null;
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        v2Stats.data = null;
        v2Stats.error = null;
      } else {
        v2Stats.error = e instanceof ApiError ? e.message : t('system.database.stats.fetchFailed');
      }
    } finally {
      v2Stats.loading = false;
    }
  }

  /** Fetch errors keep the last snapshot; the message is shown next to it. */
  function setPrerequisitesError(e: unknown): void {
    prerequisites.error =
      e instanceof ApiError
        ? e.message
        : t('system.database.migration.prerequisites.errors.fetchFailed');
  }

  /**
   * Counts the run requests. A poll or an older run request that finishes after a newer run
   * request was sent describes an older state, so it must not overwrite the newer snapshot.
   */
  let prerequisitesRequestSeq = 0;

  /**
   * Starts the background prerequisites run, or joins the one in progress, and stores the
   * snapshot. A forced run replaces a fresh passing result. The checks themselves are
   * followed by the polling effect below. Of overlapping requests the latest one wins.
   */
  async function runPrerequisites(force: boolean): Promise<void> {
    const seq = ++prerequisitesRequestSeq;
    try {
      // Loading covers every wait for the start response, so an older snapshot, even a
      // passing one, cannot enable Start while a new run is being requested
      prerequisites.loading = true;
      const snapshot = await api.post<PrerequisitesResponse>(
        '/api/v2/system/database/migration/prerequisites/run',
        { force }
      );
      if (seq !== prerequisitesRequestSeq) return;
      prerequisites.data = snapshot;
      prerequisites.error = null;
    } catch (e) {
      if (seq === prerequisitesRequestSeq) setPrerequisitesError(e);
    } finally {
      // A newer request is still in flight and owns the loading flag
      if (seq === prerequisitesRequestSeq) prerequisites.loading = false;
    }
  }

  async function pollPrerequisites(): Promise<void> {
    // A run request in flight owns the snapshot; a poll started now could not be ordered after it
    if (prerequisites.loading) return;
    const seq = prerequisitesRequestSeq;
    try {
      const snapshot = await api.get<PrerequisitesResponse>(
        '/api/v2/system/database/migration/prerequisites'
      );
      // A run request was sent while this poll was in flight: its answer is newer
      if (seq !== prerequisitesRequestSeq) return;
      prerequisites.data = snapshot;
      prerequisites.error = null;
      // The server lost the run (a restart), so nothing is left to follow: start it again
      if (snapshot.status === 'idle') await runPrerequisites(false);
    } catch (e) {
      // The run goes on server-side, so keep polling
      if (seq === prerequisitesRequestSeq) setPrerequisitesError(e);
    }
  }

  async function fetchLegacyStatus(): Promise<void> {
    try {
      legacyStatus.loading = true;
      legacyStatus.data = await api.get<LegacyStatus>('/api/v2/system/database/legacy/status');
      legacyStatus.error = null;
    } catch (e) {
      legacyStatus.error =
        e instanceof ApiError ? e.message : t('system.database.legacy.fetchFailed');
    } finally {
      legacyStatus.loading = false;
    }
  }

  /** Shows a failed migration action: the server message, or the fallback when there is none. */
  function failAction(e: unknown, fallbackKey: string, refusal?: ActionRefusal): void {
    actionError = { message: e instanceof ApiError ? e.message : t(fallbackKey), refusal };
  }

  // --- Migration actions (same as DatabaseManagement) ---
  async function startMigration(): Promise<void> {
    showConfirmDialog = false;
    startLoading = true;
    actionError = null;
    try {
      if (legacyStats.data?.total_detections != null) {
        await api.post('/api/v2/system/database/migration/start', {
          total_records: legacyStats.data.total_detections,
        });
      } else {
        await api.post('/api/v2/system/database/migration/start');
      }
      await fetchMigrationStatus();
    } catch (e) {
      const errorKey = e instanceof ApiError ? e.errorKey : undefined;
      const checkRefused = errorKey === PREFLIGHT_FAILED_KEY;
      const rerun = checkRefused || errorKey === PREREQUISITES_NOT_READY_KEY;
      // Only a refusal that rests on the checks goes away when a newer run passes
      failAction(
        e,
        'system.database.migration.errors.startFailed',
        rerun
          ? { runId: prerequisites.data?.run_id, startedAt: prerequisites.data?.started_at }
          : undefined
      );
      // A migration that started anyway must show, and a missing or stale run is redone.
      // A refused check forces a new run so the panel shows it instead of the old pass.
      await fetchMigrationStatus();
      if (rerun) await runPrerequisites(checkRefused);
    } finally {
      startLoading = false;
    }
  }

  async function pauseMigration(): Promise<void> {
    actionError = null;
    try {
      await api.post('/api/v2/system/database/migration/pause');
      await fetchMigrationStatus();
    } catch (e) {
      failAction(e, 'system.database.migration.errors.pauseFailed');
    }
  }

  async function resumeMigration(): Promise<void> {
    actionError = null;
    try {
      await api.post('/api/v2/system/database/migration/resume');
      await fetchMigrationStatus();
    } catch (e) {
      failAction(e, 'system.database.migration.errors.resumeFailed');
    }
  }

  async function retryValidation(): Promise<void> {
    actionError = null;
    try {
      await api.post('/api/v2/system/database/migration/retry-validation');
      await fetchMigrationStatus();
    } catch (e) {
      failAction(e, 'system.database.migration.errors.retryValidationFailed');
    }
  }

  async function cancelMigration(): Promise<void> {
    actionError = null;
    try {
      await api.post('/api/v2/system/database/migration/cancel');
      await fetchMigrationStatus();
    } catch (e) {
      failAction(e, 'system.database.migration.errors.cancelFailed');
    }
  }

  async function startCleanup(): Promise<void> {
    showCleanupDialog = false;
    cleanupLoading = true;
    try {
      await api.post('/api/v2/system/database/legacy/cleanup');
      await Promise.all([fetchMigrationStatus(), fetchLegacyStatus()]);
    } catch (e) {
      legacyStatus.error =
        e instanceof ApiError ? e.message : t('system.database.legacy.cleanup.failed');
    } finally {
      cleanupLoading = false;
    }
  }

  // --- Backup job state ---
  interface BackupJob {
    job_id: string;
    db_type: string;
    status: string;
    progress: number;
    bytes_written: number;
    total_bytes: number;
    started_at: string;
    completed_at?: string;
    error?: string;
    download_url?: string;
  }

  let backupJobs = $state<BackupJob[]>([]);
  let backupCreating = $state(false);

  async function fetchBackupJobs(): Promise<void> {
    try {
      const resp = await api.get<{ jobs: BackupJob[] }>('/api/v2/system/database/backup/jobs');
      backupJobs = resp.jobs ?? [];
    } catch {
      logger.debug('Failed to fetch backup jobs');
    }
  }

  async function createBackup(): Promise<void> {
    backupCreating = true;
    try {
      await api.post('/api/v2/system/database/backup/jobs?type=legacy');
      await fetchBackupJobs();
    } catch (e) {
      logger.debug('Failed to create backup', {
        error: e instanceof Error ? e.message : 'Unknown',
      });
    } finally {
      backupCreating = false;
    }
  }

  // --- Metrics SSE ---
  interface MetricPoint {
    timestamp: string;
    value: number;
  }

  interface MetricsHistoryResponse {
    metrics: Record<string, MetricPoint[]>;
  }

  async function loadMetricsHistory(active: { current: boolean }): Promise<void> {
    try {
      const data = await api.get<MetricsHistoryResponse>(
        `/api/v2/system/metrics/history?points=${MAX_HISTORY_POINTS}&metrics=db.read_latency_ms,db.write_latency_ms,db.queries_per_sec`
      );
      if (!active.current) return;

      if (data.metrics['db.read_latency_ms']) {
        readLatencyHistory = data.metrics['db.read_latency_ms'].map(p => p.value);
      }
      if (data.metrics['db.write_latency_ms']) {
        writeLatencyHistory = data.metrics['db.write_latency_ms'].map(p => p.value);
      }
      if (data.metrics['db.queries_per_sec']) {
        queriesPerSecHistory = data.metrics['db.queries_per_sec'].map(p => p.value);
      }
    } catch {
      logger.debug('Database metrics history not available');
    } finally {
      // Always connect SSE for live updates, even if history backfill failed
      if (active.current) {
        connectMetricsStream();
      }
    }
  }

  function connectMetricsStream(): void {
    disconnectMetricsStream();
    metricsSSE = new ReconnectingEventSource(
      '/api/v2/system/metrics/stream?metrics=db.read_latency_ms,db.write_latency_ms,db.queries_per_sec',
      { max_retry_time: 30000 }
    );

    metricsSSE.addEventListener('metrics', (event: Event) => {
      try {
        const messageEvent = event as MessageEvent;
        const metrics = JSON.parse(messageEvent.data) as Record<string, MetricPoint>;

        if (metrics['db.read_latency_ms']) {
          readLatencyHistory = appendHistory(
            readLatencyHistory,
            metrics['db.read_latency_ms'].value
          );
        }
        if (metrics['db.write_latency_ms']) {
          writeLatencyHistory = appendHistory(
            writeLatencyHistory,
            metrics['db.write_latency_ms'].value
          );
        }
        if (metrics['db.queries_per_sec']) {
          queriesPerSecHistory = appendHistory(
            queriesPerSecHistory,
            metrics['db.queries_per_sec'].value
          );
        }
      } catch {
        logger.debug('Failed to parse database metrics SSE event');
      }
    });
  }

  function disconnectMetricsStream(): void {
    if (metricsSSE) {
      metricsSSE.close();
      metricsSSE = null;
    }
  }

  // --- Initialization ---
  $effect(() => {
    const active = { current: true };

    // Fetch all data in parallel
    Promise.all([
      fetchOverview(),
      fetchMigrationStatus(),
      fetchLegacyStats(),
      fetchV2Stats(),
      runPrerequisites(false),
      fetchBackupJobs(),
    ]).then(() => {
      if (active.current) {
        loadMetricsHistory(active);
      }
    });

    // Periodic overview refresh (for slow-changing data like table stats, detection rate)
    const overviewInterval = setInterval(fetchOverview, OVERVIEW_REFRESH_INTERVAL_MS);

    return () => {
      active.current = false;
      disconnectMetricsStream();
      clearInterval(overviewInterval);
    };
  });

  // Poll during active migration (recursive setTimeout to prevent pile-up)
  $effect(() => {
    if (!isActive) return;
    let cancelled = false;
    let timeoutId: ReturnType<typeof setTimeout> | null = null;
    async function poll() {
      if (cancelled) return;

      if (!connectionState.isOnline) {
        // Skip API calls but keep the polling loop alive
        if (!cancelled) {
          timeoutId = setTimeout(poll, STATUS_POLL_INTERVAL_MS);
        }
        return;
      }

      await Promise.all([fetchMigrationStatus(), fetchV2Stats()]);
      if (!cancelled) {
        timeoutId = setTimeout(poll, STATUS_POLL_INTERVAL_MS);
      }
    }
    timeoutId = setTimeout(poll, STATUS_POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      if (timeoutId) clearTimeout(timeoutId);
    };
  });

  // The stored checks describe the state they ran in: when the migration returns to idle
  // (cancel from any state, rollback), check again. Not keyed on isActive, which misses
  // paused, failed and completed.
  let previousMigrationState: string | undefined;
  $effect(() => {
    const state = migrationStatus.data?.state;
    if (
      state === 'idle' &&
      previousMigrationState !== undefined &&
      previousMigrationState !== 'idle'
    ) {
      untrack(() => void runPrerequisites(false));
    }
    previousMigrationState = state;
  });

  // A start refusal says what was wrong at that time: drop it when a newer run completes and
  // allows the start. It stays while the forced re-run is still running or still failing.
  $effect(() => {
    const data = prerequisites.data;
    const refusal = actionError?.refusal;
    if (!refusal) return;
    // Run ids restart with the server, so the start time tells a new run from the refused one
    const isNewRun = data?.run_id !== refusal.runId || data?.started_at !== refusal.startedAt;
    if (data?.status === 'completed' && data.can_start_migration && isNewRun) {
      actionError = null;
    }
  });

  // Follow a running prerequisites run (recursive setTimeout to prevent pile-up)
  $effect(() => {
    if (!prerequisitesRunning) return;
    let cancelled = false;
    let timeoutId: ReturnType<typeof setTimeout> | null = null;
    async function poll() {
      if (cancelled) return;

      if (connectionState.isOnline) {
        await pollPrerequisites();
      }
      if (!cancelled) {
        timeoutId = setTimeout(poll, PREREQUISITES_POLL_INTERVAL_MS);
      }
    }
    timeoutId = setTimeout(poll, PREREQUISITES_POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      if (timeoutId) clearTimeout(timeoutId);
    };
  });

  // Poll during active backup jobs (recursive setTimeout to prevent pile-up)
  const ACTIVE_BACKUP_STATUSES = new Set(['pending', 'in_progress']);
  let hasActiveBackup = $derived(backupJobs.some(j => ACTIVE_BACKUP_STATUSES.has(j.status)));

  $effect(() => {
    if (!hasActiveBackup) return;
    let cancelled = false;
    let timeoutId: ReturnType<typeof setTimeout> | null = null;
    async function poll() {
      if (cancelled) return;

      if (!connectionState.isOnline) {
        // Skip API call but keep the polling loop alive
        if (!cancelled) {
          timeoutId = setTimeout(poll, STATUS_POLL_INTERVAL_MS);
        }
        return;
      }

      await fetchBackupJobs();
      if (!cancelled) {
        timeoutId = setTimeout(poll, STATUS_POLL_INTERVAL_MS);
      }
    }
    timeoutId = setTimeout(poll, STATUS_POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      if (timeoutId) clearTimeout(timeoutId);
    };
  });

  // Fetch legacy status in v2-only mode
  $effect(() => {
    if (isV2OnlyMode && !legacyStatus.data && !legacyStatus.loading && !legacyStatus.error) {
      fetchLegacyStatus();
    }
  });

  // Poll during cleanup (recursive setTimeout to prevent pile-up)
  $effect(() => {
    if (!isCleanupActive) return;
    let cancelled = false;
    let timeoutId: ReturnType<typeof setTimeout> | null = null;
    async function poll() {
      if (cancelled) return;

      if (!connectionState.isOnline) {
        // Skip API calls but keep the polling loop alive
        if (!cancelled) {
          timeoutId = setTimeout(poll, STATUS_POLL_INTERVAL_MS);
        }
        return;
      }

      await Promise.all([fetchMigrationStatus(), fetchLegacyStatus()]);
      if (!cancelled) {
        timeoutId = setTimeout(poll, STATUS_POLL_INTERVAL_MS);
      }
    }
    timeoutId = setTimeout(poll, STATUS_POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      if (timeoutId) clearTimeout(timeoutId);
    };
  });
</script>

<!-- The wrapper stays mounted while alerts and views come and go, so focus has somewhere
     to land when a successful retry removes the button that had it -->
<div
  bind:this={viewAnchor}
  tabindex="-1"
  class="rounded-lg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-primary)]"
>
  <!-- Failed status fetches and failed migration actions land here, whichever view follows -->
  {#if !migrationStatus.loading && migrationStatus.error}
    <ErrorAlert className="mb-4">
      <span class="block">{migrationStatus.error}</span>
      <Button
        className="mt-2"
        onclick={retryMigrationStatus}
        aria-disabled={statusRetrying ? 'true' : undefined}
      >
        {statusRetrying ? t('common.retrying') : t('common.retry')}
      </Button>
    </ErrorAlert>
  {/if}
  {#if actionError}
    <ErrorAlert
      message={actionError.message}
      dismissible
      onDismiss={() => {
        actionError = null;
      }}
      className="mb-4"
    />
  {/if}

  {#if migrationStatus.loading}
    <!-- Loading state (waiting for migration status to determine view) -->
    <div class="flex items-center justify-center py-16" role="status">
      <div class="text-sm text-muted">
        {t('system.database.dashboard.loading')}
      </div>
    </div>
  {:else if isV2OnlyMode}
    <!-- OPERATIONAL DASHBOARD VIEW -->
    {#if overviewLoading}
      <div class="flex items-center justify-center py-16" role="status">
        <div class="text-sm text-muted">
          {t('system.database.dashboard.loading')}
        </div>
      </div>
    {:else if overviewError || !overview}
      <div
        class="bg-[color-mix(in_srgb,var(--color-error)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-error)_20%,transparent)] rounded-xl p-4 text-sm text-[var(--color-error)]"
      >
        {overviewError ?? t('system.database.dashboard.fetchFailed')}
      </div>
    {:else}
      <div class="space-y-4">
        <!-- Top metric strip -->
        <DatabaseMetricStrip
          performance={overview.performance}
          engine={overview.engine}
          status={overview.status}
          sizeBytes={overview.size_bytes}
          totalDetections={overview.total_detections}
          journalMode={overview.sqlite?.journal_mode}
          {readLatencyHistory}
          {writeLatencyHistory}
          {queriesPerSecHistory}
        />

        <!-- Middle row: engine-specific cards + detection rate -->
        <div class="grid grid-cols-1 lg:grid-cols-3 gap-3">
          {#if overview.engine === 'sqlite' && overview.sqlite}
            <DatabaseSqliteDetails details={overview.sqlite} />
            <DatabaseLocksWalCard details={overview.sqlite} />
          {:else if overview.engine === 'mysql' && overview.mysql}
            <DatabaseMysqlConnectionPool details={overview.mysql} />
            <DatabaseMysqlInnodbCard details={overview.mysql} />
          {/if}

          <DatabaseDetectionRateCard
            data={overview.detection_rate_24h ?? []}
            engine={overview.engine}
            mysqlHost={overview.engine === 'mysql' ? overview.location : undefined}
          />
        </div>

        <!-- Table breakdown -->
        <DatabaseTableBreakdown
          tables={overview.tables ?? []}
          showEngine={overview.engine === 'mysql'}
        />

        <!-- Legacy cleanup (only if legacy DB exists and cleanup is available) -->
        {#if legacyStatus.data?.exists && legacyStatus.data?.can_cleanup}
          <LegacyCleanupCard
            status={legacyStatus.data}
            cleanupState={migrationStatus.data?.cleanup_state ?? 'idle'}
            cleanupError={migrationStatus.data?.cleanup_error ?? ''}
            cleanupSpaceReclaimed={migrationStatus.data?.cleanup_space_reclaimed ?? 0}
            isLoading={legacyStatus.loading || cleanupLoading}
            onDelete={() => (showCleanupDialog = true)}
          />
        {/if}
      </div>
    {/if}
  {:else}
    <!-- MIGRATION VIEW (not v2-only) -->
    <div class="space-y-4">
      <!-- Top metric strip. The strip and the progress card report the migration state,
           which they would show as Idle while the status is unknown; the legacy details,
           prerequisites and backups come from their own endpoints and stay. -->
      {#if !statusUnknown}
        <DatabaseMigrationMetricStrip
          legacyStats={legacyStats.data}
          v2Stats={v2Stats.data}
          migrationStatus={migrationStatus.data}
        />
      {/if}

      <!-- Middle row: details + progress -->
      <div class="grid grid-cols-1 lg:grid-cols-3 gap-3">
        <DatabaseMigrationDetails stats={legacyStats.data} />
        {#if !statusUnknown}
          <div class="lg:col-span-2">
            <DatabaseMigrationProgress
              status={migrationStatus.data}
              legacyStats={legacyStats.data}
              v2Stats={v2Stats.data}
              prerequisites={prerequisites.data}
              prerequisitesLoading={prerequisitesBusy}
              {prerequisitesError}
              isStarting={startLoading}
              onStart={() => (showConfirmDialog = true)}
              onPause={pauseMigration}
              onResume={resumeMigration}
              onRetryValidation={retryValidation}
              onCancel={cancelMigration}
            />
          </div>
        {/if}
      </div>

      <!-- Prerequisites (collapsible, hidden during active migration) -->
      {#if !isActive}
        <DatabasePrerequisites
          prerequisites={prerequisites.data}
          isLoading={prerequisites.loading}
          error={prerequisites.error}
          onRefresh={() => runPrerequisites(true)}
        />
      {/if}

      <!-- Backup history -->
      <DatabaseBackupHistory
        backups={backupJobs}
        onCreateBackup={createBackup}
        isCreating={backupCreating}
        disabled={isActive}
      />
    </div>
  {/if}
</div>

<!-- Dialogs -->
<MigrationConfirmDialog
  open={showConfirmDialog}
  onConfirm={startMigration}
  onCancel={() => (showConfirmDialog = false)}
  isLoading={startLoading}
/>
<LegacyCleanupConfirmDialog
  open={showCleanupDialog}
  sizeBytes={legacyStatus.data?.size_bytes ?? 0}
  isLoading={cleanupLoading}
  onConfirm={startCleanup}
  onCancel={() => (showCleanupDialog = false)}
/>
