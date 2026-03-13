<!--
  DatabasePrerequisites Component

  Collapsible prerequisites panel for database migration.
  Displays summary badges in the header and an expandable
  checklist of individual prerequisite check results.

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
  import { t } from '$lib/i18n';
  import type { PrerequisitesResponse, PrerequisiteCheckStatus } from '$lib/types/migration';

  interface Props {
    prerequisites: PrerequisitesResponse | null;
    isLoading?: boolean;
    onRefresh: () => Promise<void>;
  }

  let { prerequisites, isLoading = false, onRefresh }: Props = $props();

  let showDetails = $state(false);
  let isRefreshing = $state(false);

  /** Checks array with null-safety (Go may serialize nil slice as null) */
  let checks = $derived(prerequisites?.checks ?? []);

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

  async function handleRefresh() {
    isRefreshing = true;
    try {
      await onRefresh();
    } finally {
      isRefreshing = false;
    }
  }
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <!-- Header row -->
  <div class="flex items-center justify-between">
    <!-- Left: label + summary badges -->
    <div class="flex items-center gap-2 flex-wrap">
      <span class="text-xs font-semibold uppercase tracking-wider text-muted">
        {t('system.database.migration.prerequisites.title')}
      </span>

      {#if prerequisites}
        <span
          class="inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium badge-status-success"
        >
          {t('system.database.migration.prerequisites.passedCount', {
            passed: passedCount,
            total: totalCount,
          })}
        </span>

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
      <button
        type="button"
        class="relative flex items-center gap-1 text-[10px] px-2 py-1 rounded cursor-pointer transition-colors hover:bg-black/5 dark:hover:bg-white/5 text-muted disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={handleRefresh}
        disabled={isLoading || isRefreshing}
      >
        <RefreshCw class="w-3 h-3 {isRefreshing ? 'animate-spin' : ''}" />
        {t('system.database.migration.prerequisites.rerun')}
      </button>

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
    </div>
  </div>

  <!-- Expandable check list -->
  {#if showDetails && prerequisites}
    <div id="prerequisites-details" class="mt-3 space-y-1.5">
      {#each checks as check (check.id)}
        {@const StatusIcon = statusIconMap[check.status]}
        <div
          class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors hover:bg-black/[0.02] dark:hover:bg-white/[0.02]"
        >
          <StatusIcon class="w-4 h-4 flex-shrink-0 {statusIconColor[check.status]}" />

          <div class="flex-1 min-w-0">
            <span class="font-medium">{check.name}</span>
            <span class="mx-1.5 text-muted">&mdash;</span>
            <span class="text-muted">{check.message}</span>
          </div>

          {#if check.severity === 'critical' && check.status !== 'passed'}
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
      {/each}
    </div>
  {/if}
</div>
