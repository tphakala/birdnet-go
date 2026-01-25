<!--
  DatabaseStatsCard Component

  Displays database statistics with backup download capability.
  Shows type, location, size, detections count, and connection status.

  Props:
  - title: Card title (e.g., "Legacy Database" or "V2 Database")
  - dbType: 'legacy' | 'v2' - For API calls
  - stats: Database stats from API or null
  - isLoading: Whether stats are being loaded
  - error: Error message if stats fetch failed

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { getCsrfToken } from '$lib/utils/api';
  import { formatBytes } from '$lib/utils/formatters';
  import { Database, Download, Loader2 } from '@lucide/svelte';

  interface DatabaseStats {
    type: string;
    location: string;
    size_bytes: number;
    total_detections: number;
    connected: boolean;
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

  let isDownloading = $state(false);
  let downloadError = $state<string | null>(null);

  // Check if backup is available (SQLite only)
  let canBackup = $derived(stats?.type === 'sqlite' && stats?.connected);

  async function handleBackupDownload() {
    if (!canBackup) return;

    isDownloading = true;
    downloadError = null;

    try {
      const response = await fetch(`/api/v2/system/database/backup?type=${dbType}`, {
        method: 'POST',
        headers: {
          'X-CSRF-Token': getCsrfToken() ?? '',
        },
      });

      if (!response.ok) {
        const errorData = await response.json().catch(() => ({}));
        throw new Error(errorData.message || 'Backup failed');
      }

      // Trigger browser download
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `birdnet-${dbType}-backup-${Date.now()}.db`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      downloadError = e instanceof Error ? e.message : 'Backup failed';
    } finally {
      isDownloading = false;
    }
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
      <div class="p-3 rounded-lg bg-[var(--color-error)]/10 text-[var(--color-error)] text-sm">
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
    {#if downloadError}
      <div class="mb-3 p-2 rounded text-xs bg-[var(--color-error)]/10 text-[var(--color-error)]">
        {downloadError}
      </div>
    {/if}

    {#if stats?.type === 'mysql'}
      <p class="text-xs text-[var(--color-base-content)]/60 text-center">
        {t('system.database.backup.mysqlNote')}
      </p>
    {:else}
      <button
        class="w-full inline-flex items-center justify-center gap-2 px-4 py-2
               text-sm font-medium rounded-lg transition-colors
               border border-[var(--color-base-300)]
               text-[var(--color-base-content)]
               hover:bg-[var(--color-base-200)]
               disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={handleBackupDownload}
        disabled={isLoading || !canBackup || isDownloading || migrationActive}
      >
        {#if isDownloading}
          <Loader2 class="size-4 animate-spin" />
          {t('system.database.backup.downloading')}
        {:else}
          <Download class="size-4" />
          {t('system.database.backup.download')}
        {/if}
      </button>
    {/if}
  </div>
</div>
