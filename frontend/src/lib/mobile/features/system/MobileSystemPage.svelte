<script lang="ts">
  import { t } from '$lib/i18n';
  import { systemIcons, actionIcons } from '$lib/utils/icons';
  import { MobileResourceCard, MobileDiskCard, MobileProcessList } from '../../components/system';

  interface SystemInfo {
    os_display: string;
    hostname: string;
    uptime_seconds: number;
    num_cpu: number;
  }

  interface DiskInfo {
    mountpoint: string;
    total: number;
    used: number;
    usage_percent: number;
  }

  interface MemoryInfo {
    total: number;
    used: number;
    usedPercent: number;
  }

  interface ProcessInfo {
    pid: number;
    name: string;
    status: string;
    cpu: number;
    memory: number;
    uptime: number;
  }

  let systemInfo = $state<SystemInfo | null>(null);
  let diskInfo = $state<DiskInfo[]>([]);
  let memoryInfo = $state<MemoryInfo | null>(null);
  let cpuPercent = $state(0);
  let processes = $state<ProcessInfo[]>([]);

  let loading = $state(true);
  let error = $state<string | null>(null);
  let processesExpanded = $state(false);

  function getCsrfToken(): string {
    const meta = document.querySelector('meta[name="csrf-token"]');
    return meta?.getAttribute('content') ?? '';
  }

  async function loadSystemData(): Promise<void> {
    loading = true;
    error = null;

    try {
      const csrfToken = getCsrfToken();
      const headers = { 'X-CSRF-Token': csrfToken };

      const [infoRes, diskRes, resourceRes, processRes] = await Promise.all([
        fetch('/api/v2/system/info', { headers }),
        fetch('/api/v2/system/disks', { headers }),
        fetch('/api/v2/system/resources', { headers }),
        fetch('/api/v2/system/processes', { headers }),
      ]);

      if (infoRes.ok) {
        systemInfo = await infoRes.json();
      }

      if (diskRes.ok) {
        diskInfo = await diskRes.json();
      }

      if (resourceRes.ok) {
        const data = await resourceRes.json();
        memoryInfo = {
          total: data.memory_total,
          used: data.memory_used,
          usedPercent: data.memory_usage_percent,
        };
        cpuPercent = data.cpu_usage_percent ?? 0;
      }

      if (processRes.ok) {
        processes = await processRes.json();
      }
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to load system data';
    } finally {
      loading = false;
    }
  }

  function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  }

  function formatUptime(seconds: number): string {
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    if (days > 0) return `${days}d ${hours}h`;
    return `${hours}h`;
  }

  $effect(() => {
    loadSystemData();
  });
</script>

<div class="flex flex-col gap-4 p-4 pb-20">
  <!-- System Info Header -->
  {#if systemInfo}
    <div class="card bg-base-100 shadow-sm">
      <div class="card-body p-4">
        <div class="flex items-center gap-3">
          <div
            class="flex h-10 w-10 items-center justify-center rounded-full bg-primary/20 text-primary"
          >
            {@html systemIcons.settingsGear}
          </div>
          <div>
            <div class="font-semibold">{systemInfo.hostname}</div>
            <div class="text-sm text-base-content/60">{systemInfo.os_display}</div>
            <div class="text-xs text-base-content/50">
              {t('system.systemInfo.uptime')}: {formatUptime(systemInfo.uptime_seconds)}
            </div>
          </div>
        </div>
      </div>
    </div>
  {/if}

  <!-- Resource Cards Grid -->
  <div class="grid grid-cols-2 gap-3">
    <MobileResourceCard
      title={t('system.resourceUsage.cpu')}
      value="{cpuPercent.toFixed(1)}%"
      percent={cpuPercent}
      iconHtml={systemIcons.settingsGear}
    />

    {#if memoryInfo}
      <MobileResourceCard
        title={t('system.memoryUsage.title')}
        value={formatBytes(memoryInfo.used)}
        subtitle="of {formatBytes(memoryInfo.total)}"
        percent={memoryInfo.usedPercent}
        iconHtml={systemIcons.settingsGear}
      />
    {/if}
  </div>

  <!-- Disk Usage -->
  <MobileDiskCard disks={diskInfo} {loading} {error} />

  <!-- Processes -->
  <MobileProcessList
    {processes}
    {loading}
    {error}
    expanded={processesExpanded}
    onToggle={() => (processesExpanded = !processesExpanded)}
  />

  <!-- Refresh Button -->
  <div class="flex justify-center pt-2">
    <button class="btn btn-primary btn-sm gap-2" onclick={loadSystemData} disabled={loading}>
      {#if loading}
        <span class="loading loading-spinner loading-xs"></span>
      {:else}
        {@html actionIcons.refresh}
      {/if}
      {t('system.refreshData')}
    </button>
  </div>
</div>
