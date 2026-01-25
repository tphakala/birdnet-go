<!--
  MigrationControlCard Component

  Displays migration status and provides controls for migration operations.
  Shows progress, state, and action buttons based on current migration state.

  Props:
  - status: Migration status from API
  - isLoading: Whether status is being loaded
  - error: Error message if status fetch failed
  - onStart: Callback to open confirmation dialog
  - onPause: Callback to pause migration
  - onResume: Callback to resume migration
  - onCancel: Callback to cancel migration

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import {
    RefreshCw,
    Play,
    Pause,
    Square,
    CheckCircle2,
    AlertCircle,
    Loader2,
    Info,
  } from '@lucide/svelte';

  interface MigrationStatus {
    state: string;
    total_records: number;
    migrated_records: number;
    progress_percent: number;
    records_per_second?: number;
    estimated_remaining?: string;
    worker_running: boolean;
    worker_paused: boolean;
    can_start: boolean;
    can_pause: boolean;
    can_resume: boolean;
    can_cancel: boolean;
    dirty_id_count: number;
    error_message?: string;
  }

  interface Props {
    status: MigrationStatus | null;
    isLoading: boolean;
    isStarting?: boolean;
    error: string | null;
    onStart: () => void;
    onPause: () => Promise<void>;
    onResume: () => Promise<void>;
    onCancel: () => Promise<void>;
  }

  let {
    status,
    isLoading,
    isStarting = false,
    error,
    onStart,
    onPause,
    onResume,
    onCancel,
  }: Props = $props();

  let showCancelConfirm = $state(false);
  let actionLoading = $state(false);

  // State badge styling
  const stateStyles: Record<string, string> = {
    idle: 'bg-[var(--color-base-200)] text-[var(--color-base-content)]',
    initializing: 'bg-[var(--color-primary)]/10 text-[var(--color-primary)]',
    dual_write: 'bg-[var(--color-warning)]/10 text-[var(--color-warning)]',
    migrating: 'bg-[var(--color-primary)]/10 text-[var(--color-primary)]',
    paused: 'bg-[var(--color-warning)]/10 text-[var(--color-warning)]',
    validating: 'bg-[var(--color-primary)]/10 text-[var(--color-primary)]',
    cutover: 'bg-[var(--color-warning)]/10 text-[var(--color-warning)]',
    completed: 'bg-[var(--color-success)]/10 text-[var(--color-success)]',
  };

  async function handlePause() {
    actionLoading = true;
    try {
      await onPause();
    } finally {
      actionLoading = false;
    }
  }

  async function handleResume() {
    actionLoading = true;
    try {
      await onResume();
    } finally {
      actionLoading = false;
    }
  }

  async function handleCancel() {
    actionLoading = true;
    showCancelConfirm = false;
    try {
      await onCancel();
    } finally {
      actionLoading = false;
    }
  }
</script>

<div class="rounded-lg bg-[var(--color-base-100)] shadow-sm border border-[var(--color-base-200)]">
  <!-- Header -->
  <div class="px-6 py-4 border-b border-[var(--color-base-200)]">
    <div class="flex items-center justify-between">
      <h3 class="text-lg font-semibold flex items-center gap-2 text-[var(--color-base-content)]">
        <RefreshCw class="size-5" />
        {t('system.database.migration.title')}
      </h3>
      {#if status}
        <span
          class="px-2.5 py-1 text-xs font-medium rounded-full {stateStyles[status.state] ||
            stateStyles.idle}"
        >
          {t(`system.database.migration.status.${status.state}`)}
        </span>
      {/if}
    </div>
  </div>

  <!-- Body -->
  <div class="px-6 py-4">
    {#if isLoading}
      <div class="space-y-3">
        <div class="h-4 bg-[var(--color-base-200)] rounded animate-pulse w-full"></div>
        <div class="h-2 bg-[var(--color-base-200)] rounded animate-pulse w-full"></div>
        <div class="h-4 bg-[var(--color-base-200)] rounded animate-pulse w-1/2"></div>
      </div>
    {:else if error}
      <div
        class="p-4 rounded-lg bg-[var(--color-error)]/10 text-[var(--color-error)] flex items-center gap-3"
        role="alert"
        aria-live="assertive"
      >
        <AlertCircle class="size-5 shrink-0" />
        <span>{error}</span>
      </div>
    {:else if status?.state === 'idle'}
      <!-- Idle State -->
      {#if isStarting}
        <!-- Starting indicator -->
        <div class="flex flex-col items-center justify-center py-6 gap-3">
          <Loader2 class="size-8 animate-spin text-[var(--color-primary)]" />
          <span class="text-sm text-[var(--color-base-content)]/70">
            {t('system.database.migration.starting')}
          </span>
        </div>
      {:else}
        <div
          class="p-4 rounded-lg bg-[var(--color-base-200)] text-sm mb-4 text-[var(--color-base-content)]"
        >
          <p>{t('system.database.migration.requiredNote')}</p>
        </div>
        <button
          class="w-full inline-flex items-center justify-center gap-2 px-4 py-2.5
                 text-sm font-medium rounded-lg transition-colors
                 bg-[var(--color-primary)] text-[var(--color-primary-content)]
                 hover:bg-[var(--color-primary)]/90
                 disabled:opacity-50 disabled:cursor-not-allowed"
          onclick={onStart}
          disabled={!status.can_start}
        >
          <Play class="size-4" />
          {t('system.database.migration.actions.start')}
        </button>
      {/if}
    {:else if status?.state === 'completed'}
      <!-- Completed State -->
      <div class="p-4 rounded-lg bg-[var(--color-success)]/10">
        <div class="flex items-center gap-2 text-[var(--color-success)]">
          <CheckCircle2 class="size-5" />
          <span class="font-medium">{t('system.database.migration.completedTitle')}</span>
        </div>
        <p class="mt-2 text-sm text-[var(--color-base-content)]">
          {t('system.database.migration.completedNote')}
        </p>
      </div>
    {:else if status?.state === 'paused'}
      <!-- Paused State -->
      <div class="space-y-4">
        <!-- Friendly status note -->
        <div class="p-3 rounded-lg bg-[var(--color-warning)]/10 flex items-start gap-3">
          <Info class="size-5 shrink-0 text-[var(--color-warning)] mt-0.5" />
          <p class="text-sm text-[var(--color-base-content)]">
            {t('system.database.migration.notes.paused')}
          </p>
        </div>

        <!-- Progress bar -->
        <div>
          <div class="flex justify-between text-sm mb-2 text-[var(--color-base-content)]">
            <span>{t('system.database.migration.progress.paused')}</span>
            <span>{status.progress_percent.toFixed(1)}%</span>
          </div>
          <div class="w-full h-2 bg-[var(--color-base-200)] rounded-full overflow-hidden">
            <div
              class="h-full bg-[var(--color-warning)] rounded-full"
              style:width="{status.progress_percent}%"
            ></div>
          </div>
          <div class="mt-2 text-sm text-[var(--color-base-content)]/70">
            {status.migrated_records.toLocaleString()} / {status.total_records.toLocaleString()}
            {t('system.database.migration.progress.records')}
          </div>
        </div>

        <!-- Action buttons -->
        <div class="flex gap-2">
          <button
            class="flex-1 inline-flex items-center justify-center gap-2 px-4 py-2
                   text-sm font-medium rounded-lg transition-colors
                   bg-[var(--color-primary)] text-[var(--color-primary-content)]
                   hover:bg-[var(--color-primary)]/90
                   disabled:opacity-50 disabled:cursor-not-allowed"
            onclick={handleResume}
            disabled={!status.can_resume || actionLoading}
          >
            <Play class="size-4" />
            {t('system.database.migration.actions.resume')}
          </button>
          <button
            class="flex-1 inline-flex items-center justify-center gap-2 px-4 py-2
                   text-sm font-medium rounded-lg transition-colors
                   bg-[var(--color-error)]/10 text-[var(--color-error)]
                   hover:bg-[var(--color-error)]/20
                   disabled:opacity-50 disabled:cursor-not-allowed"
            onclick={() => (showCancelConfirm = true)}
            disabled={!status.can_cancel || actionLoading}
          >
            <Square class="size-4" />
            {t('system.database.migration.actions.cancel')}
          </button>
        </div>
      </div>
    {:else if status}
      <!-- Active State (migrating, dualWrite, etc.) -->
      <div class="space-y-4">
        <!-- Friendly status note -->
        <div class="p-3 rounded-lg bg-[var(--color-primary)]/10 flex items-start gap-3">
          <Info class="size-5 shrink-0 text-[var(--color-primary)] mt-0.5" />
          <p class="text-sm text-[var(--color-base-content)]">
            {t(`system.database.migration.notes.${status.state}`)}
          </p>
        </div>

        <!-- Progress bar -->
        <div>
          <div class="flex justify-between text-sm mb-2 text-[var(--color-base-content)]">
            <span
              >{status.migrated_records.toLocaleString()} / {status.total_records.toLocaleString()}</span
            >
            <span>{status.progress_percent.toFixed(1)}%</span>
          </div>
          <div class="w-full h-2 bg-[var(--color-base-200)] rounded-full overflow-hidden">
            <div
              class="h-full bg-[var(--color-primary)] transition-all duration-300 rounded-full"
              style:width="{status.progress_percent}%"
            ></div>
          </div>
        </div>

        <!-- Rate and ETA -->
        {#if status.records_per_second && status.records_per_second > 0}
          <div class="flex justify-between text-sm text-[var(--color-base-content)]/70">
            <span
              >{t('system.database.migration.progress.rateValue', {
                rate: status.records_per_second.toFixed(1),
              })}</span
            >
            {#if status.estimated_remaining}
              <span
                >{t('system.database.migration.progress.eta')}: {status.estimated_remaining}</span
              >
            {/if}
          </div>
        {/if}

        <!-- Dirty IDs warning -->
        {#if status.dirty_id_count > 0}
          <div class="text-sm text-[var(--color-warning)]">
            {t('system.database.migration.progress.dirtyIds', { count: status.dirty_id_count })}
          </div>
        {/if}

        <!-- Error message -->
        {#if status.error_message}
          <div
            class="p-3 rounded-lg bg-[var(--color-error)]/10 text-[var(--color-error)] text-sm"
            role="alert"
            aria-live="assertive"
          >
            {status.error_message}
          </div>
        {/if}

        <!-- Action buttons -->
        <div class="flex gap-2">
          {#if status.can_pause}
            <button
              class="flex-1 inline-flex items-center justify-center gap-2 px-4 py-2
                     text-sm font-medium rounded-lg transition-colors
                     border border-[var(--color-base-300)]
                     text-[var(--color-base-content)]
                     hover:bg-[var(--color-base-200)]
                     disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={handlePause}
              disabled={actionLoading}
            >
              <Pause class="size-4" />
              {t('system.database.migration.actions.pause')}
            </button>
          {/if}
          {#if status.can_cancel}
            <button
              class="flex-1 inline-flex items-center justify-center gap-2 px-4 py-2
                     text-sm font-medium rounded-lg transition-colors
                     bg-[var(--color-error)]/10 text-[var(--color-error)]
                     hover:bg-[var(--color-error)]/20
                     disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={() => (showCancelConfirm = true)}
              disabled={actionLoading}
            >
              <Square class="size-4" />
              {t('system.database.migration.actions.cancel')}
            </button>
          {/if}
        </div>
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
      aria-label={t('common.aria.closeModal')}
    ></button>
    <div
      class="relative bg-[var(--color-base-100)] rounded-xl shadow-xl p-6 max-w-md w-full mx-4 z-10"
    >
      <h3 class="text-lg font-semibold text-[var(--color-base-content)]">
        {t('system.database.migration.confirmCancel.title')}
      </h3>
      <p class="mt-2 text-sm text-[var(--color-base-content)]/80">
        {t('system.database.migration.confirmCancel.message')}
      </p>
      <div class="flex justify-end gap-2 mt-6">
        <button
          class="inline-flex items-center justify-center gap-2 px-4 py-2
                 text-sm font-medium rounded-lg transition-colors
                 border border-[var(--color-base-300)]
                 text-[var(--color-base-content)]
                 hover:bg-[var(--color-base-200)]"
          onclick={() => (showCancelConfirm = false)}
        >
          {t('system.database.migration.confirmCancel.dismiss')}
        </button>
        <button
          class="inline-flex items-center justify-center gap-2 px-4 py-2
                 text-sm font-medium rounded-lg transition-colors
                 bg-[var(--color-error)] text-white
                 hover:bg-[var(--color-error)]/90
                 disabled:opacity-50 disabled:cursor-not-allowed"
          onclick={handleCancel}
          disabled={actionLoading}
        >
          {t('system.database.migration.confirmCancel.confirm')}
        </button>
      </div>
    </div>
  </div>
{/if}
