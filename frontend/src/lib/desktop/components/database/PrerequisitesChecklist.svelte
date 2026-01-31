<!--
  PrerequisitesChecklist Component

  Displays migration prerequisites checks and their status.
  Shows visual indicators for passed/failed/warning/skipped checks.
  Provides a "Re-run Checks" button for manual refresh.

  Props:
  - prerequisites: Prerequisites response from API
  - isLoading: Whether checks are being fetched
  - error: Error message if fetch failed
  - onRefresh: Callback to re-run checks

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import {
    CheckCircle2,
    XCircle,
    AlertTriangle,
    SkipForward,
    RefreshCw,
    AlertCircle,
    Loader2,
    ShieldCheck,
  } from '@lucide/svelte';
  import type { PrerequisitesResponse } from '$lib/types/migration';

  interface Props {
    prerequisites: PrerequisitesResponse | null;
    isLoading: boolean;
    error: string | null;
    onRefresh: () => Promise<void>;
  }

  let { prerequisites, isLoading, error, onRefresh }: Props = $props();

  let isRefreshing = $state(false);
  let currentStep = $state(1);

  // Check names for the loading animation (matches backend check order)
  const checkSteps = [
    'state_idle',
    'disk_space',
    'legacy_accessible',
    'sqlite_integrity',
    'mysql_table_health',
    'mysql_permissions',
    'write_permission',
    'record_count',
    'existing_v2_data',
    'memory_available',
    'mysql_max_packet',
    'mysql_timeout',
  ];

  const totalSteps = checkSteps.length;

  // Animate through steps while loading
  $effect(() => {
    if (!isLoading && !isRefreshing) {
      currentStep = 1;
      return;
    }

    const interval = setInterval(() => {
      currentStep = currentStep >= totalSteps ? 1 : currentStep + 1;
    }, 400);

    return () => clearInterval(interval);
  });

  // Status icon mapping
  const statusIcons = {
    passed: CheckCircle2,
    failed: XCircle,
    warning: AlertTriangle,
    skipped: SkipForward,
    error: XCircle,
  };

  // Status color classes
  const statusColors: Record<string, string> = {
    passed: 'text-[var(--color-success)]',
    failed: 'text-[var(--color-error)]',
    warning: 'text-[var(--color-warning)]',
    skipped: 'text-[var(--color-base-content)]/50',
    error: 'text-[var(--color-error)]',
  };

  // Background colors for status badges
  const statusBgColors: Record<string, string> = {
    passed: 'bg-[var(--color-success)]/10',
    failed: 'bg-[var(--color-error)]/10',
    warning: 'bg-[var(--color-warning)]/10',
    skipped: 'bg-[var(--color-base-200)]',
    error: 'bg-[var(--color-error)]/10',
  };

  async function handleRefresh() {
    isRefreshing = true;
    try {
      await onRefresh();
    } finally {
      isRefreshing = false;
    }
  }
</script>

<div class="space-y-4 relative">
  <!-- Header -->
  <div class="flex items-center justify-between">
    <h4 class="text-sm font-medium text-[var(--color-base-content)] flex items-center gap-2">
      <ShieldCheck class="size-4" />
      {t('system.database.migration.prerequisites.title')}
    </h4>
    <button
      type="button"
      class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium
             rounded-md transition-colors
             border border-[var(--color-base-300)]
             text-[var(--color-base-content)]
             hover:bg-[var(--color-base-200)]
             disabled:opacity-50 disabled:cursor-not-allowed"
      onclick={handleRefresh}
      disabled={isLoading || isRefreshing}
    >
      <RefreshCw class="size-3.5 {isRefreshing ? 'animate-spin' : ''}" />
      {t('system.database.migration.prerequisites.rerunChecks')}
    </button>
  </div>

  <!-- Content -->
  {#if isLoading && !prerequisites}
    <!-- Initial loading state with spinner and step progress -->
    <div class="flex flex-col items-center justify-center py-8 gap-4">
      <Loader2 class="size-8 animate-spin text-[var(--color-primary)]" />
      <div class="text-center space-y-1">
        <p class="text-sm font-medium text-[var(--color-base-content)]">
          {t('system.database.migration.prerequisites.running')}
        </p>
        <p class="text-xs text-[var(--color-base-content)]/60">
          {t('system.database.migration.prerequisites.checkingStep', {
            current: currentStep,
            total: totalSteps,
          })}
        </p>
      </div>
    </div>
  {:else if error}
    <!-- Error state -->
    <div
      class="p-4 rounded-lg bg-[var(--color-error)]/10 text-[var(--color-error)] flex items-center gap-3"
      role="alert"
      aria-live="assertive"
    >
      <AlertCircle class="size-5 shrink-0" />
      <span class="text-sm">{error}</span>
    </div>
  {:else if prerequisites}
    <!-- Checks list -->
    <div class="space-y-2">
      {#each prerequisites.checks as check (check.id)}
        {@const StatusIcon = statusIcons[check.status]}
        <div
          class="flex items-start gap-3 p-3 rounded-lg {statusBgColors[check.status]}"
          role="listitem"
        >
          <div class="shrink-0 mt-0.5">
            <StatusIcon class="size-5 {statusColors[check.status]}" />
          </div>
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-2">
              <span class="text-sm font-medium text-[var(--color-base-content)]">
                {t(`system.database.migration.prerequisites.checks.${check.id}.name`)}
              </span>
              {#if check.severity === 'critical' && check.status === 'failed'}
                <span
                  class="text-[10px] uppercase font-semibold px-1.5 py-0.5 rounded
                         bg-[var(--color-error)] text-white"
                >
                  {t('system.database.migration.prerequisites.critical')}
                </span>
              {/if}
            </div>
            <p class="text-xs text-[var(--color-base-content)]/70 mt-0.5">
              {check.message}
            </p>
          </div>
        </div>
      {/each}
    </div>

    <!-- Summary -->
    <div class="pt-2 border-t border-[var(--color-base-200)]">
      {#if prerequisites.can_start_migration}
        <div
          class="flex items-center gap-2 text-sm text-[var(--color-success)]"
          role="status"
          aria-live="polite"
        >
          <CheckCircle2 class="size-4" />
          <span>{t('system.database.migration.prerequisites.allPassed')}</span>
        </div>
      {:else}
        <div
          class="flex items-center gap-2 text-sm text-[var(--color-error)]"
          role="alert"
          aria-live="assertive"
        >
          <AlertCircle class="size-4" />
          <span>
            {t('system.database.migration.prerequisites.criticalIssues', {
              count: prerequisites.critical_failures,
            })}
          </span>
        </div>
      {/if}
      {#if prerequisites.warnings > 0}
        <div
          class="flex items-center gap-2 text-sm text-[var(--color-warning)] mt-1"
          role="status"
          aria-live="polite"
        >
          <AlertTriangle class="size-4" />
          <span>
            {t('system.database.migration.prerequisites.warningsCount', {
              count: prerequisites.warnings,
            })}
          </span>
        </div>
      {/if}
    </div>

    <!-- Refreshing overlay -->
    {#if isRefreshing}
      <div
        class="absolute inset-0 bg-[var(--color-base-100)]/50 flex items-center justify-center rounded-lg"
      >
        <Loader2 class="size-6 animate-spin text-[var(--color-primary)]" />
      </div>
    {/if}
  {/if}
</div>
