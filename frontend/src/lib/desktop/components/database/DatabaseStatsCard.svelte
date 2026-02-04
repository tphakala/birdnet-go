<!--
  DatabaseStatsCard Component

  Displays database statistics with async backup capability and progress tracking.
  Shows type, location, size, detections count, and connection status.

  Props:
  - title: Card title (e.g., "Legacy Database" or "Enhanced Database")
  - dbType: 'legacy' | 'v2' - For API calls
  - stats: Database stats from API or null
  - isLoading: Whether stats are being loaded
  - error: Error message if stats fetch failed

  @component
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { t } from '$lib/i18n';
  import { getCsrfToken } from '$lib/utils/api';
  import { formatBytes } from '$lib/utils/formatters';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { Database, Download, X } from '@lucide/svelte';

  interface DatabaseStats {
    type: string;
    location: string;
    size_bytes: number;
    total_detections: number;
    connected: boolean;
  }

  interface BackupJob {
    job_id: string;
    db_type: string;
    status: 'pending' | 'in_progress' | 'completed' | 'failed';
    progress: number;
    bytes_written: number;
    total_bytes: number;
    started_at: string;
    completed_at?: string;
    error?: string;
    download_url?: string;
  }

  interface Props {
    title: string;
    dbType: 'legacy' | 'v2';
    stats: DatabaseStats | null;
    isLoading: boolean;
    error: string | null;
    migrationActive?: boolean;
  }

  let { title, dbType, stats, isLoading, error, migrationActive = false }: Props = $props();

  // Backup state
  let backupJobId = $state<string | null>(null);
  let backupStatus = $state<
    'idle' | 'pending' | 'in_progress' | 'completed' | 'downloading' | 'failed'
  >('idle');
  let backupProgress = $state(0);
  let backupBytesWritten = $state(0);
  let backupTotalBytes = $state(0);
  let backupError = $state<string | null>(null);
  let pollInterval: ReturnType<typeof setInterval> | null = null;

  // Polling interval in milliseconds
  const POLL_INTERVAL_MS = 1500;

  // Check if backup is available (SQLite only)
  let canBackup = $derived(stats?.type === 'sqlite' && stats?.connected);

  // Check if backup is in progress (includes downloading state)
  let isBackupActive = $derived(
    backupStatus === 'pending' || backupStatus === 'in_progress' || backupStatus === 'downloading'
  );

  // Check for active backup jobs on mount
  onMount(async () => {
    await checkForActiveJob();
  });

  // Cleanup polling on destroy
  onDestroy(() => {
    stopPolling();
  });

  // Check for existing active backup job
  async function checkForActiveJob() {
    try {
      const response = await fetch(
        buildAppUrl(`/api/v2/system/database/backup/jobs?type=${dbType}`),
        {
          headers: {
            'X-CSRF-Token': getCsrfToken() ?? '',
          },
        }
      );

      if (!response.ok) return;

      const data = await response.json();
      const activeJob = data.jobs?.find(
        (job: BackupJob) => job.status === 'pending' || job.status === 'in_progress'
      );

      if (activeJob) {
        // Resume tracking this job
        backupJobId = activeJob.job_id;
        updateFromJob(activeJob);
        startPolling();
      }
    } catch {
      // Ignore errors - just means no active job
    }
  }

  // Start a new backup job
  async function startBackup() {
    if (!canBackup || isBackupActive) return;

    backupError = null;
    backupStatus = 'pending';
    backupProgress = 0;
    backupBytesWritten = 0;
    backupTotalBytes = stats?.size_bytes ?? 0;

    try {
      const response = await fetch(
        buildAppUrl(`/api/v2/system/database/backup/jobs?type=${dbType}`),
        {
          method: 'POST',
          headers: {
            'X-CSRF-Token': getCsrfToken() ?? '',
          },
        }
      );

      if (!response.ok) {
        const errorData = await response.json().catch(() => ({}));

        // Check if backup already in progress
        if (response.status === 409 && errorData.existing_job_id) {
          backupJobId = errorData.existing_job_id;
          startPolling();
          return;
        }

        throw new Error(errorData.message || errorData.error || 'Failed to start backup');
      }

      const data = await response.json();
      backupJobId = data.job_id;
      startPolling();
    } catch (e) {
      backupStatus = 'failed';
      backupError = e instanceof Error ? e.message : 'Failed to start backup';
    }
  }

  // Start polling for job status
  function startPolling() {
    if (pollInterval) return;

    pollInterval = setInterval(pollJobStatus, POLL_INTERVAL_MS);
    // Also poll immediately
    pollJobStatus();
  }

  // Stop polling
  function stopPolling() {
    if (pollInterval) {
      clearInterval(pollInterval);
      pollInterval = null;
    }
  }

  // Poll for job status
  async function pollJobStatus() {
    if (!backupJobId) {
      stopPolling();
      return;
    }

    try {
      const response = await fetch(
        buildAppUrl(`/api/v2/system/database/backup/jobs/${backupJobId}`),
        {
          headers: {
            'X-CSRF-Token': getCsrfToken() ?? '',
          },
        }
      );

      if (!response.ok) {
        if (response.status === 404 || response.status === 410) {
          // Job expired or not found
          stopPolling();
          resetBackupState();
          return;
        }
        throw new Error('Failed to get backup status');
      }

      const job: BackupJob = await response.json();
      updateFromJob(job);

      // Handle completed/failed states
      if (job.status === 'completed' && backupStatus !== 'downloading') {
        stopPolling();
        backupStatus = 'downloading';
        await triggerDownload();
      } else if (job.status === 'failed') {
        stopPolling();
        backupError = job.error || 'Backup failed';
      }
    } catch {
      // Don't stop polling on transient errors - silently retry
    }
  }

  // Update state from job data
  function updateFromJob(job: BackupJob) {
    backupStatus = job.status;
    backupProgress = job.progress;
    backupBytesWritten = job.bytes_written;
    backupTotalBytes = job.total_bytes;
    if (job.error) {
      backupError = job.error;
    }
  }

  // Trigger the file download
  async function triggerDownload() {
    if (!backupJobId) return;

    try {
      const response = await fetch(
        buildAppUrl(`/api/v2/system/database/backup/jobs/${backupJobId}/download`),
        {
          headers: {
            'X-CSRF-Token': getCsrfToken() ?? '',
          },
        }
      );

      if (!response.ok) {
        throw new Error('Failed to download backup');
      }

      // Get filename from Content-Disposition header or generate one
      const contentDisposition = response.headers.get('Content-Disposition');
      let filename = `birdnet-${dbType}-backup-${Date.now()}.db`;
      if (contentDisposition) {
        const match = contentDisposition.match(/filename="?([^"]+)"?/);
        if (match) {
          filename = match[1];
        }
      }

      // Trigger browser download
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      a.click();
      URL.revokeObjectURL(url);

      // Reset state after successful download
      resetBackupState();
    } catch (e) {
      backupError = e instanceof Error ? e.message : 'Failed to download backup';
      backupStatus = 'failed';
    }
  }

  // Cancel backup job
  async function cancelBackup() {
    if (!backupJobId) return;

    stopPolling();

    try {
      await fetch(buildAppUrl(`/api/v2/system/database/backup/jobs/${backupJobId}`), {
        method: 'DELETE',
        headers: {
          'X-CSRF-Token': getCsrfToken() ?? '',
        },
      });
    } catch {
      // Ignore cancel errors
    }

    resetBackupState();
  }

  // Reset backup state
  function resetBackupState() {
    backupJobId = null;
    backupStatus = 'idle';
    backupProgress = 0;
    backupBytesWritten = 0;
    backupTotalBytes = 0;
    backupError = null;
  }

  // Retry backup after failure
  function retryBackup() {
    resetBackupState();
    startBackup();
  }
</script>

<div class="rounded-lg bg-[var(--color-base-100)] shadow-sm border border-[var(--color-base-200)]">
  <!-- Header -->
  <div class="px-6 py-4 border-b border-[var(--color-base-200)]">
    <div class="flex items-center justify-between">
      <h3 class="text-lg font-semibold flex items-center gap-2 text-[var(--color-base-content)]">
        <Database class="size-5" />
        {title}
      </h3>
      <!-- Status badge -->
      {#if stats}
        <span
          class="px-2 py-1 text-xs font-medium rounded-full
                     {stats.connected
            ? 'bg-[var(--color-success)]/10 text-[var(--color-success)]'
            : 'bg-[var(--color-error)]/10 text-[var(--color-error)]'}"
        >
          {stats.connected
            ? t('system.database.stats.connected')
            : t('system.database.stats.disconnected')}
        </span>
      {/if}
    </div>
  </div>

  <!-- Body -->
  <div class="px-6 py-4 space-y-3">
    {#if isLoading}
      <!-- Loading skeleton -->
      <div class="space-y-2">
        <div class="h-4 bg-[var(--color-base-200)] rounded animate-pulse w-3/4"></div>
        <div class="h-4 bg-[var(--color-base-200)] rounded animate-pulse w-1/2"></div>
        <div class="h-4 bg-[var(--color-base-200)] rounded animate-pulse w-2/3"></div>
      </div>
    {:else if error}
      <div
        class="p-3 rounded-lg bg-[var(--color-error)]/10 text-[var(--color-error)] text-sm"
        role="alert"
        aria-live="assertive"
      >
        {error}
      </div>
    {:else if stats}
      <!-- Stats rows -->
      <div class="flex justify-between text-sm">
        <span class="text-[var(--color-base-content)]/70">{t('system.database.stats.type')}:</span>
        <span class="font-medium text-[var(--color-base-content)]">{stats.type}</span>
      </div>
      <div class="flex justify-between text-sm">
        <span class="text-[var(--color-base-content)]/70"
          >{t('system.database.stats.location')}:</span
        >
        <span
          class="font-medium text-[var(--color-base-content)] truncate max-w-[200px]"
          title={stats.location}
        >
          {stats.location}
        </span>
      </div>
      <div class="flex justify-between text-sm">
        <span class="text-[var(--color-base-content)]/70">{t('system.database.stats.size')}:</span>
        <span class="font-medium text-[var(--color-base-content)]"
          >{formatBytes(stats.size_bytes)}</span
        >
      </div>
      <div class="flex justify-between text-sm">
        <span class="text-[var(--color-base-content)]/70"
          >{t('system.database.stats.detections')}:</span
        >
        <span class="font-medium text-[var(--color-base-content)]"
          >{stats.total_detections.toLocaleString()}</span
        >
      </div>
    {:else}
      <div class="text-sm text-[var(--color-base-content)]/70 text-center py-4">
        {t('system.database.notInitialized')}
      </div>
    {/if}
  </div>

  <!-- Footer -->
  <div class="px-6 py-4 border-t border-[var(--color-base-200)]">
    {#if backupError}
      <div
        class="mb-3 p-2 rounded text-xs bg-[var(--color-error)]/10 text-[var(--color-error)]"
        role="alert"
        aria-live="assertive"
      >
        {backupError}
      </div>
    {/if}

    {#if stats?.type === 'mysql'}
      <p class="text-xs text-[var(--color-base-content)]/60 text-center">
        {t('system.database.backup.mysqlNote')}
      </p>
    {:else if isBackupActive}
      <!-- Backup in progress -->
      <div class="space-y-2">
        <div class="flex items-center justify-between text-sm">
          <span class="text-[var(--color-base-content)]">
            {#if backupStatus === 'pending'}
              {t('system.database.backup.starting')}
            {:else if backupStatus === 'downloading'}
              {t('system.database.backup.downloading')}
            {:else}
              {t('system.database.backup.creating')}
            {/if}
          </span>
          {#if backupStatus !== 'downloading'}
            <button
              class="p-1 text-[var(--color-base-content)]/60 hover:text-[var(--color-error)] transition-colors"
              onclick={cancelBackup}
              title={t('system.database.backup.cancel')}
            >
              <X class="size-4" />
            </button>
          {/if}
        </div>

        <!-- Progress bar -->
        <div class="w-full bg-[var(--color-base-200)] rounded-full h-2 overflow-hidden">
          <div
            class="bg-[var(--color-primary)] h-2 rounded-full transition-all duration-300"
            style:width="{backupStatus === 'downloading' ? 100 : backupProgress}%"
          ></div>
        </div>

        <!-- Progress text -->
        {#if backupStatus !== 'downloading'}
          <div class="flex justify-between text-xs text-[var(--color-base-content)]/70">
            <span>{backupProgress}%</span>
            <span>{formatBytes(backupBytesWritten)} / {formatBytes(backupTotalBytes)}</span>
          </div>
        {/if}
      </div>
    {:else if backupStatus === 'failed'}
      <!-- Backup failed - show retry button -->
      <button
        class="w-full inline-flex items-center justify-center gap-2 px-4 py-2
               text-sm font-medium rounded-lg transition-colors
               border border-[var(--color-error)]
               text-[var(--color-error)]
               hover:bg-[var(--color-error)]/10"
        onclick={retryBackup}
      >
        <Download class="size-4" />
        {t('system.database.backup.retry')}
      </button>
    {:else}
      <!-- Idle state - show download button -->
      <button
        class="w-full inline-flex items-center justify-center gap-2 px-4 py-2
               text-sm font-medium rounded-lg transition-colors
               border border-[var(--color-base-300)]
               text-[var(--color-base-content)]
               hover:bg-[var(--color-base-200)]
               disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={startBackup}
        disabled={isLoading || !canBackup || migrationActive}
      >
        <Download class="size-4" />
        {t('system.database.backup.download')}
      </button>
    {/if}
  </div>
</div>
