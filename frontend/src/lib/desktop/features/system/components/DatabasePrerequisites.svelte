<!--
  DatabasePrerequisites Component

  Prerequisites panel for database migration. Displays summary badges in the
  header, always lists every failed, errored or warning check, and keeps only
  the passed and skipped checks behind the details toggle. Without data it
  shows a loading row or the request error with a Retry, never an empty body.

  @component
-->
<script lang="ts">
  import {
    RefreshCw,
    ChevronDown,
    ChevronUp,
    Check,
    CircleAlert,
    AlertTriangle,
    Info,
  } from '@lucide/svelte';
  import { tick } from 'svelte';
  import { t } from '$lib/i18n';
  import {
    isBlockingCheck,
    elapsedSeconds,
    failedRunMessage,
  } from '$lib/utils/migrationPrerequisites';
  import { generateId } from '$lib/utils/uuid';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import type {
    PrerequisitesResponse,
    PrerequisiteCheck,
    PrerequisiteCheckStatus,
  } from '$lib/types/migration';

  interface Props {
    prerequisites: PrerequisitesResponse | null;
    isLoading?: boolean;
    /** Message of the failed prerequisites request, or null. */
    error?: string | null;
    onRefresh: () => Promise<void>;
  }

  let { prerequisites, isLoading = false, error = null, onRefresh }: Props = $props();

  let showDetails = $state(false);
  let isRefreshing = $state(false);
  let statusRegion = $state<HTMLDivElement>();
  let titleElement = $state<HTMLElement>();
  let retryButton = $state<HTMLButtonElement>();
  /** Result of the last successful check run, read out by the live region. */
  let announcement = $state('');

  /** Checks array with null-safety (Go may serialize nil slice as null) */
  let checks = $derived(prerequisites?.checks ?? []);

  /** A run is in progress on the server: the checks arrive one by one. */
  let isRunning = $derived(prerequisites?.status === 'running');
  let progress = $derived(prerequisites?.progress);
  const RUNNING_ROW_ID = generateId('prerequisites-running');
  const RERUN_REASON_ID = generateId('prerequisites-rerun-reason');

  /** Re-run is unavailable while a request or a run is in progress. */
  let rerunBlocked = $derived(isLoading || isRefreshing || isRunning);
  /** Element that says why Re-run is blocked: the running row, or a hidden line for a bare request. */
  let rerunReasonId = $derived(
    isRunning ? RUNNING_ROW_ID : rerunBlocked ? RERUN_REASON_ID : undefined
  );

  /** The run itself did not finish; its checks are partial. */
  let runFailure = $derived(failedRunMessage(prerequisites));

  /** The alert shows when a request failed, a run failed or there is nothing else to show, except during the first load. */
  let showError = $derived(
    !(isLoading && !prerequisites) && (!!error || !!runFailure || !prerequisites)
  );
  let errorText = $derived(
    error || runFailure || t('system.database.migration.prerequisites.errors.fetchFailed')
  );

  /** Passed and skipped checks, the only ones that sit behind the details toggle. */
  const isQuiet = (c: PrerequisiteCheck): boolean =>
    c.status === 'passed' || c.status === 'skipped';

  /** Failed and errored checks first, then warnings, in the server's order otherwise. */
  let nonPassingChecks = $derived(
    checks
      .filter(c => !isQuiet(c))
      .sort((a, b) => Number(a.status === 'warning') - Number(b.status === 'warning'))
  );

  let quietChecks = $derived(checks.filter(isQuiet));

  /** Number of checks that passed */
  let passedCount = $derived(checks.filter(c => c.status === 'passed').length);

  /** Total number of checks */
  let totalCount = $derived(checks.length);

  /** Status icon component mapping */
  const statusIconMap: Record<PrerequisiteCheckStatus, typeof Check> = {
    passed: Check,
    failed: CircleAlert,
    error: CircleAlert,
    warning: AlertTriangle,
    skipped: Info,
  };

  /** Status icon color classes */
  const statusIconColor: Record<PrerequisiteCheckStatus, string> = {
    passed: 'text-[var(--color-success)]',
    failed: 'text-[var(--color-error)]',
    error: 'text-[var(--color-error)]',
    warning: 'text-[var(--color-warning)]',
    skipped: 'text-muted',
  };

  /** Status badge color classes */
  const statusBadgeColor: Record<PrerequisiteCheckStatus, string> = {
    passed: 'badge-status-success',
    failed: 'badge-status-error',
    error: 'badge-status-error',
    warning: 'badge-status-warning',
    skipped: 'bg-slate-500/10 text-muted',
  };

  function statusLabel(status: string): string {
    const key = `system.database.migration.prerequisites.status.${status}`;
    const translated = t(key);
    return translated === key
      ? status.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase())
      : translated;
  }

  /** One sentence for a screen reader once a check run has produced data. */
  function resultAnnouncement(): string {
    if (prerequisites?.status !== 'completed') return '';
    if (prerequisites.critical_failures > 0) {
      return t('system.database.migration.prerequisites.criticalIssues', {
        count: prerequisites.critical_failures,
      });
    }
    // all_passed only means no critical check failed, so warnings must be reported before it
    if (prerequisites.warnings > 0) {
      return t('system.database.migration.prerequisites.warningsCount', {
        count: prerequisites.warnings,
      });
    }
    if (prerequisites.can_start_migration !== true) {
      return t('system.database.migration.status.notReady');
    }
    return t('system.database.migration.prerequisites.allPassed');
  }

  /**
   * Retry sits in an alert that can unmount while the request runs or once it succeeds.
   * With no data, park focus on the status region first so it does not fall back to the
   * page body. When the request ends and focus has nowhere visible to be (the empty region
   * or the body), hand it to the re-mounted Retry button after a failure, or to the panel
   * title after a success.
   */
  async function handleRetry(): Promise<void> {
    const parked = !prerequisites && !isLoading && !isRefreshing;
    const hadFocus = parked || document.activeElement === retryButton;
    if (parked) statusRegion?.focus();
    await handleRefresh();
    const active = document.activeElement;
    if (hadFocus && (active === statusRegion || active === document.body || active === null)) {
      (retryButton ?? titleElement)?.focus();
    }
  }

  /**
   * The announcement follows the snapshot. While a run is in progress, whoever started it,
   * the result text of an earlier run is dropped; a run seen running is read out once it
   * completes. A result that was never seen running (a mount, a reuse) is not read out here,
   * and it does not leave the text of an earlier run standing for it.
   */
  let watchedRunId: number | undefined;
  let announcedRunId: number | undefined;
  $effect(() => {
    const status = prerequisites?.status;
    const runId = prerequisites?.run_id;
    if (status === 'running') {
      watchedRunId = runId;
      announcement = '';
    } else if (status === 'completed') {
      if (runId === watchedRunId) {
        watchedRunId = undefined;
        announcedRunId = runId;
        announcement = resultAnnouncement();
      } else if (runId !== announcedRunId) {
        announcement = '';
      }
    }
  });

  async function handleRefresh() {
    if (isLoading || isRefreshing) return;
    isRefreshing = true;
    announcement = '';
    try {
      await onRefresh();
    } finally {
      isRefreshing = false;
    }
    await tick();
    announcement = error ? '' : resultAnnouncement();
  }
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <!-- Header row -->
  <div class="flex items-center justify-between">
    <!-- Left: label + summary badges -->
    <div class="flex items-center gap-2 flex-wrap">
      <span
        bind:this={titleElement}
        tabindex="-1"
        class="rounded text-xs font-semibold uppercase tracking-wider text-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-primary)]"
      >
        {t('system.database.migration.prerequisites.title')}
      </span>

      {#if prerequisites}
        <!-- The count means something only once the run is done; mid-run it counts finished checks -->
        {#if prerequisites.status === 'completed'}
          <span
            class="inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium badge-status-success"
          >
            {t('system.database.migration.prerequisites.passedCount', {
              passed: passedCount,
              total: totalCount,
            })}
          </span>
        {/if}

        {#if prerequisites.critical_failures > 0}
          <span
            class="inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium badge-status-error"
          >
            {t('system.database.migration.prerequisites.criticalCount', {
              count: prerequisites.critical_failures,
            })}
          </span>
        {/if}

        {#if prerequisites.warnings > 0}
          <span
            class="inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium badge-status-warning"
          >
            {t('system.database.migration.prerequisites.warningCount', {
              count: prerequisites.warnings,
            })}
          </span>
        {/if}
      {/if}
    </div>

    <!-- Right: re-run button + toggle -->
    <div class="flex items-center gap-2">
      <!-- aria-disabled, not disabled: a natively disabled button drops keyboard focus -->
      <button
        type="button"
        class="relative flex items-center gap-1 text-[10px] px-2 py-1 rounded cursor-pointer transition-colors hover:bg-black/5 dark:hover:bg-white/5 text-muted aria-disabled:opacity-50 aria-disabled:cursor-not-allowed"
        onclick={() => {
          if (!rerunBlocked) void handleRefresh();
        }}
        aria-disabled={rerunBlocked ? 'true' : undefined}
        aria-describedby={rerunReasonId}
      >
        <RefreshCw class="w-3 h-3 {isRefreshing ? 'animate-spin' : ''}" />
        {t('system.database.migration.prerequisites.rerun')}
      </button>

      {#if quietChecks.length > 0}
        <button
          type="button"
          class="flex items-center gap-1 text-xs cursor-pointer transition-colors hover:text-primary text-muted"
          onclick={() => (showDetails = !showDetails)}
          aria-expanded={showDetails}
          aria-controls="prerequisites-details"
        >
          {showDetails
            ? t('system.database.migration.prerequisites.hideDetails')
            : t('system.database.migration.prerequisites.showDetails')}
          {#if showDetails}
            <ChevronUp class="w-3 h-3" />
          {:else}
            <ChevronDown class="w-3 h-3" />
          {/if}
        </button>
      {/if}
    </div>
  </div>

  <!-- Always-rendered live region: only its text changes, so the loading state is announced -->
  <div
    bind:this={statusRegion}
    role="status"
    aria-atomic="true"
    tabindex="-1"
    class="rounded-lg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-primary)]"
  >
    {#if isLoading && !isRunning}
      <div class="mt-3 flex items-center gap-2 px-3 py-2 text-sm text-muted">
        <RefreshCw class="w-3.5 h-3.5 animate-spin" aria-hidden="true" />
        {t('system.database.migration.prerequisites.running')}
      </div>
    {/if}
    {#if isRunning}
      <span class="sr-only">{t('system.database.migration.prerequisites.running')}</span>
    {/if}
    <span class="sr-only">{announcement}</span>
  </div>

  {#if rerunBlocked && !isRunning}
    <span id={RERUN_REASON_ID} class="sr-only">
      {t('system.database.migration.prerequisites.running')}
    </span>
  {/if}

  <!-- Progress detail sits outside the live region: its text changes on every poll -->
  {#if isRunning}
    <div id={RUNNING_ROW_ID} class="mt-3 flex items-start gap-2 px-3 py-2 text-sm text-muted">
      <RefreshCw class="w-3.5 h-3.5 mt-0.5 flex-shrink-0 animate-spin" aria-hidden="true" />
      <div>
        <div>
          {t('system.database.migration.prerequisites.running')}
          {#if progress}
            {t('system.database.migration.prerequisites.checkingStep', {
              current: Math.min(progress.completed_steps + 1, progress.total_steps),
              total: progress.total_steps,
            })}
          {/if}
        </div>
        {#if progress?.current_check_name}
          <div>
            {t('system.database.migration.prerequisites.currentCheck', {
              name: progress.current_check_name,
              seconds: elapsedSeconds(progress.current_check_elapsed_ms),
            })}
          </div>
        {/if}
        <div class="text-xs">{t('system.database.migration.prerequisites.slowHint')}</div>
      </div>
    </div>
  {/if}

  {#if showError}
    <ErrorAlert
      className="mt-3"
      message={errorText}
      onRetry={handleRetry}
      retryBusy={isLoading}
      bind:retryButton
    />
  {/if}

  <!-- Failed, errored and warning checks are never hidden -->
  {#if nonPassingChecks.length > 0}
    <div class="mt-3 space-y-1.5">
      {#each nonPassingChecks as check (check.id)}
        {@render checkRow(check)}
      {/each}
    </div>
  {/if}

  <!-- Passed and skipped checks, on request -->
  {#if showDetails && quietChecks.length > 0}
    <div id="prerequisites-details" class="mt-3 space-y-1.5">
      {#each quietChecks as check (check.id)}
        {@render checkRow(check)}
      {/each}
    </div>
  {/if}
</div>

{#snippet checkRow(check: PrerequisiteCheck)}
  {@const StatusIcon = statusIconMap[check.status]}
  <div
    class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors hover:bg-black/[0.02] dark:hover:bg-white/[0.02]"
  >
    <StatusIcon class="w-4 h-4 flex-shrink-0 {statusIconColor[check.status]}" />

    <div class="flex-1 min-w-0">
      <span class="font-medium">{check.name}:</span>
      <span class="text-muted">{check.message}</span>
    </div>

    {#if isBlockingCheck(check)}
      <span
        class="inline-flex items-center rounded-full px-1.5 py-0.5 text-[9px] font-semibold badge-status-error"
      >
        {t('system.database.migration.prerequisites.critical')}
      </span>
    {/if}

    <span
      class="inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium {statusBadgeColor[
        check.status
      ]}"
    >
      {statusLabel(check.status)}
    </span>
  </div>
{/snippet}
