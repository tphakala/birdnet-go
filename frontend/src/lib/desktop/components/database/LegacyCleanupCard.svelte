<!--
  LegacyCleanupCard Component

  Displays legacy database information and cleanup controls.
  Only shown in v2-only mode when legacy database exists.

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import type { LegacyStatus } from '$lib/types/legacy';
  import { formatBytes, formatDate } from '$lib/utils/formatters';

  interface Props {
    status: LegacyStatus | null;
    cleanupState: string;
    cleanupError: string;
    cleanupSpaceReclaimed: number;
    isLoading: boolean;
    onDelete: () => void;
  }

  let { status, cleanupState, cleanupError, cleanupSpaceReclaimed, isLoading, onDelete }: Props =
    $props();

  // Derived state
  let isInProgress = $derived(cleanupState === 'in_progress');
  let isCompleted = $derived(cleanupState === 'completed');
  let isFailed = $derived(cleanupState === 'failed');
</script>

<div class="card bg-base-100 shadow-lg">
  <div class="card-body">
    <h2 class="card-title text-lg">
      {t('system.database.legacy.cleanup.title')}
    </h2>

    {#if isLoading}
      <div class="flex justify-center py-8">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if isCompleted}
      <!-- Success state -->
      <div class="alert alert-success">
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 shrink-0 stroke-current"
          fill="none"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"
          />
        </svg>
        <span>
          {t('system.database.legacy.cleanup.success', {
            size: formatBytes(cleanupSpaceReclaimed),
          })}
        </span>
      </div>
    {:else if isFailed}
      <!-- Error state -->
      <div class="alert alert-error">
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 shrink-0 stroke-current"
          fill="none"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z"
          />
        </svg>
        <div>
          <p class="font-semibold">{t('system.database.legacy.cleanup.failed')}</p>
          <p class="text-sm">{cleanupError}</p>
        </div>
      </div>
    {:else if status?.exists && status?.can_cleanup}
      <!-- Normal state - show info and cleanup button -->
      <div class="space-y-4">
        <!-- Database info -->
        <div class="grid grid-cols-2 gap-2 text-sm">
          <div class="text-base-content/70">{t('system.database.legacy.cleanup.location')}</div>
          <div class="font-mono text-xs break-all">{status.location}</div>

          <div class="text-base-content/70">{t('system.database.legacy.cleanup.size')}</div>
          <div>{formatBytes(status.size_bytes)}</div>

          {#if status.total_records > 0}
            <div class="text-base-content/70">{t('system.database.legacy.cleanup.records')}</div>
            <div>
              {status.total_records.toLocaleString()}
              {t('system.database.legacy.cleanup.detections')}
            </div>
          {/if}

          {#if status.last_modified}
            <div class="text-base-content/70">
              {t('system.database.legacy.cleanup.lastModified')}
            </div>
            <div>{formatDate(status.last_modified)}</div>
          {/if}
        </div>

        <!-- Warning -->
        <div class="alert alert-warning">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-6 w-6 shrink-0 stroke-current"
            fill="none"
            viewBox="0 0 24 24"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
            />
          </svg>
          <div>
            <p class="font-semibold">{t('system.database.legacy.cleanup.warning')}</p>
            <p class="text-sm">{t('system.database.legacy.cleanup.backupReminder')}</p>
          </div>
        </div>

        <!-- Cleanup button -->
        <div class="card-actions justify-end">
          <button class="btn btn-error" onclick={onDelete} disabled={isInProgress}>
            {#if isInProgress}
              <span class="loading loading-spinner loading-sm"></span>
              {t('system.database.legacy.cleanup.inProgress')}
            {:else}
              {t('system.database.legacy.cleanup.deleteButton')}
            {/if}
          </button>
        </div>
      </div>
    {:else if status?.exists && !status?.can_cleanup}
      <!-- Cannot cleanup - show reason -->
      <div class="alert alert-info">
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 shrink-0 stroke-current"
          fill="none"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
          />
        </svg>
        <div>
          <p class="font-semibold">{t('system.database.legacy.cleanup.notAvailable')}</p>
          <p class="text-sm">{status.reason}</p>
        </div>
      </div>
    {/if}
  </div>
</div>
