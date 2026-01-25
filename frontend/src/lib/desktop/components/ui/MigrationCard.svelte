<!--
MigrationCard.svelte - Database migration status and control card

Purpose:
- Display current migration status with state-based UI
- Show migration progress (records migrated, percentage, rate)
- Provide control buttons (start, pause, resume, cancel, rollback)
- Auto-poll status when migration is active

Features:
- State-based styling (idle, dual-write, migrating, paused, completed)
- Progress bar with percentage
- Migration rate and estimated time remaining
- Dirty ID count for records pending re-sync
- Confirmation modal for cancel action
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { api, ApiError } from '$lib/utils/api';
  import {
    Play,
    Pause,
    Square,
    RefreshCw,
    Database,
    AlertCircle,
    CheckCircle2,
    RotateCcw,
    Loader2,
  } from '@lucide/svelte';

  // Migration status response type
  interface MigrationStatus {
    state: string;
    started_at?: string;
    completed_at?: string;
    total_records: number;
    migrated_records: number;
    progress_percent: number;
    last_migrated_id: number;
    error_message?: string;
    dirty_id_count: number;
    records_per_second?: number;
    estimated_remaining?: string;
    worker_running: boolean;
    worker_paused: boolean;
    can_start: boolean;
    can_pause: boolean;
    can_resume: boolean;
    can_cancel: boolean;
    can_rollback: boolean;
    is_dual_write_active: boolean;
    should_read_from_v2: boolean;
  }

  // State
  let status = $state<MigrationStatus | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let actionLoading = $state(false);
  let showCancelConfirm = $state(false);
  let showRollbackConfirm = $state(false);
  let pollInterval = $state<ReturnType<typeof setInterval> | null>(null);
  let initialized = $state(false);

  // Computed: Is migration active (should poll)
  let isActive = $derived(
    status?.state === 'dual_write' ||
      status?.state === 'migrating' ||
      status?.state === 'validating' ||
      status?.state === 'cutover'
  );

  // Computed: State display info
  let stateInfo = $derived.by(() => {
    if (!status) return { label: '', colorClasses: '', icon: Database };

    const stateMap: Record<string, { label: string; colorClasses: string; icon: typeof Database }> =
      {
        idle: {
          label: t('system.database.migration.status.idle'),
          colorClasses: 'bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-300',
          icon: Database,
        },
        initializing: {
          label: t('system.database.migration.status.initializing'),
          colorClasses: 'bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300',
          icon: RefreshCw,
        },
        dual_write: {
          label: t('system.database.migration.status.dualWrite'),
          colorClasses: 'bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300',
          icon: Database,
        },
        migrating: {
          label: t('system.database.migration.status.migrating'),
          colorClasses: 'bg-primary/10 text-primary',
          icon: RefreshCw,
        },
        paused: {
          label: t('system.database.migration.status.paused'),
          colorClasses: 'bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300',
          icon: Pause,
        },
        validating: {
          label: t('system.database.migration.status.validating'),
          colorClasses: 'bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300',
          icon: RefreshCw,
        },
        cutover: {
          label: t('system.database.migration.status.cutover'),
          colorClasses: 'bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300',
          icon: AlertCircle,
        },
        completed: {
          label: t('system.database.migration.status.completed'),
          colorClasses: 'bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300',
          icon: CheckCircle2,
        },
      };

    return (
      stateMap[status.state] || {
        label: status.state,
        colorClasses: 'bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-300',
        icon: Database,
      }
    );
  });

  // Fetch migration status
  async function fetchStatus(): Promise<void> {
    try {
      status = await api.get<MigrationStatus>('/api/v2/system/database/migration/status');
      error = null;
    } catch (e) {
      if (e instanceof ApiError) {
        error = e.message;
      } else {
        error = t('system.database.migration.errors.fetchFailed');
      }
    } finally {
      loading = false;
    }
  }

  // Start migration
  async function startMigration(): Promise<void> {
    actionLoading = true;
    try {
      await api.post('/api/v2/system/database/migration/start', { total_records: 0 });
      await fetchStatus();
    } catch (e) {
      if (e instanceof ApiError) {
        error = e.message;
      } else {
        error = t('system.database.migration.errors.startFailed');
      }
    } finally {
      actionLoading = false;
    }
  }

  // Pause migration
  async function pauseMigration(): Promise<void> {
    actionLoading = true;
    try {
      await api.post('/api/v2/system/database/migration/pause');
      await fetchStatus();
    } catch (e) {
      if (e instanceof ApiError) {
        error = e.message;
      } else {
        error = t('system.database.migration.errors.pauseFailed');
      }
    } finally {
      actionLoading = false;
    }
  }

  // Resume migration
  async function resumeMigration(): Promise<void> {
    actionLoading = true;
    try {
      await api.post('/api/v2/system/database/migration/resume');
      await fetchStatus();
    } catch (e) {
      if (e instanceof ApiError) {
        error = e.message;
      } else {
        error = t('system.database.migration.errors.resumeFailed');
      }
    } finally {
      actionLoading = false;
    }
  }

  // Cancel migration
  async function cancelMigration(): Promise<void> {
    actionLoading = true;
    showCancelConfirm = false;
    try {
      await api.post('/api/v2/system/database/migration/cancel');
      await fetchStatus();
    } catch (e) {
      if (e instanceof ApiError) {
        error = e.message;
      } else {
        error = t('system.database.migration.errors.cancelFailed');
      }
    } finally {
      actionLoading = false;
    }
  }

  // Rollback migration
  async function rollbackMigration(): Promise<void> {
    actionLoading = true;
    showRollbackConfirm = false;
    try {
      await api.post('/api/v2/system/database/migration/rollback');
      await fetchStatus();
    } catch (e) {
      if (e instanceof ApiError) {
        error = e.message;
      } else {
        error = t('system.database.migration.errors.rollbackFailed');
      }
    } finally {
      actionLoading = false;
    }
  }

  // Setup polling when active
  $effect(() => {
    if (isActive && !pollInterval) {
      pollInterval = setInterval(fetchStatus, 2000);
    } else if (!isActive && pollInterval) {
      clearInterval(pollInterval);
      pollInterval = null;
    }

    return () => {
      if (pollInterval) {
        clearInterval(pollInterval);
        pollInterval = null;
      }
    };
  });

  // Initial fetch (runs only once)
  $effect(() => {
    if (!initialized) {
      initialized = true;
      fetchStatus();
    }
  });
</script>

<div
  class="rounded-xl bg-white dark:bg-gray-800 shadow-lg border border-gray-200 dark:border-gray-700"
>
  <div class="p-6">
    <div class="flex items-center justify-between">
      <h2 class="text-lg font-semibold flex items-center gap-2 text-gray-900 dark:text-white">
        <Database class="size-5" />
        {t('system.database.migration.title')}
      </h2>
      {#if status}
        {@const Icon = stateInfo.icon}
        <div
          class="px-2.5 py-1 text-xs font-medium rounded-full flex items-center gap-1 {stateInfo.colorClasses}"
        >
          <Icon class="size-3" />
          {stateInfo.label}
        </div>
      {/if}
    </div>

    <p class="text-sm text-gray-500 dark:text-gray-400 mt-1">
      {t('system.database.migration.description')}
    </p>

    {#if loading}
      <div class="flex justify-center py-8">
        <Loader2 class="size-6 animate-spin text-primary" />
      </div>
    {:else if error}
      <div
        class="p-4 rounded-lg bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-300 flex items-center gap-3 mt-4"
      >
        <AlertCircle class="size-5 shrink-0" />
        <span class="flex-1">{error}</span>
        <button
          class="p-2 rounded-lg hover:bg-red-200 dark:hover:bg-red-800/50 transition-colors"
          onclick={fetchStatus}
        >
          <RefreshCw class="size-4" />
        </button>
      </div>
    {:else if status}
      <!-- Progress Section -->
      {#if status.state !== 'idle' && status.state !== 'completed'}
        <div class="mt-4 space-y-3">
          <div class="flex justify-between text-sm text-gray-700 dark:text-gray-300">
            <span>{t('system.database.migration.progress.title')}</span>
            <span class="font-mono">
              {t('system.database.migration.progress.records', {
                migrated: status.migrated_records.toLocaleString(),
                total: status.total_records.toLocaleString(),
              })}
            </span>
          </div>
          <!-- Progress bar -->
          <div class="w-full h-2 bg-gray-200 dark:bg-gray-700 rounded-full overflow-hidden">
            <div
              class="h-full bg-primary transition-all duration-300"
              style:width="{status.progress_percent}%"
            ></div>
          </div>
          <div class="flex justify-between text-xs text-gray-400 dark:text-gray-500">
            <span>
              {t('system.database.migration.progress.percent', {
                percent: status.progress_percent.toFixed(1),
              })}
            </span>
            {#if status.records_per_second && status.records_per_second > 0}
              <span>
                {t('system.database.migration.progress.rate', {
                  rate: status.records_per_second.toFixed(1),
                })}
              </span>
            {/if}
          </div>
          {#if status.estimated_remaining}
            <div class="text-xs text-gray-400 dark:text-gray-500">
              {t('system.database.migration.progress.remaining', {
                time: status.estimated_remaining,
              })}
            </div>
          {/if}
          {#if status.dirty_id_count > 0}
            <div class="text-xs text-amber-600 dark:text-amber-400">
              {t('system.database.migration.progress.dirtyIds', {
                count: status.dirty_id_count,
              })}
            </div>
          {/if}
        </div>
      {/if}

      <!-- Worker Status -->
      {#if status.state !== 'idle'}
        <div class="mt-4 flex items-center gap-2 text-sm">
          <span class="text-gray-500 dark:text-gray-400">Worker:</span>
          {#if status.worker_running && !status.worker_paused}
            <span
              class="px-2 py-0.5 text-xs font-medium rounded-full bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300"
            >
              {t('system.database.migration.worker.running')}
            </span>
          {:else if status.worker_paused}
            <span
              class="px-2 py-0.5 text-xs font-medium rounded-full bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300"
            >
              {t('system.database.migration.worker.paused')}
            </span>
          {:else}
            <span
              class="px-2 py-0.5 text-xs font-medium rounded-full bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-300"
            >
              {t('system.database.migration.worker.stopped')}
            </span>
          {/if}
        </div>
      {/if}

      <!-- Error Message -->
      {#if status.error_message}
        <div
          class="p-4 rounded-lg bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300 flex items-center gap-3 mt-4"
        >
          <AlertCircle class="size-5 shrink-0" />
          <span>{status.error_message}</span>
        </div>
      {/if}

      <!-- Action Buttons -->
      <div class="flex justify-end gap-2 mt-6 flex-wrap">
        {#if status.can_start}
          <button
            class="px-4 py-2 bg-primary text-white rounded-lg hover:bg-primary/90 disabled:opacity-50 disabled:cursor-not-allowed transition-colors font-medium inline-flex items-center gap-2"
            onclick={startMigration}
            disabled={actionLoading}
          >
            {#if actionLoading}
              <Loader2 class="size-4 animate-spin" />
            {:else}
              <Play class="size-4" />
            {/if}
            {t('system.database.migration.actions.start')}
          </button>
        {/if}

        {#if status.can_pause}
          <button
            class="px-4 py-2 bg-amber-500 text-white rounded-lg hover:bg-amber-600 disabled:opacity-50 disabled:cursor-not-allowed transition-colors font-medium inline-flex items-center gap-2"
            onclick={pauseMigration}
            disabled={actionLoading}
          >
            {#if actionLoading}
              <Loader2 class="size-4 animate-spin" />
            {:else}
              <Pause class="size-4" />
            {/if}
            {t('system.database.migration.actions.pause')}
          </button>
        {/if}

        {#if status.can_resume}
          <button
            class="px-4 py-2 bg-green-600 text-white rounded-lg hover:bg-green-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors font-medium inline-flex items-center gap-2"
            onclick={resumeMigration}
            disabled={actionLoading}
          >
            {#if actionLoading}
              <Loader2 class="size-4 animate-spin" />
            {:else}
              <Play class="size-4" />
            {/if}
            {t('system.database.migration.actions.resume')}
          </button>
        {/if}

        {#if status.can_cancel}
          <button
            class="px-4 py-2 border border-red-500 text-red-500 rounded-lg hover:bg-red-500 hover:text-white disabled:opacity-50 disabled:cursor-not-allowed transition-colors font-medium inline-flex items-center gap-2"
            onclick={() => (showCancelConfirm = true)}
            disabled={actionLoading}
          >
            <Square class="size-4" />
            {t('system.database.migration.actions.cancel')}
          </button>
        {/if}

        {#if status.can_rollback}
          <button
            class="px-4 py-2 border border-amber-500 text-amber-500 rounded-lg hover:bg-amber-500 hover:text-white disabled:opacity-50 disabled:cursor-not-allowed transition-colors font-medium inline-flex items-center gap-2"
            onclick={() => (showRollbackConfirm = true)}
            disabled={actionLoading}
          >
            <RotateCcw class="size-4" />
            {t('system.database.migration.actions.rollback')}
          </button>
        {/if}

        <!-- Refresh button when idle or completed -->
        {#if status.state === 'idle' || status.state === 'completed'}
          <button
            class="px-4 py-2 text-gray-600 dark:text-gray-300 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 disabled:opacity-50 transition-colors font-medium inline-flex items-center gap-2"
            onclick={fetchStatus}
            disabled={loading}
          >
            <RefreshCw class="size-4" />
          </button>
        {/if}
      </div>
    {/if}
  </div>
</div>

<!-- Cancel Confirmation Modal -->
{#if showCancelConfirm}
  <div class="fixed inset-0 z-50 flex items-center justify-center">
    <button
      type="button"
      class="fixed inset-0 bg-black/50"
      onclick={() => (showCancelConfirm = false)}
      aria-label="Close modal"
    ></button>
    <div
      class="relative bg-white dark:bg-gray-800 rounded-xl shadow-xl p-6 max-w-md w-full mx-4 z-10"
    >
      <h3 class="font-bold text-lg text-gray-900 dark:text-white">
        {t('system.database.migration.confirmCancel.title')}
      </h3>
      <p class="py-4 text-gray-600 dark:text-gray-300">
        {t('system.database.migration.confirmCancel.message')}
      </p>
      <div class="flex justify-end gap-2 mt-2">
        <button
          class="px-4 py-2 text-gray-600 dark:text-gray-300 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors font-medium"
          onclick={() => (showCancelConfirm = false)}
        >
          {t('system.database.migration.confirmCancel.dismiss')}
        </button>
        <button
          class="px-4 py-2 bg-red-600 text-white rounded-lg hover:bg-red-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors font-medium inline-flex items-center gap-2"
          onclick={cancelMigration}
          disabled={actionLoading}
        >
          {#if actionLoading}
            <Loader2 class="size-4 animate-spin" />
          {/if}
          {t('system.database.migration.confirmCancel.confirm')}
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- Rollback Confirmation Modal -->
{#if showRollbackConfirm}
  <div class="fixed inset-0 z-50 flex items-center justify-center">
    <button
      type="button"
      class="fixed inset-0 bg-black/50"
      onclick={() => (showRollbackConfirm = false)}
      aria-label="Close modal"
    ></button>
    <div
      class="relative bg-white dark:bg-gray-800 rounded-xl shadow-xl p-6 max-w-md w-full mx-4 z-10"
    >
      <h3 class="font-bold text-lg text-gray-900 dark:text-white">
        {t('system.database.migration.confirmRollback.title')}
      </h3>
      <p class="py-4 text-gray-600 dark:text-gray-300">
        {t('system.database.migration.confirmRollback.message')}
      </p>
      <div class="flex justify-end gap-2 mt-2">
        <button
          class="px-4 py-2 text-gray-600 dark:text-gray-300 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors font-medium"
          onclick={() => (showRollbackConfirm = false)}
        >
          {t('system.database.migration.confirmRollback.dismiss')}
        </button>
        <button
          class="px-4 py-2 bg-amber-600 text-white rounded-lg hover:bg-amber-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors font-medium inline-flex items-center gap-2"
          onclick={rollbackMigration}
          disabled={actionLoading}
        >
          {#if actionLoading}
            <Loader2 class="size-4 animate-spin" />
          {/if}
          {t('system.database.migration.confirmRollback.confirm')}
        </button>
      </div>
    </div>
  </div>
{/if}
