<script lang="ts">
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import StatusPill from '$lib/desktop/components/ui/StatusPill.svelte';
  import type { StatusVariant } from '$lib/desktop/components/ui/StatusPill.svelte';
  import {
    Activity,
    Play,
    Download,
    ClipboardCopy,
    CheckCircle,
    AlertTriangle,
    XCircle,
    SkipForward,
    Clock,
    Loader2,
    Info,
  } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import { copyToClipboard, COPY_FEEDBACK_TIMEOUT_MS } from '$lib/utils/clipboard';
  import { getLocalDateString, getLocalTimeString } from '$lib/utils/date';
  import { downloadBlob } from '$lib/utils/fileHelpers';

  type HealthStatus = 'healthy' | 'warning' | 'critical' | 'unknown' | 'skipped';
  type HealthCategory =
    | 'system'
    | 'audio'
    | 'analysis'
    | 'streams'
    | 'database'
    | 'network'
    | 'config'
    | 'logs';

  interface DiagnosticsResult {
    name: string;
    category: HealthCategory;
    status: HealthStatus;
    message: string;
    details?: Record<string, unknown>;
    duration_ms: number;
    timestamp: string;
  }

  interface DiagnosticsReport {
    id: string;
    status: HealthStatus;
    started_at: string;
    completed_at: string;
    duration_ms: number;
    total_checks: number;
    results: DiagnosticsResult[];
    summary: Record<string, HealthStatus>;
    count_by_status: Record<string, number>;
  }

  const categoryOrder: HealthCategory[] = [
    'system',
    'audio',
    'analysis',
    'streams',
    'database',
    'network',
    'config',
    'logs',
  ];

  let report = $state<DiagnosticsReport | null>(null);
  let running = $state(false);
  let error = $state<string | null>(null);
  let copied = $state(false);
  let copyTimer: ReturnType<typeof setTimeout> | null = null;
  let expandedChecks = $state(new Set<string>());

  function toggleExpand(checkName: string) {
    const next = new Set(expandedChecks);
    if (next.has(checkName)) {
      next.delete(checkName);
    } else {
      next.add(checkName);
    }
    expandedChecks = next;
  }

  interface ErrorGroup {
    component?: string;
    message: string;
    count: number;
    level: string;
    sample_fields?: Record<string, unknown>;
  }

  function getTopErrors(result: DiagnosticsResult): ErrorGroup[] | null {
    const topErrors = result.details?.top_errors;
    if (!Array.isArray(topErrors) || topErrors.length === 0) return null;
    return topErrors as ErrorGroup[];
  }

  function levelColor(level: string): string {
    switch (level) {
      case 'fatal':
      case 'panic':
        return 'var(--color-error)';
      case 'error':
        return 'var(--color-warning)';
      default:
        return 'var(--color-base-content)';
    }
  }

  onMount(() => {
    return () => {
      if (copyTimer !== null) clearTimeout(copyTimer);
    };
  });

  let groupedResults = $derived.by(() => {
    if (!report) return new Map<HealthCategory, DiagnosticsResult[]>();
    const groups = new Map<HealthCategory, DiagnosticsResult[]>();
    const seen = new Set<HealthCategory>();
    for (const cat of categoryOrder) {
      const results = report.results.filter(r => r.category === cat);
      if (results.length > 0) {
        groups.set(cat, results);
      }
      seen.add(cat);
    }
    for (const r of report.results) {
      if (!seen.has(r.category)) {
        const existing = groups.get(r.category) ?? [];
        existing.push(r);
        groups.set(r.category, existing);
      }
    }
    return groups;
  });

  function statusToVariant(status: HealthStatus): StatusVariant {
    switch (status) {
      case 'healthy':
        return 'success';
      case 'warning':
        return 'warning';
      case 'critical':
        return 'error';
      default:
        return 'neutral';
    }
  }

  async function runDiagnostics() {
    if (running) return;
    running = true;
    error = null;
    try {
      report = await api.post<DiagnosticsReport>('/api/v2/system/diagnostics/run');
    } catch {
      error = t('health.errors.fetchFailed');
    } finally {
      running = false;
    }
  }

  function formatCheckName(name: string): string {
    return name
      .split('_')
      .map(w => w.charAt(0).toUpperCase() + w.slice(1))
      .join(' ');
  }

  function buildTextReport(): string {
    if (!report) return '';
    const lines: string[] = [];
    lines.push(t('health.export.reportTitle'));
    lines.push(`${t('health.export.statusLabel')}: ${t(`health.status.${report.status}`)}`);
    lines.push(
      `${t('health.export.timeLabel')}: ${getLocalDateString(new Date(report.started_at))} ${getLocalTimeString(new Date(report.started_at))}`
    );
    lines.push(`${t('health.export.durationLabel')}: ${report.duration_ms.toFixed(0)}ms`);
    lines.push(`${t('health.export.checksLabel')}: ${report.total_checks}`);
    lines.push('');

    for (const [cat, results] of groupedResults) {
      const catLabel = t(`health.categories.${cat}`);
      lines.push(`--- ${catLabel} ---`);
      for (const r of results) {
        const statusLabel = t(`health.status.${r.status}`);
        lines.push(`  [${statusLabel}] ${formatCheckName(r.name)}: ${r.message}`);
      }
      lines.push('');
    }
    return lines.join('\n');
  }

  async function copyReport() {
    const ok = await copyToClipboard(buildTextReport());
    if (!ok) return;
    copied = true;
    if (copyTimer !== null) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      copied = false;
      copyTimer = null;
    }, COPY_FEEDBACK_TIMEOUT_MS);
  }

  function downloadJSON() {
    if (!report) return;
    const blob = new Blob([JSON.stringify(report, null, 2)], { type: 'application/json' });
    downloadBlob(blob, `health-report-${report.id?.slice(0, 8) ?? 'unknown'}.json`);
  }
</script>

{#snippet statusIcon(status: HealthStatus, sizeClass: string)}
  {#if status === 'healthy'}
    <CheckCircle class={sizeClass} />
  {:else if status === 'warning'}
    <AlertTriangle class={sizeClass} />
  {:else if status === 'critical'}
    <XCircle class={sizeClass} />
  {:else if status === 'skipped'}
    <SkipForward class={sizeClass} />
  {:else}
    <Info class={sizeClass} />
  {/if}
{/snippet}

<div class="col-span-12 space-y-4">
  <!-- Page Header -->
  <Card className="bg-[var(--color-base-100)] shadow-sm">
    <div class="flex flex-col items-center text-center">
      <div
        class="w-20 h-20 rounded-full bg-gradient-to-b from-[var(--surface-200)] to-[var(--color-base-100)] flex items-center justify-center border border-[var(--border-100)]"
      >
        <Activity class="size-10 text-[var(--color-primary)]" />
      </div>
      <div class="mt-3">
        <h1 class="text-3xl font-bold">{t('health.title')}</h1>
        <p class="text-[var(--color-base-content)] opacity-70 text-base mt-2">
          {t('health.subtitle')}
        </p>
      </div>

      <div class="mt-4">
        <button
          onclick={runDiagnostics}
          disabled={running}
          class="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-md transition-all bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:bg-[var(--color-primary-hover)] focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {#if running}
            <Loader2 class="size-4 animate-spin" />
            {t('health.running')}
          {:else}
            <Play class="size-4" />
            {t('health.runDiagnostics')}
          {/if}
        </button>
      </div>
    </div>
  </Card>

  <!-- Error State -->
  {#if error}
    <Card className="bg-[var(--color-base-100)] shadow-sm">
      <div
        role="alert"
        aria-live="assertive"
        class="flex items-center gap-3 p-3 rounded-lg bg-[color-mix(in_srgb,var(--color-error)_10%,transparent)]"
      >
        <XCircle class="size-5 shrink-0 text-[var(--color-error)]" />
        <p class="text-sm text-[var(--color-base-content)]">{error}</p>
      </div>
    </Card>
  {/if}

  <!-- No Results State -->
  {#if !report && !running && !error}
    <Card className="bg-[var(--color-base-100)] shadow-sm">
      <div class="flex flex-col items-center py-6 text-center">
        <Activity class="size-12 text-[var(--color-base-content)] opacity-20 mb-3" />
        <p class="text-[var(--color-base-content)] opacity-60 text-sm">
          {t('health.noResults')}
        </p>
      </div>
    </Card>
  {/if}

  <!-- Results -->
  {#if report}
    <!-- Summary Bar -->
    <Card className="bg-[var(--color-base-100)] shadow-sm">
      <div class="flex flex-wrap items-center gap-4">
        <div class="flex items-center gap-2">
          <StatusPill
            variant={statusToVariant(report.status)}
            label={t(`health.status.${report.status}`)}
            size="md"
          >
            {#snippet leadingIcon()}
              {#if report}
                {@render statusIcon(report.status, 'size-4')}
              {/if}
            {/snippet}
          </StatusPill>
        </div>

        <div class="flex items-center gap-3 text-sm text-[var(--color-base-content)] opacity-70">
          {#if report.count_by_status.healthy}
            <span class="flex items-center gap-1">
              <CheckCircle class="size-3.5 text-[var(--color-success)]" />
              {report.count_by_status.healthy}
              {t('health.summary.healthy')}
            </span>
          {/if}
          {#if report.count_by_status.warning}
            <span class="flex items-center gap-1">
              <AlertTriangle class="size-3.5 text-[var(--color-warning)]" />
              {report.count_by_status.warning}
              {t('health.summary.warnings')}
            </span>
          {/if}
          {#if report.count_by_status.critical}
            <span class="flex items-center gap-1">
              <XCircle class="size-3.5 text-[var(--color-error)]" />
              {report.count_by_status.critical}
              {t('health.summary.critical')}
            </span>
          {/if}
          {#if report.count_by_status.skipped}
            <span class="flex items-center gap-1">
              <SkipForward class="size-3.5 opacity-40" />
              {report.count_by_status.skipped}
              {t('health.summary.skipped')}
            </span>
          {/if}
        </div>

        <div
          class="ml-auto flex items-center gap-2 text-xs text-[var(--color-base-content)] opacity-50"
        >
          <Clock class="size-3.5" />
          {report.duration_ms.toFixed(0)}ms
        </div>
      </div>

      <!-- Export Buttons -->
      <div class="flex items-center gap-2 mt-3 pt-3 border-t border-[var(--color-base-200)]">
        <button
          onclick={copyReport}
          class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-md transition-all bg-[var(--color-base-200)] text-[var(--color-base-content)] hover:bg-[var(--color-base-300)]"
        >
          {#if copied}
            <CheckCircle class="size-3.5 text-[var(--color-success)]" />
            {t('health.copied')}
          {:else}
            <ClipboardCopy class="size-3.5" />
            {t('health.exportText')}
          {/if}
        </button>
        <button
          onclick={downloadJSON}
          class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-md transition-all bg-[var(--color-base-200)] text-[var(--color-base-content)] hover:bg-[var(--color-base-300)]"
        >
          <Download class="size-3.5" />
          {t('health.exportJSON')}
        </button>
      </div>
    </Card>

    <!-- Category Results -->
    {#each [...groupedResults] as [category, results] (category)}
      <Card
        title={t(`health.categories.${category}`)}
        className="bg-[var(--color-base-100)] shadow-sm"
      >
        <div class="space-y-2">
          {#each results as result (result.name)}
            {@const topErrors = getTopErrors(result)}
            {@const isExpandable =
              topErrors !== null && (result.status === 'warning' || result.status === 'critical')}
            {@const isExpanded = expandedChecks.has(result.name)}
            <div>
              <button
                type="button"
                class="flex items-center gap-3 w-full px-3 py-2.5 rounded-lg bg-[var(--color-base-200)]/50 text-left {isExpandable
                  ? 'cursor-pointer hover:bg-[var(--color-base-200)]'
                  : 'cursor-default'}"
                onclick={() => isExpandable && toggleExpand(result.name)}
                disabled={!isExpandable}
              >
                <StatusPill
                  variant={statusToVariant(result.status)}
                  label={t(`health.status.${result.status}`)}
                  size="sm"
                >
                  {#snippet leadingIcon()}
                    {@render statusIcon(result.status, 'size-3.5')}
                  {/snippet}
                </StatusPill>
                <div class="flex-1 min-w-0">
                  <span class="text-sm font-medium">{formatCheckName(result.name)}</span>
                  <p class="text-xs text-[var(--color-base-content)] opacity-60 truncate">
                    {result.message}
                  </p>
                </div>
                <span class="text-xs text-[var(--color-base-content)] opacity-40 shrink-0">
                  {result.duration_ms.toFixed(1)}ms
                </span>
                {#if isExpandable}
                  <svg
                    class="size-4 shrink-0 opacity-40 transition-transform"
                    class:rotate-180={isExpanded}
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                    stroke-width="2"
                  >
                    <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
                  </svg>
                {/if}
              </button>

              {#if isExpanded && topErrors}
                <div
                  class="mt-1 ml-3 mr-3 mb-2 rounded-lg bg-[var(--color-base-200)]/30 overflow-hidden"
                >
                  <table class="w-full text-xs">
                    <thead>
                      <tr
                        class="text-left text-[var(--color-base-content)] opacity-50 border-b border-[var(--color-base-200)]"
                      >
                        <th class="px-3 py-1.5 font-medium">{t('health.logs.errorComponent')}</th>
                        <th class="px-3 py-1.5 font-medium">{t('health.logs.errorMessage')}</th>
                        <th class="px-3 py-1.5 font-medium text-right"
                          >{t('health.logs.errorCount')}</th
                        >
                        <th class="px-3 py-1.5 font-medium">{t('health.logs.errorLevel')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {#each topErrors as group (group.component + ':' + group.message)}
                        <tr class="border-b border-[var(--color-base-200)]/50 last:border-0">
                          <td class="px-3 py-1.5">
                            {#if group.component}
                              <span
                                class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-[var(--color-base-300)] text-[var(--color-base-content)]"
                              >
                                {group.component}
                              </span>
                            {:else}
                              <span class="opacity-30">-</span>
                            {/if}
                          </td>
                          <td class="px-3 py-1.5 max-w-[300px] truncate" title={group.message}>
                            {group.message}
                          </td>
                          <td class="px-3 py-1.5 text-right font-mono">{group.count}</td>
                          <td class="px-3 py-1.5">
                            <span
                              class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium"
                              style:color={levelColor(group.level)}
                              style:background="color-mix(in srgb, {levelColor(group.level)} 10%, transparent)"
                            >
                              {group.level}
                            </span>
                          </td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                </div>
              {/if}
            </div>
          {/each}
        </div>
      </Card>
    {/each}
  {/if}
</div>
