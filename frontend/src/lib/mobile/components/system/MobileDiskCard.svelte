<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';

  interface DiskInfo {
    mountpoint: string;
    total: number;
    used: number;
    usage_percent: number;
  }

  interface Props {
    disks: DiskInfo[];
    loading?: boolean;
    error?: string | null;
    className?: string;
  }

  let { disks, loading = false, error = null, className = '' }: Props = $props();

  function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  }

  function getProgressColor(percent: number): string {
    if (percent > 90) return 'bg-error';
    if (percent > 70) return 'bg-warning';
    return 'bg-primary';
  }
</script>

<div class={cn('card bg-base-100 shadow-sm', className)}>
  <div class="card-body p-4">
    <h3 class="mb-3 font-semibold">{t('system.diskUsage.title')}</h3>

    {#if loading}
      <div class="flex justify-center py-4">
        <span class="loading loading-spinner loading-sm"></span>
      </div>
    {:else if error}
      <div class="text-sm text-error">{error}</div>
    {:else if disks.length === 0}
      <div class="text-sm text-base-content/60">{t('system.diskUsage.emptyMessage')}</div>
    {:else}
      <div class="space-y-4">
        {#each disks as disk (disk.mountpoint)}
          <div>
            <div class="mb-1 flex justify-between text-sm">
              <span class="font-mono text-base-content/70">{disk.mountpoint}</span>
              <span class="text-base-content/60">
                {formatBytes(disk.used)} / {formatBytes(disk.total)}
              </span>
            </div>
            <div class="h-2 w-full overflow-hidden rounded-full bg-base-200">
              <div
                class={cn(
                  'h-full rounded-full transition-all',
                  getProgressColor(disk.usage_percent)
                )}
                style:width="{disk.usage_percent}%"
              ></div>
            </div>
            <div class="mt-1 text-right text-xs text-base-content/50">
              {disk.usage_percent.toFixed(1)}%
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>
