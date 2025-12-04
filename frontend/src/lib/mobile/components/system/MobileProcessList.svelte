<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import { navigationIcons } from '$lib/utils/icons';

  interface ProcessInfo {
    pid: number;
    name: string;
    status: string;
    cpu: number;
    memory: number;
    uptime: number;
  }

  interface Props {
    processes: ProcessInfo[];
    loading?: boolean;
    error?: string | null;
    expanded?: boolean;
    onToggle?: () => void;
    className?: string;
  }

  let {
    processes,
    loading = false,
    error = null,
    expanded = false,
    onToggle,
    className = '',
  }: Props = $props();

  function formatUptime(seconds: number): string {
    const hours = Math.floor(seconds / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    if (hours > 0) return `${hours}h ${minutes}m`;
    return `${minutes}m`;
  }
</script>

<div class={cn('card bg-base-100 shadow-sm', className)}>
  <button
    class="flex w-full items-center justify-between p-4 text-left"
    onclick={onToggle}
    aria-expanded={expanded}
  >
    <h3 class="font-semibold">{t('system.processInfo.title')}</h3>
    <span class="transition-transform duration-200" class:rotate-180={expanded}>
      {@html navigationIcons.chevronDown}
    </span>
  </button>

  {#if expanded}
    <div class="px-4 pb-4">
      {#if loading}
        <div class="flex justify-center py-4">
          <span class="loading loading-spinner loading-sm"></span>
        </div>
      {:else if error}
        <div class="text-sm text-error">{error}</div>
      {:else if processes.length === 0}
        <div class="text-sm text-base-content/60">{t('system.processInfo.noProcesses')}</div>
      {:else}
        <div class="overflow-x-auto">
          <table class="table table-xs">
            <thead>
              <tr>
                <th>Name</th>
                <th class="text-right">CPU</th>
                <th class="text-right">Mem</th>
                <th class="text-right">Uptime</th>
              </tr>
            </thead>
            <tbody>
              {#each processes as process (process.pid)}
                <tr>
                  <td class="max-w-[120px] truncate font-mono text-xs">{process.name}</td>
                  <td class="text-right">{process.cpu.toFixed(1)}%</td>
                  <td class="text-right">{process.memory.toFixed(1)}%</td>
                  <td class="text-right text-base-content/60">{formatUptime(process.uptime)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>
  {/if}
</div>
