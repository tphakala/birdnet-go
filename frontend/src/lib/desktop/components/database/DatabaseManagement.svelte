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
  import type {
    DatabaseStats,
    MigrationStatus,
    PrerequisitesResponse,
    ApiState,
  } from '$lib/types/migration';

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

  // Computed: Is migration active (should poll)
  let isActive = $derived(
    migrationStatus.data?.state === 'dual_write' ||
      migrationStatus.data?.state === 'migrating' ||
      migrationStatus.data?.state === 'validating' ||
      migrationStatus.data?.state === 'cutover'
  );

  // Computed: Is v2-only mode (fresh install or post-migration complete)
  // In this mode, we only show the primary database card and hide migration controls
  let isV2OnlyMode = $derived(migrationStatus.data?.is_v2_only_mode === true);

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
    }, 2000);

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
</script>

<div class="space-y-6">
  <!-- Database Stats Grid with Data Flow Animation -->
  <div class="relative">
    {#if isV2OnlyMode}
      <!-- V2-only mode: Show single database card -->
      <div class="max-w-md mx-auto">
        <DatabaseStatsCard
          title={t('system.database.v2.title')}
          dbType="v2"
          stats={legacyStats.data}
          isLoading={legacyStats.loading}
          error={legacyStats.error}
          migrationActive={false}
        />
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

      <!-- Data Flow Animation (visible during active migration on desktop) -->
      {#if isActive}
        <div
          class="hidden lg:flex absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2
                 w-28 h-10 items-center justify-center pointer-events-none z-10"
        >
          <!-- Data stream track/bridge with faded ends -->
          <div class="absolute inset-y-3 inset-x-0 flex items-center">
            <div
              class="h-1 flex-1 bg-gradient-to-r from-transparent via-[var(--color-primary)]/25 to-[var(--color-primary)]/25"
            ></div>
            <div class="h-1 flex-[2] bg-[var(--color-primary)]/25"></div>
            <div
              class="h-1 flex-1 bg-gradient-to-l from-transparent via-[var(--color-primary)]/25 to-[var(--color-primary)]/25"
            ></div>
          </div>

          <!-- Subtle glow along the bridge with faded ends -->
          <div class="absolute inset-y-2 inset-x-0 flex items-center">
            <div
              class="h-2 flex-1 bg-gradient-to-r from-transparent to-[var(--color-primary)]/10 blur-sm"
            ></div>
            <div class="h-2 flex-[2] bg-[var(--color-primary)]/10 blur-sm animate-pulse"></div>
            <div
              class="h-2 flex-1 bg-gradient-to-l from-transparent to-[var(--color-primary)]/10 blur-sm"
            ></div>
          </div>

          <!-- Animated data packets flowing left to right -->
          <div class="data-packet-container absolute inset-y-0 inset-x-0 overflow-hidden">
            {#each { length: 4 } as _, i (i)}
              <div
                class="data-packet absolute top-1/2 -translate-y-1/2 w-2.5 h-2.5
                       rounded-full bg-[var(--color-primary)]"
                style:animation-delay="{i * 0.5}s"
              ></div>
            {/each}
          </div>
        </div>
      {/if}
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

<style>
  /* Gradient mask to fade particles at both ends */
  .data-packet-container {
    mask-image: linear-gradient(to right, transparent, black 25%, black 75%, transparent);
  }

  /* Data packet animation - flows from left to right */
  .data-packet {
    animation: flow-packet 2s ease-in-out infinite;
    opacity: 0;
    box-shadow:
      0 0 6px var(--color-primary),
      0 0 12px var(--color-primary);
  }

  @keyframes flow-packet {
    0% {
      left: -10%;
      opacity: 0;
      transform: translateY(-50%) scale(0.5);
    }

    15% {
      opacity: 1;
      transform: translateY(-50%) scale(1);
    }

    85% {
      opacity: 1;
      transform: translateY(-50%) scale(1);
    }

    100% {
      left: 110%;
      opacity: 0;
      transform: translateY(-50%) scale(0.5);
    }
  }
</style>
