<script lang="ts">
  import {
    Database,
    Info,
    Loader2,
    Pause,
    Play,
    Square,
    RotateCcw,
    CheckCircle,
    CircleAlert,
    AlertTriangle,
  } from '@lucide/svelte';
  import { formatBytesCompact, formatNumber } from '$lib/utils/formatters';
  import { t } from '$lib/i18n';
  import type { MigrationStatus, DatabaseStats, PrerequisitesResponse } from '$lib/types/migration';

  interface Props {
    status: MigrationStatus | null;
    legacyStats: DatabaseStats | null;
    v2Stats: DatabaseStats | null;
    prerequisites: PrerequisitesResponse | null;
    isStarting?: boolean;
    onStart: () => void;
    onPause: () => Promise<void>;
    onResume: () => Promise<void>;
    onRetryValidation: () => Promise<void>;
    onCancel: () => Promise<void>;
  }

  let {
    status,
    legacyStats,
    v2Stats,
    prerequisites,
    isStarting = false,
    onStart,
    onPause,
    onResume,
    onRetryValidation,
    onCancel,
  }: Props = $props();

  // Derived state helpers
  let migState = $derived(status?.state ?? 'idle');
  let isIdle = $derived(migState === 'idle');
  let isCompleted = $derived(migState === 'completed');
  let isFailed = $derived(migState === 'failed');
  let isValidating = $derived(migState === 'validating');
  let isCutover = $derived(migState === 'cutover');
  let isPaused = $derived(status?.worker_paused === true);
  let banner = $derived(bannerMessage(migState));

  /** Average bytes per detection record in v2 database, used to estimate v2 DB size during migration */
  const ESTIMATED_V2_RECORD_SIZE_BYTES = 180;

  let phaseName = $derived(
    status?.current_phase
      ? status.current_phase.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase())
      : ''
  );

  function stateLabel(state: string): string {
    const key = `system.database.migration.status.${state}`;
    const translated = t(key);
    return translated === key
      ? state.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase())
      : translated;
  }

  function stateBadgeClass(state: string): string {
    switch (state) {
      case 'idle':
        return 'bg-slate-500/10 text-slate-600 dark:text-slate-400';
      case 'initializing':
      case 'dual_write':
      case 'migrating':
        return 'bg-blue-500/10 text-blue-600 dark:text-blue-400';
      case 'validating':
        return 'bg-violet-500/10 text-violet-600 dark:text-violet-400';
      case 'cutover':
        return 'bg-amber-500/10 text-amber-600 dark:text-amber-400';
      case 'completed':
        return 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400';
      case 'failed':
        return 'bg-red-500/10 text-red-600 dark:text-red-400';
      default:
        return 'bg-slate-500/10 text-slate-600 dark:text-slate-400';
    }
  }

  function bannerMessage(state: string): string {
    if (isPaused) return t('system.database.migration.notes.paused');
    const key = `system.database.migration.notes.${state}`;
    const translated = t(key);
    return translated === key ? '' : translated;
  }
</script>

<!-- Migration Progress Card (2 cols wide) -->
<div
  class="lg:col-span-2 bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm"
>
  <!-- Header -->
  <div class="flex items-center justify-between mb-3">
    <h3 class="text-xs font-semibold uppercase tracking-wider text-slate-400 dark:text-slate-500">
      {t('system.database.migration.progress.title')}
    </h3>
    <div class="flex items-center gap-2">
      {#if status?.current_phase && !isIdle && !isCompleted}
        <span class="text-[10px] tabular-nums text-slate-400 dark:text-slate-500">
          {t('system.database.migration.phase.indicator', {
            current: status.phase_number,
            total: status.total_phases,
          })}
        </span>
        <span class="px-1.5 py-0.5 rounded text-[10px] font-medium {stateBadgeClass(migState)}">
          {phaseName}
        </span>
      {/if}
      <span
        class="px-1.5 py-0.5 rounded text-[10px] font-medium {stateBadgeClass(
          isPaused ? 'paused' : migState
        )}"
      >
        {isPaused ? t('system.database.migration.status.paused') : stateLabel(migState)}
      </span>
    </div>
  </div>

  <!-- STATE: Completed -->
  {#if isCompleted}
    <div class="flex items-start gap-3 p-4 rounded-lg bg-emerald-500/10" role="status">
      <CheckCircle class="w-5 h-5 text-emerald-500 flex-shrink-0 mt-0.5" />
      <div>
        <p class="text-sm font-semibold text-emerald-700 dark:text-emerald-300">
          {t('system.database.migration.progress.completedTitle')}
        </p>
        <p class="text-xs mt-1 text-slate-500 dark:text-slate-400">
          {t('system.database.migration.progress.completedBody', {
            count: formatNumber(status?.total_records ?? 0),
          })}
        </p>
      </div>
    </div>

    <!-- Restart prompt -->
    <div class="mt-4 pt-4 border-t border-[var(--border-100)]">
      <div class="flex items-start gap-3 p-3 rounded-lg bg-blue-500/10">
        <Info class="w-4 h-4 text-blue-500 flex-shrink-0 mt-0.5" />
        <div class="flex-1">
          <p class="text-sm font-medium text-blue-700 dark:text-blue-300">
            {t('system.database.migration.progress.restartTitle')}
          </p>
          <p class="text-xs mt-0.5 text-slate-500 dark:text-slate-400">
            {t('system.database.migration.progress.restartBody')}
          </p>
        </div>
      </div>
    </div>

    <!-- STATE: Failed -->
  {:else if isFailed}
    <div class="flex items-start gap-3 p-4 rounded-lg bg-red-500/10" role="alert">
      <CircleAlert class="w-5 h-5 text-red-500 flex-shrink-0 mt-0.5" />
      <div class="flex-1">
        <p class="text-sm font-semibold text-red-700 dark:text-red-300">
          {t('system.database.migration.status.failed')}
        </p>
        <p class="text-xs mt-1 text-slate-500 dark:text-slate-400">
          {status?.error_message ?? t('system.database.migration.progress.fallbackError')}
        </p>
        {#if status && status.dirty_id_count > 0}
          <p class="text-xs mt-1 font-medium text-amber-600 dark:text-amber-400">
            {t('system.database.migration.progress.dirtyIds', { count: status.dirty_id_count })}
          </p>
        {/if}
      </div>
    </div>

    <!-- Retry / Cancel buttons -->
    <div class="flex gap-2 mt-4">
      {#if status?.can_retry_validation}
        <button
          onclick={onRetryValidation}
          class="flex-1 flex items-center justify-center gap-2 px-3 py-2 text-sm font-medium rounded-lg cursor-pointer bg-violet-500/10 text-violet-600 dark:text-violet-400 hover:bg-violet-500/20 transition-colors"
        >
          <RotateCcw class="w-3.5 h-3.5" />
          {t('system.database.migration.actions.retryValidation')}
        </button>
      {/if}
      {#if status?.can_cancel}
        <button
          onclick={onCancel}
          class="flex-1 flex items-center justify-center gap-2 px-3 py-2 text-sm font-medium rounded-lg cursor-pointer bg-red-500/10 text-red-600 dark:text-red-400 hover:bg-red-500/20 transition-colors"
        >
          <Square class="w-3.5 h-3.5" />
          {t('system.database.migration.actions.cancel')}
        </button>
      {/if}
    </div>

    <!-- STATE: Idle (pre-migration) -->
  {:else if isIdle}
    <div
      class="flex items-start gap-2 p-3 rounded-lg mb-4 text-xs bg-black/[0.03] dark:bg-white/[0.03] text-slate-500 dark:text-slate-400"
    >
      <Info class="w-3.5 h-3.5 flex-shrink-0 mt-0.5" />
      <span>
        {t('system.database.migration.progress.migrateInfo', {
          count: formatNumber(legacyStats?.total_detections ?? 0),
        })}
      </span>
    </div>

    <!-- Source / Target preview -->
    <div class="grid grid-cols-2 gap-4 mb-4">
      <div class="p-3 rounded-lg bg-black/[0.03] dark:bg-white/[0.03]">
        <div class="flex items-center gap-2 mb-2">
          <Database class="w-3.5 h-3.5 text-amber-500" />
          <span class="text-xs font-medium"
            >{t('system.database.migration.progress.sourceLegacy')}</span
          >
        </div>
        <div class="space-y-1">
          <div class="flex justify-between text-xs">
            <span class="text-slate-400 dark:text-slate-500"
              >{t('system.database.migration.progress.recordsLabel')}</span
            >
            <span class="tabular-nums font-medium"
              >{formatNumber(legacyStats?.total_detections ?? 0)}</span
            >
          </div>
          <div class="flex justify-between text-xs">
            <span class="text-slate-400 dark:text-slate-500"
              >{t('system.database.migration.progress.sizeLabel')}</span
            >
            <span class="tabular-nums font-medium"
              >{formatBytesCompact(legacyStats?.size_bytes ?? 0)}</span
            >
          </div>
        </div>
      </div>
      <div class="p-3 rounded-lg bg-black/[0.03] dark:bg-white/[0.03]">
        <div class="flex items-center gap-2 mb-2">
          <Database class="w-3.5 h-3.5 text-emerald-500" />
          <span class="text-xs font-medium">{t('system.database.migration.progress.targetV2')}</span
          >
        </div>
        <div class="space-y-1">
          <div class="flex justify-between text-xs">
            <span class="text-slate-400 dark:text-slate-500"
              >{t('system.database.migration.progress.recordsLabel')}</span
            >
            <span class="tabular-nums font-medium"
              >{formatNumber(v2Stats?.total_detections ?? 0)}</span
            >
          </div>
          <div class="flex justify-between text-xs">
            <span class="text-slate-400 dark:text-slate-500"
              >{t('system.database.migration.progress.sizeLabel')}</span
            >
            <span class="tabular-nums font-medium"
              >{formatBytesCompact(v2Stats?.size_bytes ?? 0)}</span
            >
          </div>
        </div>
      </div>
    </div>

    <!-- Start button -->
    {#if prerequisites?.can_start_migration}
      <button
        onclick={onStart}
        disabled={isStarting}
        class="w-full flex items-center justify-center gap-2 px-4 py-2.5 text-sm font-medium rounded-lg cursor-pointer bg-emerald-500 text-white hover:bg-emerald-600 transition-colors disabled:opacity-60 disabled:cursor-not-allowed"
      >
        {#if isStarting}
          <Loader2 class="w-4 h-4 animate-spin" />
          {t('system.database.migration.starting')}
        {:else}
          <Play class="w-4 h-4" />
          {t('system.database.migration.actions.start')}
        {/if}
      </button>
    {:else}
      <button
        class="w-full flex items-center justify-center gap-2 px-4 py-2.5 text-sm font-medium rounded-lg bg-slate-400/50 text-slate-500 cursor-not-allowed"
        disabled
      >
        <Play class="w-4 h-4" />
        {t('system.database.migration.actions.start')}
      </button>
      <p class="text-[10px] text-center mt-1.5 text-red-500">
        {t('system.database.migration.progress.prerequisitesNotMet')}
      </p>
    {/if}

    <!-- STATE: Validating -->
  {:else if isValidating}
    <div
      class="flex items-start gap-2 p-2.5 rounded-lg mb-4 text-xs bg-violet-500/10 text-violet-700 dark:text-violet-300"
    >
      <Loader2 class="w-3.5 h-3.5 flex-shrink-0 mt-0.5 animate-spin" />
      <span>{bannerMessage(migState)}</span>
    </div>

    <!-- Validation in progress -->
    <div class="p-4 rounded-lg text-center bg-black/[0.03] dark:bg-white/[0.03]">
      <Loader2 class="w-6 h-6 mx-auto mb-2 text-violet-500 animate-spin" />
      <p class="text-sm font-medium">{t('system.database.migration.progress.validatingTitle')}</p>
      <p class="text-xs mt-1 text-slate-400 dark:text-slate-500">
        {t('system.database.migration.progress.validatingBody', {
          count: formatNumber(legacyStats?.total_detections ?? 0),
        })}
      </p>

      <!-- Indeterminate progress bar -->
      <div
        class="h-2 rounded-full overflow-hidden mt-3 mx-auto max-w-xs bg-black/[0.03] dark:bg-white/[0.03]"
        role="progressbar"
        aria-label={t('system.database.migration.progress.validatingTitle')}
      >
        <div class="h-full w-1/3 rounded-full bg-violet-500 animate-validating"></div>
      </div>

      {#if status && status.dirty_id_count > 0}
        <p class="text-xs mt-2 text-amber-600 dark:text-amber-400">
          {t('system.database.migration.progress.dirtyRecordsCatchup', {
            count: status.dirty_id_count,
          })}
        </p>
      {/if}
    </div>

    {#if status?.can_cancel}
      <div class="flex justify-center mt-4">
        <button
          onclick={onCancel}
          class="flex items-center gap-2 px-3 py-2 text-sm font-medium rounded-lg cursor-pointer bg-red-500/10 text-red-600 dark:text-red-400 hover:bg-red-500/20 transition-colors"
        >
          <Square class="w-3.5 h-3.5" />
          {t('system.database.migration.actions.cancel')}
        </button>
      </div>
    {/if}

    <!-- STATE: Cutover -->
  {:else if isCutover}
    <div
      class="flex items-start gap-2 p-2.5 rounded-lg mb-4 text-xs bg-amber-500/10 text-amber-700 dark:text-amber-300"
    >
      <Loader2 class="w-3.5 h-3.5 flex-shrink-0 mt-0.5 animate-spin" />
      <span>{bannerMessage(migState)}</span>
    </div>

    <div class="p-4 rounded-lg text-center bg-black/[0.03] dark:bg-white/[0.03]">
      <Loader2 class="w-6 h-6 mx-auto mb-2 text-amber-500 animate-spin" />
      <p class="text-sm font-medium">{t('system.database.migration.progress.cutoverTitle')}</p>
      <p class="text-xs mt-1 text-slate-400 dark:text-slate-500">
        {t('system.database.migration.progress.cutoverBody')}
      </p>
    </div>

    <!-- STATES: Active (initializing / dual_write / migrating) or Paused -->
  {:else}
    <!-- State-aware info banner -->
    {#if banner}
      <div
        class="flex items-start gap-2 p-2.5 rounded-lg mb-4 text-xs {isPaused
          ? 'bg-amber-500/10 text-amber-700 dark:text-amber-300'
          : 'bg-blue-500/10 text-blue-700 dark:text-blue-300'}"
      >
        {#if isPaused}
          <Pause class="w-3.5 h-3.5 flex-shrink-0 mt-0.5" />
        {:else}
          <Info class="w-3.5 h-3.5 flex-shrink-0 mt-0.5" />
        {/if}
        <span>{banner}</span>
      </div>
    {/if}

    <!-- Progress visualization -->
    <div class="space-y-4">
      <!-- Phase indicator -->
      {#if status?.current_phase}
        <div class="flex items-center gap-2">
          {#if !isPaused}
            <Loader2 class="w-3.5 h-3.5 text-blue-500 animate-spin" />
          {:else}
            <Pause class="w-3.5 h-3.5 text-amber-500" />
          {/if}
          <span class="text-sm font-medium">
            {t('system.database.migration.progress.migratingPhase', { phase: phaseName })}
          </span>
          <span class="text-[10px] tabular-nums text-slate-400 dark:text-slate-500">
            {t('system.database.migration.phase.indicator', {
              current: status.phase_number,
              total: status.total_phases,
            })}
          </span>
        </div>
      {/if}

      <!-- Progress bar -->
      <div>
        <div class="flex items-center justify-between mb-1.5">
          <span class="text-sm font-medium"
            >{t('system.database.migration.progress.progressLabel')}</span
          >
          <div class="text-sm tabular-nums">
            <span class="font-semibold">{formatNumber(status?.migrated_records ?? 0)}</span>
            <span class="text-slate-400 dark:text-slate-500">
              {t('system.database.migration.progress.ofRecords', {
                count: formatNumber(status?.total_records ?? 0),
              })}</span
            >
          </div>
        </div>
        <div
          class="h-3 rounded-full overflow-hidden bg-black/[0.03] dark:bg-white/[0.03]"
          role="progressbar"
          aria-label={t('system.database.migration.progress.title')}
          aria-valuenow={Math.round(Math.min(status?.progress_percent ?? 0, 100))}
          aria-valuemin={0}
          aria-valuemax={100}
        >
          <div
            class="h-full rounded-full transition-all duration-300 {isPaused
              ? 'bg-amber-500'
              : 'bg-blue-500'}"
            style:width="{Math.min(status?.progress_percent ?? 0, 100).toFixed(1)}%"
          ></div>
        </div>
        <div
          class="grid grid-cols-3 mt-1.5 text-[10px] tabular-nums text-slate-400 dark:text-slate-500"
        >
          <span
            >{t('system.database.migration.progress.percentComplete', {
              percent: (status?.progress_percent ?? 0).toFixed(1),
            })}</span
          >
          <span class="text-center">
            {isPaused
              ? '\u2014'
              : t('system.database.migration.progress.recordsPerSec', {
                  rate: formatNumber(Math.round(status?.records_per_second ?? 0)),
                })}
          </span>
          <span class="text-right">
            {#if isPaused}
              {t('system.database.migration.status.paused')}
            {:else if status?.estimated_remaining}
              {t('system.database.migration.progress.remaining', {
                time: status.estimated_remaining,
              })}
            {:else}
              {t('system.database.migration.progress.calculating')}
            {/if}
          </span>
        </div>
      </div>

      <!-- Dirty ID warning -->
      {#if status && status.dirty_id_count > 0}
        <div class="flex items-center gap-2 px-3 py-2 rounded-lg bg-amber-500/10 text-xs">
          <AlertTriangle class="w-3.5 h-3.5 text-amber-500 flex-shrink-0" />
          <span class="text-amber-700 dark:text-amber-300">
            {t('system.database.migration.progress.dirtyWriteFailed', {
              count: status.dirty_id_count,
            })}
          </span>
        </div>
      {/if}

      <!-- Source / Target comparison -->
      <div class="pt-3 border-t border-[var(--border-100)]">
        <div class="grid grid-cols-2 gap-4">
          <div class="p-3 rounded-lg bg-black/[0.03] dark:bg-white/[0.03]">
            <div class="flex items-center gap-2 mb-2">
              <Database class="w-3.5 h-3.5 text-amber-500" />
              <span class="text-xs font-medium"
                >{t('system.database.migration.progress.sourceLegacy')}</span
              >
            </div>
            <div class="space-y-1">
              <div class="flex justify-between text-xs">
                <span class="text-slate-400 dark:text-slate-500"
                  >{t('system.database.migration.progress.recordsLabel')}</span
                >
                <span class="tabular-nums font-medium"
                  >{formatNumber(legacyStats?.total_detections ?? 0)}</span
                >
              </div>
              <div class="flex justify-between text-xs">
                <span class="text-slate-400 dark:text-slate-500"
                  >{t('system.database.migration.progress.sizeLabel')}</span
                >
                <span class="tabular-nums font-medium"
                  >{formatBytesCompact(legacyStats?.size_bytes ?? 0)}</span
                >
              </div>
            </div>
          </div>

          <div class="p-3 rounded-lg bg-black/[0.03] dark:bg-white/[0.03]">
            <div class="flex items-center gap-2 mb-2">
              <Database class="w-3.5 h-3.5 text-emerald-500" />
              <span class="text-xs font-medium"
                >{t('system.database.migration.progress.targetV2')}</span
              >
            </div>
            <div class="space-y-1">
              <div class="flex justify-between text-xs">
                <span class="text-slate-400 dark:text-slate-500"
                  >{t('system.database.migration.progress.recordsLabel')}</span
                >
                <span class="tabular-nums font-medium"
                  >{formatNumber(
                    (v2Stats?.total_detections ?? 0) + (status?.migrated_records ?? 0)
                  )}</span
                >
              </div>
              <div class="flex justify-between text-xs">
                <span class="text-slate-400 dark:text-slate-500"
                  >{t('system.database.migration.progress.sizeLabel')}</span
                >
                <span class="tabular-nums font-medium"
                  >{formatBytesCompact(
                    (v2Stats?.size_bytes ?? 0) +
                      (status?.migrated_records ?? 0) * ESTIMATED_V2_RECORD_SIZE_BYTES
                  )}</span
                >
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- State-specific action buttons -->
      <div class="flex gap-2">
        {#if isPaused}
          {#if status?.can_resume}
            <button
              onclick={onResume}
              class="flex-1 flex items-center justify-center gap-2 px-3 py-2 text-sm font-medium rounded-lg cursor-pointer bg-emerald-500 text-white hover:bg-emerald-600 transition-colors"
            >
              <Play class="w-3.5 h-3.5" />
              {t('system.database.migration.actions.resume')}
            </button>
          {/if}
        {:else if status?.can_pause}
          <button
            onclick={onPause}
            class="flex-1 flex items-center justify-center gap-2 px-3 py-2 text-sm font-medium rounded-lg transition-colors border border-[var(--border-100)] cursor-pointer hover:bg-black/5 dark:hover:bg-white/5"
          >
            <Pause class="w-3.5 h-3.5" />
            {t('system.database.migration.actions.pause')}
          </button>
        {/if}
        {#if status?.can_cancel}
          <button
            onclick={onCancel}
            class="flex-1 flex items-center justify-center gap-2 px-3 py-2 text-sm font-medium rounded-lg transition-colors cursor-pointer bg-red-500/10 text-red-600 dark:text-red-400 hover:bg-red-500/20"
          >
            <Square class="w-3.5 h-3.5" />
            {t('system.database.migration.actions.cancel')}
          </button>
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  @keyframes validating {
    0% {
      transform: translateX(-100%);
    }

    100% {
      transform: translateX(400%);
    }
  }

  .animate-validating {
    animation: validating 1.5s ease-in-out infinite;
  }
</style>
