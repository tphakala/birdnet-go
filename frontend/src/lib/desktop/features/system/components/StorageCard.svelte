<script lang="ts">
  import { t } from '$lib/i18n';
  import { HardDrive, MemoryStick } from '@lucide/svelte';
  import { formatBytesCompact } from '$lib/utils/formatters';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import Skeleton from '$lib/desktop/components/ui/Skeleton.svelte';
  import SkeletonStatus from '$lib/desktop/components/ui/SkeletonStatus.svelte';

  interface DiskInfo {
    mountpoint: string;
    total: number;
    used: number;
    usage_percent: number;
  }

  interface MemoryInfo {
    total: number;
    used: number;
    free: number;
    available: number;
    buffers: number;
    cached: number;
    usedPercent: number;
  }

  interface Props {
    disks: DiskInfo[];
    memory: MemoryInfo;
    /** Shows a skeleton of the disks and the RAM block until both first answers. */
    loading?: boolean;
    /** Why the disks could not load; replaces the disk rows. */
    diskError?: string | null;
    /** Why the memory reading could not load; replaces the RAM block. */
    memoryError?: string | null;
    onRetryDisks?: () => Promise<unknown> | unknown;
    onRetryMemory?: () => Promise<unknown> | unknown;
  }

  let {
    disks,
    memory,
    loading = false,
    diskError = null,
    memoryError = null,
    onRetryDisks,
    onRetryMemory,
  }: Props = $props();

  /** Disk placeholders in the skeleton; most hosts show one or two mounts. */
  const SKELETON_DISK_COUNT = 2;
  const DISK_FOOTER_COLUMNS = 2;
  const MEMORY_COLUMN_COUNT = 4;

  let card = $state<HTMLDivElement>();
  // A Retry that succeeds removes the alert; focus then lands on the card
  const focusCard = () => [card];
  // The bars of a part give way to its error; the label stays while any part shows bars
  let showLabel = $derived(loading && (diskError === null || memoryError === null));

  function getProgressColor(pct: number): string {
    if (pct < 70) return 'var(--color-success)';
    if (pct < 90) return 'var(--color-warning)';
    return 'var(--color-error)';
  }

  let buffCachePct = $derived(
    memory.total > 0 ? ((memory.buffers + memory.cached) / memory.total) * 100 : 0
  );
</script>

<!-- Placeholder of one usage block: name and size line, the bar, then the footer line -->
{#snippet usageSkeleton(footerColumns: number, footerClass: string)}
  <div class="flex items-center justify-between mb-1.5 text-sm">
    <div class="flex items-center gap-2">
      <Skeleton className="h-3.5 w-3.5" />
      <span><Skeleton inline className="h-3.5 w-20" /></span>
    </div>
    <span><Skeleton inline className="h-3.5 w-28" /></span>
  </div>
  <Skeleton className="h-2 w-full rounded-full" />
  <div class="flex justify-between text-[10px] {footerClass}">
    {#each { length: footerColumns } as _, i (i)}
      <span><Skeleton inline className="h-2 w-14" /></span>
    {/each}
  </div>
{/snippet}

<div
  bind:this={card}
  tabindex="-1"
  class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-primary)]"
>
  <div class="flex items-center justify-between gap-2 mb-3">
    <h3 class="text-xs font-semibold uppercase tracking-wider text-muted">
      {t('system.storage.title')}
    </h3>
    {#if showLabel}
      <SkeletonStatus label={t('common.loading')} className="text-xs" />
    {/if}
  </div>
  <div class="space-y-4">
    {#if diskError}
      <ErrorAlert message={diskError} onRetry={onRetryDisks} focusFallback={focusCard} />
    {:else if loading}
      {#each { length: SKELETON_DISK_COUNT } as _, i (i)}
        <div>{@render usageSkeleton(DISK_FOOTER_COLUMNS, 'mt-1')}</div>
      {/each}
    {:else}
      {#each disks as disk (disk.mountpoint)}
        <div>
          <div class="flex items-center justify-between mb-1.5">
            <div class="flex items-center gap-2">
              <HardDrive class="w-3.5 h-3.5 text-muted" />
              <span class="text-sm font-medium font-mono">{disk.mountpoint}</span>
            </div>
            <div class="text-sm font-mono tabular-nums">
              <span class="font-semibold">{formatBytesCompact(disk.used)}</span>
              <span class="text-muted"> / {formatBytesCompact(disk.total)}</span>
            </div>
          </div>
          <div class="h-2 rounded-full overflow-hidden bg-[var(--surface-300)]">
            <div
              class="h-full rounded-full transition-[width] duration-600 ease-out"
              style:width="{disk.usage_percent}%"
              style:background={getProgressColor(disk.usage_percent)}
            ></div>
          </div>
          <div class="flex justify-between mt-1 text-[10px] font-mono tabular-nums text-muted">
            <span>{Math.round(disk.usage_percent)}% {t('system.storage.used')}</span>
            <span>{formatBytesCompact(disk.total - disk.used)} {t('system.storage.free')}</span>
          </div>
        </div>
      {/each}
    {/if}

    <!-- Memory breakdown -->
    {#if memoryError}
      <ErrorAlert message={memoryError} onRetry={onRetryMemory} focusFallback={focusCard} />
    {:else if loading}
      <div class="pt-2 border-t border-[var(--border-100)]">
        {@render usageSkeleton(MEMORY_COLUMN_COUNT, 'mt-2')}
      </div>
    {:else if memory.total > 0}
      <div class="pt-2 border-t border-[var(--border-100)]">
        <div class="flex items-center justify-between mb-1.5">
          <div class="flex items-center gap-2">
            <MemoryStick class="w-3.5 h-3.5 text-muted" />
            <span class="text-sm font-medium">{t('system.storage.ram')}</span>
          </div>
          <div class="text-sm font-mono tabular-nums">
            <span class="font-semibold">{formatBytesCompact(memory.used)}</span>
            <span class="text-muted"> / {formatBytesCompact(memory.total)}</span>
          </div>
        </div>
        <div class="h-2 rounded-full overflow-hidden relative bg-[var(--surface-300)]">
          <div
            class="h-full absolute left-0 top-0 rounded-full"
            style:width="{Math.min(memory.usedPercent + buffCachePct, 100)}%"
            style:background="rgba(139, 92, 246, 0.3)"
          ></div>
          <div
            class="h-full absolute left-0 top-0 rounded-full transition-[width] duration-600 ease-out"
            style:width="{memory.usedPercent}%"
            style:background="#8b5cf6"
          ></div>
        </div>
        <div class="grid grid-cols-4 mt-2 text-[10px] font-mono tabular-nums text-muted">
          <span>{Math.round(memory.usedPercent)}% {t('system.storage.used')}</span>
          <span>{formatBytesCompact(memory.free)} {t('system.storage.free')}</span>
          <span>{formatBytesCompact(memory.available)} {t('system.metrics.available')}</span>
          <span
            >{formatBytesCompact(memory.buffers + memory.cached)}
            {t('system.memoryUsage.labels.buffCache')}</span
          >
        </div>
      </div>
    {/if}
  </div>
</div>
