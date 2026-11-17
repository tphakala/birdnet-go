<!--
  SystemProcessTable.svelte

  The process list on the System overview, on the shared SortableDataTable: sortable
  columns (CPU descending first), search by name or PID while Show All is on, a capped
  body that scrolls under a sticky header, and loading, empty and failed as distinct
  states. A failed refresh shows an alert with Retry above the rows already loaded.

  Process states are the backend's own words, shown as they arrive; only their pill
  color is chosen here (processStatusVariant).

  @component
-->
<script lang="ts" module>
  import type { StatusVariant } from '$lib/desktop/components/ui/StatusPill.svelte';

  // Keyed on gopsutil's state words; 'sleeping' is the older spelling of 'sleep'
  const STATUS_VARIANTS = new Map<string, StatusVariant>([
    ['running', 'success'],
    ['sleep', 'neutral'],
    ['sleeping', 'neutral'],
    ['idle', 'neutral'],
    ['stop', 'warning'],
    ['blocked', 'warning'],
    ['wait', 'warning'],
    ['lock', 'warning'],
    ['zombie', 'error'],
  ]);

  /** Pill variant for a process state; any state not listed (daemon, unknown, '') is neutral. */
  export function processStatusVariant(status: string): StatusVariant {
    return STATUS_VARIANTS.get(status) ?? 'neutral';
  }
</script>

<script lang="ts">
  import { Terminal } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { formatBytesCompact, formatUptimeCompact } from '$lib/utils/formatters';
  import SortableDataTable from '$lib/desktop/components/data/SortableDataTable.svelte';
  import type { Column } from '$lib/desktop/components/data/DataTable.types';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import StatusPill from '$lib/desktop/components/ui/StatusPill.svelte';
  import Meter from '$lib/desktop/components/ui/Meter.svelte';
  import ToggleField from '$lib/desktop/components/forms/ToggleField.svelte';
  import type { ProcessInfo } from '../types';

  interface Props {
    processes: ProcessInfo[];
    /** True until the first answer for the current Show All mode arrives */
    loading: boolean;
    /** Why the latest load failed; the rows already loaded stay below it */
    error: string | null;
    showAllProcesses: boolean;
    /** Name shown for the app's own process, which the OS calls main */
    processName?: string;
    onShowAllChange: (_value: boolean) => void;
    onRetry: () => Promise<void>;
  }

  let {
    processes,
    loading,
    error,
    showAllProcesses,
    processName = 'BirdNET-Go',
    onShowAllChange,
    onRetry,
  }: Props = $props();

  /** Height cap of the scrolling body: most of the window, never taller than 40rem. */
  const BODY_MAX_HEIGHT = 'min(70vh, 40rem)';
  const MAX_CPU_PERCENT = 100;

  let panel = $state<HTMLDivElement>();
  // A Retry that succeeds removes the alert under the focus; the first sort button, or the
  // Show All switch when the table shows no rows, takes focus instead of the document body.
  // After a click in the rows, though, focus sits on the page body, where the arrow keys and
  // Space scroll the capped list; pulling focus to a sort button when the alert later goes
  // would make Space sort instead, so the fallback then offers nothing. Focus anywhere in the
  // panel, or a click on the alert, restores it. The wrapper itself is not focusable.
  // Plain variable: only the fallback callback reads it, nothing renders from it.
  let leftForRows = false;

  $effect(() => {
    const root = panel;
    if (!root) return;
    // By the time a click fires, the press has already moved focus, so this records what
    // actually happened: a touch pan over the rows fires no click and leaves focus on Retry.
    const onClick = (event: MouseEvent) => {
      const target = event.target;
      if (!(target instanceof globalThis.Element)) return;
      if (target.closest('tbody')) {
        leftForRows = !document.activeElement?.closest('[role="alert"]');
      } else if (target.closest('[role="alert"]')) {
        leftForRows = false;
      }
    };
    const onFocusIn = () => {
      leftForRows = false;
    };
    root.addEventListener('click', onClick);
    root.addEventListener('focusin', onFocusIn);
    return () => {
      root.removeEventListener('click', onClick);
      root.removeEventListener('focusin', onFocusIn);
    };
  });

  const focusPanel = () =>
    leftForRows
      ? []
      : [
          panel?.querySelector<HTMLElement>('thead button'),
          panel?.querySelector<HTMLElement>('input[type="checkbox"]'),
        ];

  const displayName = (proc: ProcessInfo): string =>
    proc.name === 'main' ? processName : proc.name;

  // Clamped to 0-100 as the backend normalizes it; a missing or NaN value reads as 0
  const cpuPercent = (proc: ProcessInfo): number =>
    Number.isFinite(proc.cpu) ? Math.max(0, Math.min(proc.cpu, MAX_CPU_PERCENT)) : 0;

  let columns = $derived<Column<ProcessInfo>[]>([
    {
      key: 'name',
      header: t('system.processInfo.headers.process'),
      sortable: true,
      sortValue: displayName,
      className: 'min-w-40',
    },
    {
      key: 'status',
      header: t('system.processInfo.headers.status'),
      sortable: true,
    },
    {
      key: 'cpu',
      header: t('system.processInfo.headers.cpu'),
      sortable: true,
      sortValue: cpuPercent,
      align: 'right',
      defaultDirection: 'desc',
    },
    {
      key: 'memory',
      header: t('system.processInfo.headers.memory'),
      sortable: true,
      align: 'right',
      defaultDirection: 'desc',
    },
    {
      key: 'uptime',
      header: t('system.processInfo.headers.uptime'),
      sortable: true,
      align: 'right',
      defaultDirection: 'desc',
    },
  ]);
</script>

{#snippet notice()}
  {#if error}
    <ErrorAlert
      message={t('system.errors.processes', { error })}
      {onRetry}
      focusFallback={focusPanel}
    />
  {/if}
{/snippet}

<!-- With a failed load and no rows, the alert alone explains the empty table -->
{#snippet noEmptyState()}{/snippet}

{#snippet showAllToggle()}
  <ToggleField
    variant="inline"
    label={t('system.processInfo.showAll')}
    value={showAllProcesses}
    onUpdate={onShowAllChange}
  />
{/snippet}

{#snippet cell({ column, item }: { column: Column<ProcessInfo>; item: ProcessInfo })}
  {#if column.key === 'name'}
    <div class="flex items-center gap-2">
      <Terminal class="size-3.5 shrink-0 text-muted" aria-hidden="true" />
      <div class="min-w-0">
        <div class="font-medium wrap-anywhere">{displayName(item)}</div>
        <div class="text-[0.6875rem] font-mono tabular-nums text-muted">
          {t('system.processInfo.headers.pid')}
          {item.pid}
        </div>
      </div>
    </div>
  {:else if column.key === 'status'}
    <StatusPill variant={processStatusVariant(item.status)} label={item.status} size="xs" />
  {:else if column.key === 'cpu'}
    {@const cpu = cpuPercent(item)}
    <!-- The value is printed beside the bar, so the bar is hidden from assistive technology -->
    <div class="flex items-center justify-end gap-2">
      <Meter
        value={cpu}
        size="xs"
        tone="series-1"
        label={t('system.processInfo.headers.cpu')}
        className="w-12 shrink-0"
        aria-hidden="true"
      />
      <span class="w-12 text-right font-mono text-xs whitespace-nowrap">{cpu.toFixed(1)}%</span>
    </div>
  {:else if column.key === 'memory'}
    <span class="font-mono text-xs font-medium whitespace-nowrap"
      >{formatBytesCompact(item.memory)}</span
    >
  {:else if column.key === 'uptime'}
    <span class="font-mono text-xs whitespace-nowrap">{formatUptimeCompact(item.uptime)}</span>
  {/if}
{/snippet}

<div bind:this={panel}>
  <SortableDataTable
    {columns}
    data={processes}
    {loading}
    loadingLabel={t('system.processInfo.loading')}
    title={t('system.processInfo.title')}
    count={processes.length}
    searchable={showAllProcesses}
    searchAccessor={proc => `${displayName(proc)} ${proc.pid}`}
    defaultSortKey="cpu"
    keyFn={proc => proc.pid}
    resizable={false}
    bodyMaxHeight={BODY_MAX_HEIGHT}
    emptyTitle={t('system.processInfo.noProcesses')}
    noResultsMessage={t('search.noResultsFound')}
    {notice}
    empty={error ? noEmptyState : undefined}
    headerActions={showAllToggle}
    renderCell={cell}
  />
</div>
