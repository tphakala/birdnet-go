<script lang="ts">
  import { api, ApiError } from '$lib/utils/api';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import ReconnectingEventSource from 'reconnecting-eventsource';
  import type { DatabaseOverviewResponse } from '$lib/types/database';
  import type { MigrationStatus, ApiState, PrerequisitesResponse } from '$lib/types/migration';
  import type { LegacyStatus } from '$lib/types/legacy';

  // Sub-components
  import DatabaseMetricStrip from '$lib/desktop/features/system/components/DatabaseMetricStrip.svelte';
  import DatabaseSqliteDetails from '$lib/desktop/features/system/components/DatabaseSqliteDetails.svelte';
  import DatabaseLocksWalCard from '$lib/desktop/features/system/components/DatabaseLocksWalCard.svelte';
  import DatabaseMysqlConnectionPool from '$lib/desktop/features/system/components/DatabaseMysqlConnectionPool.svelte';
  import DatabaseMysqlInnodbCard from '$lib/desktop/features/system/components/DatabaseMysqlInnodbCard.svelte';
  import DatabaseDetectionRateCard from '$lib/desktop/features/system/components/DatabaseDetectionRateCard.svelte';
  import DatabaseTableBreakdown from '$lib/desktop/features/system/components/DatabaseTableBreakdown.svelte';

  // Existing migration components (reused)
  import MigrationControlCard from './MigrationControlCard.svelte';
  import MigrationConfirmDialog from './MigrationConfirmDialog.svelte';
  import DatabaseStatsCard from './DatabaseStatsCard.svelte';
  import LegacyCleanupCard from './LegacyCleanupCard.svelte';
  import LegacyCleanupConfirmDialog from './LegacyCleanupConfirmDialog.svelte';

  import type { DatabaseStats } from '$lib/types/migration';

  const logger = loggers.ui;

  const MAX_HISTORY_POINTS = 60;
  const OVERVIEW_REFRESH_INTERVAL_MS = 30_000;
  const STATUS_POLL_INTERVAL_MS = 2000;

  // --- Overview state ---
  let overview = $state<DatabaseOverviewResponse | null>(null);
  let overviewError = $state<string | null>(null);
  let overviewLoading = $state(true);

  // --- Sparkline history ---
  let readLatencyHistory = $state<number[]>([]);
  let writeLatencyHistory = $state<number[]>([]);
  let queriesPerSecHistory = $state<number[]>([]);

  // --- Migration state (reused from DatabaseManagement) ---
  let migrationStatus = $state<ApiState<MigrationStatus>>({
    loading: true,
    error: null,
    data: null,
  });
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
  let isActive = $derived(
    migrationStatus.data?.state === 'initializing' ||
      migrationStatus.data?.state === 'dual_write' ||
      migrationStatus.data?.state === 'migrating' ||
      migrationStatus.data?.state === 'migrating_predictions' ||
      migrationStatus.data?.state === 'validating' ||
      migrationStatus.data?.state === 'cutover'
  );
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

  async function fetchMigrationStatus(): Promise<void> {
    try {
      migrationStatus.data = await api.get<MigrationStatus>(
        '/api/v2/system/database/migration/status'
      );
      migrationStatus.error = null;
    } catch (e) {
      migrationStatus.error =
        e instanceof ApiError ? e.message : t('system.database.migration.errors.fetchFailed');
    } finally {
      migrationStatus.loading = false;
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

  async function fetchPrerequisites(): Promise<void> {
    try {
      prerequisites.loading = true;
      prerequisites.data = await api.get<PrerequisitesResponse>(
        '/api/v2/system/database/migration/prerequisites'
      );
      prerequisites.error = null;
    } catch (e) {
      prerequisites.error =
        e instanceof ApiError
          ? e.message
          : t('system.database.migration.prerequisites.errors.fetchFailed');
    } finally {
      prerequisites.loading = false;
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

  // --- Migration actions (same as DatabaseManagement) ---
  async function startMigration(): Promise<void> {
    showConfirmDialog = false;
    startLoading = true;
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
      migrationStatus.error =
        e instanceof ApiError ? e.message : t('system.database.migration.errors.startFailed');
    } finally {
      startLoading = false;
    }
  }

  async function pauseMigration(): Promise<void> {
    try {
      await api.post('/api/v2/system/database/migration/pause');
      await fetchMigrationStatus();
    } catch (e) {
      migrationStatus.error =
        e instanceof ApiError ? e.message : t('system.database.migration.errors.pauseFailed');
    }
  }

  async function resumeMigration(): Promise<void> {
    try {
      await api.post('/api/v2/system/database/migration/resume');
      await fetchMigrationStatus();
    } catch (e) {
      migrationStatus.error =
        e instanceof ApiError ? e.message : t('system.database.migration.errors.resumeFailed');
    }
  }

  async function retryValidation(): Promise<void> {
    try {
      await api.post('/api/v2/system/database/migration/retry-validation');
      await fetchMigrationStatus();
    } catch (e) {
      migrationStatus.error =
        e instanceof ApiError
          ? e.message
          : t('system.database.migration.errors.retryValidationFailed');
    }
  }

  async function cancelMigration(): Promise<void> {
    try {
      await api.post('/api/v2/system/database/migration/cancel');
      await fetchMigrationStatus();
    } catch (e) {
      migrationStatus.error =
        e instanceof ApiError ? e.message : t('system.database.migration.errors.cancelFailed');
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
        // eslint-disable-next-line no-undef
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
      fetchPrerequisites(),
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
    async function poll() {
      await Promise.all([fetchMigrationStatus(), fetchV2Stats()]);
      if (!cancelled) {
        setTimeout(poll, STATUS_POLL_INTERVAL_MS);
      }
    }
    const timer = setTimeout(poll, STATUS_POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearTimeout(timer);
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
    async function poll() {
      await Promise.all([fetchMigrationStatus(), fetchLegacyStatus()]);
      if (!cancelled) {
        setTimeout(poll, STATUS_POLL_INTERVAL_MS);
      }
    }
    const timer = setTimeout(poll, STATUS_POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  });
</script>

{#if migrationStatus.loading}
  <!-- Loading state (waiting for migration status to determine view) -->
  <div class="flex items-center justify-center py-16">
    <div class="text-sm text-slate-400 dark:text-slate-500">Loading...</div>
  </div>
{:else if isV2OnlyMode}
  <!-- OPERATIONAL DASHBOARD VIEW -->
  {#if overviewLoading}
    <div class="flex items-center justify-center py-16">
      <div class="text-sm text-slate-400 dark:text-slate-500">Loading...</div>
    </div>
  {:else if overviewError || !overview}
    <div
      class="bg-red-500/10 border border-red-500/20 rounded-xl p-4 text-sm text-red-600 dark:text-red-400"
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

      <!-- Legacy cleanup (if legacy DB still exists) -->
      {#if legacyStatus.data?.exists}
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
  <div class="space-y-6">
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
      <DatabaseStatsCard
        title={t('system.database.legacy.title')}
        dbType="legacy"
        stats={legacyStats.data}
        isLoading={legacyStats.loading}
        error={legacyStats.error}
        migrationActive={isActive}
      />
      <DatabaseStatsCard
        title={t('system.database.v2.title')}
        dbType="v2"
        stats={v2Stats.data}
        isLoading={v2Stats.loading}
        error={v2Stats.error}
        migrationActive={isActive}
      />
    </div>

    <MigrationControlCard
      status={migrationStatus.data}
      isLoading={migrationStatus.loading && !migrationStatus.data}
      isStarting={startLoading}
      error={migrationStatus.error}
      {prerequisites}
      onStart={() => (showConfirmDialog = true)}
      onPause={pauseMigration}
      onResume={resumeMigration}
      onRetryValidation={retryValidation}
      onCancel={cancelMigration}
      onRefreshPrerequisites={fetchPrerequisites}
    />
  </div>
{/if}

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
