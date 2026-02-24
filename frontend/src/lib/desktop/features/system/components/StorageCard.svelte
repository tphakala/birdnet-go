<script lang="ts">
  import { t } from '$lib/i18n';
  import { HardDrive, MemoryStick } from '@lucide/svelte';
  import { formatBytesCompact } from '$lib/utils/formatters';

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
  }

  let { disks, memory }: Props = $props();

  function getProgressColor(pct: number): string {
    if (pct < 70) return 'var(--color-success)';
    if (pct < 90) return 'var(--color-warning)';
    return 'var(--color-error)';
  }

  let buffCachePct = $derived(
    memory.total > 0 ? ((memory.buffers + memory.cached) / memory.total) * 100 : 0
  );
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <h3
    class="text-xs font-semibold uppercase tracking-wider mb-3 text-slate-400 dark:text-slate-500"
  >
    {t('system.storage.title')}
  </h3>
  <div class="space-y-4">
    {#each disks as disk (disk.mountpoint)}
      <div>
        <div class="flex items-center justify-between mb-1.5">
          <div class="flex items-center gap-2">
            <HardDrive class="w-3.5 h-3.5 text-slate-400 dark:text-slate-500" />
            <span class="text-sm font-medium font-mono">{disk.mountpoint}</span>
          </div>
          <div class="text-sm font-mono tabular-nums">
            <span class="font-semibold">{formatBytesCompact(disk.used)}</span>
            <span class="text-slate-400 dark:text-slate-500">
              / {formatBytesCompact(disk.total)}</span
            >
          </div>
        </div>
        <div class="h-2 rounded-full overflow-hidden bg-[var(--surface-300)]">
          <div
            class="h-full rounded-full transition-[width] duration-600 ease-out"
            style:width="{disk.usage_percent}%"
            style:background={getProgressColor(disk.usage_percent)}
          ></div>
        </div>
        <div
          class="flex justify-between mt-1 text-[10px] font-mono tabular-nums text-slate-400 dark:text-slate-500"
        >
          <span>{Math.round(disk.usage_percent)}% {t('system.storage.used')}</span>
          <span>{formatBytesCompact(disk.total - disk.used)} {t('system.storage.free')}</span>
        </div>
      </div>
    {/each}

    <!-- Memory breakdown -->
    {#if memory.total > 0}
      <div class="pt-2 border-t border-[var(--border-100)]">
        <div class="flex items-center justify-between mb-1.5">
          <div class="flex items-center gap-2">
            <MemoryStick class="w-3.5 h-3.5 text-slate-400 dark:text-slate-500" />
            <span class="text-sm font-medium">{t('system.storage.ram')}</span>
          </div>
          <div class="text-sm font-mono tabular-nums">
            <span class="font-semibold">{formatBytesCompact(memory.used)}</span>
            <span class="text-slate-400 dark:text-slate-500">
              / {formatBytesCompact(memory.total)}</span
            >
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
        <div
          class="grid grid-cols-4 mt-2 text-[10px] font-mono tabular-nums text-slate-400 dark:text-slate-500"
        >
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
