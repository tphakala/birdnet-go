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
    passed: 'text-emerald-600 dark:text-emerald-400',
    failed: 'text-red-600 dark:text-red-400',
    error: 'text-red-600 dark:text-red-400',
    warning: 'text-amber-600 dark:text-amber-400',
    skipped: 'text-slate-500 dark:text-slate-400',
  };

  /** Status badge color classes */
  const statusBadgeColor: Record<PrerequisiteCheckStatus, string> = {
    passed: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
    failed: 'bg-red-500/10 text-red-600 dark:text-red-400',
    error: 'bg-red-500/10 text-red-600 dark:text-red-400',
    warning: 'bg-amber-500/10 text-amber-600 dark:text-amber-400',
    skipped: 'bg-slate-500/10 text-slate-500',
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

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <!-- Header row -->
  <div class="flex items-center justify-between">
    <!-- Left: label + summary badges -->
    <div class="flex items-center gap-2 flex-wrap">
      <span
        class="text-xs font-semibold uppercase tracking-wider text-[var(--color-text-tertiary)]"
      >
        Prerequisites
      </span>

      {#if prerequisites}
        <span
          class="inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
        >
          {passedCount}/{totalCount} passed
        </span>

        {#if prerequisites.critical_failures > 0}
          <span
            class="inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium bg-red-500/10 text-red-600 dark:text-red-400"
          >
            {prerequisites.critical_failures} critical
          </span>
        {/if}

        {#if prerequisites.warnings > 0}
          <span
            class="inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium bg-amber-500/10 text-amber-600 dark:text-amber-400"
          >
            {prerequisites.warnings} warning{prerequisites.warnings > 1 ? 's' : ''}
          </span>
        {/if}
      {/if}
    </div>

    <!-- Right: re-run button + toggle -->
    <div class="flex items-center gap-2">
      <button
        type="button"
        class="relative flex items-center gap-1 text-[10px] px-2 py-1 rounded cursor-pointer transition-colors hover:bg-black/5 dark:hover:bg-white/5 text-[var(--color-text-tertiary)] disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={handleRefresh}
        disabled={isLoading || isRefreshing}
      >
        <RefreshCw class="w-3 h-3 {isRefreshing ? 'animate-spin' : ''}" />
        Re-run
      </button>

      <button
        type="button"
        class="flex items-center gap-1 text-xs cursor-pointer transition-colors hover:text-blue-500 text-[var(--color-text-tertiary)]"
        onclick={() => (showDetails = !showDetails)}
      >
        {showDetails ? 'Hide' : 'Show'} details
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
    <div class="mt-3 space-y-1.5">
      {#each checks as check (check.id)}
        {@const StatusIcon = statusIconMap[check.status]}
        <div
          class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors hover:bg-black/[0.02] dark:hover:bg-white/[0.02]"
        >
          <StatusIcon class="w-4 h-4 flex-shrink-0 {statusIconColor[check.status]}" />

          <div class="flex-1 min-w-0">
            <span class="font-medium">{check.name}</span>
            <span class="mx-1.5 text-[var(--color-text-tertiary)]">&mdash;</span>
            <span class="text-[var(--color-text-secondary)]">{check.message}</span>
          </div>

          {#if check.severity === 'critical' && check.status !== 'passed'}
            <span
              class="inline-flex items-center rounded-full px-1.5 py-0.5 text-[9px] font-semibold bg-red-500/10 text-red-600 dark:text-red-400"
            >
              Critical
            </span>
          {/if}

          <span
            class="inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium {statusBadgeColor[
              check.status
            ]}"
          >
            {check.status}
          </span>
        </div>
      {/each}
    </div>
  {/if}
</div>
