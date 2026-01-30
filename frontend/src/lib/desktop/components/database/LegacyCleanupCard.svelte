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

<div class="rounded-lg bg-[var(--color-base-100)] shadow-sm border border-[var(--color-base-200)]">
  <!-- Header -->
  <div class="px-6 py-4 border-b border-[var(--color-base-200)]">
    <h3 class="text-lg font-semibold text-[var(--color-base-content)]">
      {t('system.database.legacy.cleanup.title')}
    </h3>
  </div>

  <!-- Body -->
  <div class="px-6 py-4">
    {#if isLoading}
      <div class="flex justify-center py-4">
        <span
          class="animate-spin rounded-full border-2 h-8 w-8 border-[var(--color-primary)] border-t-transparent"
        ></span>
      </div>
    {:else if isCompleted}
      <!-- Success state -->
      <div
        class="p-3 rounded-lg bg-[var(--color-success)]/10 text-[var(--color-success)] flex items-start gap-3"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-5 w-5 shrink-0 stroke-current"
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
        <span class="text-sm">
          {t('system.database.legacy.cleanup.success', {
            size: formatBytes(cleanupSpaceReclaimed),
          })}
        </span>
      </div>
    {:else if isFailed}
      <!-- Error state -->
      <div
        class="p-3 rounded-lg bg-[var(--color-error)]/10 text-[var(--color-error)] flex items-start gap-3"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-5 w-5 shrink-0 stroke-current"
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
          <p class="font-medium text-sm">{t('system.database.legacy.cleanup.failed')}</p>
          <p class="text-sm opacity-80">{cleanupError}</p>
        </div>
      </div>
    {:else if status?.exists && status?.can_cleanup}
      <!-- Normal state - show info and cleanup button -->
      <div class="space-y-4">
        <!-- Description -->
        <p class="text-sm text-[var(--color-base-content)]/70">
          {t('system.database.legacy.cleanup.description')}
          <span class="font-medium text-[var(--color-base-content)] ml-1"
            >{t('system.database.legacy.cleanup.recommendation')}</span
          >
        </p>

        <!-- Database info -->
        <div class="space-y-2">
          <div class="flex justify-between text-sm">
            <span class="text-[var(--color-base-content)]/70">
              {t('system.database.legacy.cleanup.location')}
            </span>
            <span
              class="font-mono text-xs text-[var(--color-base-content)] truncate max-w-[200px]"
              title={status.location}
            >
              {status.location}
            </span>
          </div>

          <div class="flex justify-between text-sm">
            <span class="text-[var(--color-base-content)]/70">
              {t('system.database.legacy.cleanup.size')}
            </span>
            <span class="font-medium text-[var(--color-base-content)]"
              >{formatBytes(status.size_bytes)}</span
            >
          </div>

          {#if status.total_records > 0}
            <div class="flex justify-between text-sm">
              <span class="text-[var(--color-base-content)]/70">
                {t('system.database.legacy.cleanup.records')}
              </span>
              <span class="font-medium text-[var(--color-base-content)]">
                {status.total_records.toLocaleString()}
                {t('system.database.legacy.cleanup.detections')}
              </span>
            </div>
          {/if}

          {#if status.last_modified}
            <div class="flex justify-between text-sm">
              <span class="text-[var(--color-base-content)]/70">
                {t('system.database.legacy.cleanup.lastModified')}
              </span>
              <span class="font-medium text-[var(--color-base-content)]"
                >{formatDate(status.last_modified)}</span
              >
            </div>
          {/if}
        </div>

        <!-- Warning -->
        <div
          class="p-3 rounded-lg bg-[var(--color-warning)]/10 text-[var(--color-warning)] flex items-start gap-3"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-5 w-5 shrink-0 stroke-current"
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
            <p class="font-medium text-sm">{t('system.database.legacy.cleanup.warning')}</p>
            <p class="text-sm opacity-80">{t('system.database.legacy.cleanup.backupReminder')}</p>
          </div>
        </div>
      </div>
    {:else if status?.exists && !status?.can_cleanup}
      <!-- Cannot cleanup - show reason -->
      <div
        class="p-3 rounded-lg bg-[var(--color-info)]/10 text-[var(--color-info)] flex items-start gap-3"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-5 w-5 shrink-0 stroke-current"
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
          <p class="font-medium text-sm">{t('system.database.legacy.cleanup.notAvailable')}</p>
          <p class="text-sm opacity-80">{status.reason}</p>
        </div>
      </div>
    {/if}
  </div>

  <!-- Footer with action button -->
  {#if status?.exists && status?.can_cleanup && !isCompleted && !isFailed}
    <div class="px-6 py-4 border-t border-[var(--color-base-200)]">
      <button
        class="w-full inline-flex items-center justify-center gap-2 px-4 py-2
               text-sm font-medium rounded-lg transition-colors
               bg-[var(--color-error)] text-white
               hover:bg-[var(--color-error)]/90
               disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={onDelete}
        disabled={isInProgress}
      >
        {#if isInProgress}
          <span
            class="animate-spin rounded-full border-2 h-4 w-4 border-current border-t-transparent"
          ></span>
          {t('system.database.legacy.cleanup.inProgress')}
        {:else}
          {t('system.database.legacy.cleanup.deleteButton')}
        {/if}
      </button>
    </div>
  {/if}
</div>
