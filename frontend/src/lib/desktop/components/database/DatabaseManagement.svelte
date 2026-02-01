<!--
  DatabaseManagement Container Component

  Main container for database management view.
  Fetches and displays database stats for both legacy and v2 databases,
  and provides migration controls.

  Features:
  - Fetches legacy and v2 database stats
  - Fetches migration status
  - Polls for updates when migration is active
  - Composes DatabaseStatsCard and MigrationControlCard

  @component
-->
<script lang="ts">
  import { api, ApiError } from '$lib/utils/api';
  import { t } from '$lib/i18n';
  import DatabaseStatsCard from './DatabaseStatsCard.svelte';
  import MigrationControlCard from './MigrationControlCard.svelte';
  import MigrationConfirmDialog from './MigrationConfirmDialog.svelte';
  import LegacyCleanupCard from './LegacyCleanupCard.svelte';
  import LegacyCleanupConfirmDialog from './LegacyCleanupConfirmDialog.svelte';
  import type {
    DatabaseStats,
    MigrationStatus,
    PrerequisitesResponse,
    ApiState,
  } from '$lib/types/migration';
  import type { LegacyStatus } from '$lib/types/legacy';

  // Polling interval for migration and cleanup status updates (milliseconds)
  const STATUS_POLL_INTERVAL_MS = 2000;

  // State
  let legacyStats = $state<ApiState<DatabaseStats>>({ loading: true, error: null, data: null });
  let v2Stats = $state<ApiState<DatabaseStats>>({ loading: true, error: null, data: null });
  let migrationStatus = $state<ApiState<MigrationStatus>>({
    loading: true,
    error: null,
    data: null,
  });
  let prerequisites = $state<ApiState<PrerequisitesResponse>>({
    loading: true,
    error: null,
    data: null,
  });
  let showConfirmDialog = $state(false);
  let startLoading = $state(false);
  let initialized = $state(false);

  // Legacy cleanup state
  let legacyStatus = $state<ApiState<LegacyStatus>>({ loading: false, error: null, data: null });
  let showCleanupDialog = $state(false);
  let cleanupLoading = $state(false);

  // Computed: Is migration active (should poll)
  let isActive = $derived(
    migrationStatus.data?.state === 'initializing' ||
      migrationStatus.data?.state === 'dual_write' ||
      migrationStatus.data?.state === 'migrating' ||
      migrationStatus.data?.state === 'migrating_predictions' ||
      migrationStatus.data?.state === 'validating' ||
      migrationStatus.data?.state === 'cutover'
  );

  // Computed: Is v2-only mode (fresh install or post-migration complete)
  // In this mode, we only show the primary database card and hide migration controls
  let isV2OnlyMode = $derived(migrationStatus.data?.is_v2_only_mode === true);

  // Computed: Is cleanup in progress (should poll)
  let isCleanupActive = $derived(migrationStatus.data?.cleanup_state === 'in_progress');

  // Fetch functions
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
      // V2 not existing is not an error state
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

  async function fetchMigrationStatus(): Promise<void> {
    try {
      const data = await api.get<MigrationStatus>('/api/v2/system/database/migration/status');
      migrationStatus.data = data;
      migrationStatus.error = null;
    } catch (e) {
      migrationStatus.error =
        e instanceof ApiError ? e.message : t('system.database.migration.errors.fetchFailed');
    } finally {
      migrationStatus.loading = false;
    }
  }

  async function fetchPrerequisites(): Promise<void> {
    try {
      prerequisites.loading = true;
      prerequisites.error = null;
      const data = await api.get<PrerequisitesResponse>(
        '/api/v2/system/database/migration/prerequisites'
      );
      prerequisites.data = data;
    } catch (e) {
      prerequisites.data = null;
      prerequisites.error =
        e instanceof ApiError
          ? e.message
          : t('system.database.migration.prerequisites.errors.fetchFailed');
    } finally {
      prerequisites.loading = false;
    }
  }

  async function fetchAll(): Promise<void> {
    await Promise.all([
      fetchLegacyStats(),
      fetchV2Stats(),
      fetchMigrationStatus(),
      fetchPrerequisites(),
    ]);
  }

  // Legacy cleanup functions
  async function fetchLegacyStatus(): Promise<void> {
    try {
      legacyStatus.loading = true;
      legacyStatus.error = null;
      legacyStatus.data = await api.get<LegacyStatus>('/api/v2/system/database/legacy/status');
    } catch (e) {
      legacyStatus.error =
        e instanceof ApiError ? e.message : t('system.database.legacy.fetchFailed');
    } finally {
      legacyStatus.loading = false;
    }
  }

  async function startCleanup(): Promise<void> {
    showCleanupDialog = false;
    cleanupLoading = true;

    try {
      await api.post('/api/v2/system/database/legacy/cleanup');
      // Immediately fetch status to start tracking progress (parallel for performance)
      await Promise.all([fetchMigrationStatus(), fetchLegacyStatus()]);
    } catch (e) {
      legacyStatus.error =
        e instanceof ApiError ? e.message : t('system.database.legacy.cleanup.failed');
    } finally {
      cleanupLoading = false;
    }
  }

  // Migration actions
  async function startMigration(): Promise<void> {
    // Close dialog immediately to prevent UI blocking
    showConfirmDialog = false;
    startLoading = true;

    try {
      // Only include total_records when data is available
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

  async function cancelMigration(): Promise<void> {
    try {
      await api.post('/api/v2/system/database/migration/cancel');
      await fetchMigrationStatus();
    } catch (e) {
      migrationStatus.error =
        e instanceof ApiError ? e.message : t('system.database.migration.errors.cancelFailed');
    }
  }

  // Polling effect - uses local variable to avoid reactivity loop
  $effect(() => {
    if (!isActive) return;

    // Start polling when migration is active
    const interval = setInterval(() => {
      fetchMigrationStatus();
      fetchV2Stats(); // Also refresh v2 stats to show growing detection count
    }, STATUS_POLL_INTERVAL_MS);

    // Cleanup when isActive becomes false or component unmounts
    return () => clearInterval(interval);
  });

  // Initial fetch
  $effect(() => {
    if (!initialized) {
      initialized = true;
      fetchAll();
    }
  });

  // Fetch legacy status when entering v2-only mode
  $effect(() => {
    if (isV2OnlyMode && !legacyStatus.data && !legacyStatus.loading) {
      fetchLegacyStatus();
    }
  });

  // Poll during cleanup
  $effect(() => {
    if (!isCleanupActive) return;

    const interval = setInterval(() => {
      fetchMigrationStatus();
      fetchLegacyStatus();
    }, STATUS_POLL_INTERVAL_MS);

    return () => clearInterval(interval);
  });
</script>

<div class="space-y-6">
  <!-- Database Stats Grid -->
  <div class="relative">
    {#if isV2OnlyMode}
      <!-- V2-only mode: Show single database card and legacy cleanup -->
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-6 items-start">
        <DatabaseStatsCard
          title={t('system.database.v2.title')}
          dbType="v2"
          stats={legacyStats.data}
          isLoading={legacyStats.loading}
          error={legacyStats.error}
          migrationActive={false}
        />

        <!-- Legacy cleanup card (only if legacy database exists) -->
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
    {:else}
      <!-- Normal mode: Show both legacy and v2 database cards -->
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
    {/if}
  </div>

  <!-- Migration Control (hidden in v2-only mode) -->
  {#if !isV2OnlyMode}
    <MigrationControlCard
      status={migrationStatus.data}
      isLoading={migrationStatus.loading && !migrationStatus.data}
      isStarting={startLoading}
      error={migrationStatus.error}
      {prerequisites}
      onStart={() => (showConfirmDialog = true)}
      onPause={pauseMigration}
      onResume={resumeMigration}
      onCancel={cancelMigration}
      onRefreshPrerequisites={fetchPrerequisites}
    />
  {/if}
</div>

<!-- Confirmation Dialog -->
<MigrationConfirmDialog
  open={showConfirmDialog}
  onConfirm={startMigration}
  onCancel={() => (showConfirmDialog = false)}
  isLoading={startLoading}
/>

<!-- Legacy Cleanup Confirmation Dialog -->
<LegacyCleanupConfirmDialog
  open={showCleanupDialog}
  sizeBytes={legacyStatus.data?.size_bytes ?? 0}
  isLoading={cleanupLoading}
  onConfirm={startCleanup}
  onCancel={() => (showCleanupDialog = false)}
/>
