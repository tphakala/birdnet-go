<script lang="ts">
  import { t } from '$lib/i18n';
  import { Monitor, Server, Clock, Globe, Cpu, Thermometer } from '@lucide/svelte';
  import { formatUptimeCompact } from '$lib/utils/formatters';

  interface Props {
    osDisplay: string;
    systemModel?: string;
    uptimeSeconds: number;
    timeZone?: string;
    cpuCores: number;
    temperatureAvailable: boolean;
    temperatureValue: number;
    tempSymbol: string;
  }

  let {
    osDisplay,
    systemModel,
    uptimeSeconds,
    timeZone,
    cpuCores,
    temperatureAvailable,
    temperatureValue,
    tempSymbol,
  }: Props = $props();
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <h3
    class="text-xs font-semibold uppercase tracking-wider mb-3 text-slate-400 dark:text-slate-500"
  >
    {t('system.systemInfo.title')}
  </h3>
  <div class="space-y-2.5">
    <div class="flex items-center gap-3">
      <Monitor class="w-3.5 h-3.5 shrink-0 text-slate-400 dark:text-slate-500" />
      <span class="text-sm truncate">{osDisplay}</span>
    </div>
    {#if systemModel}
      <div class="flex items-center gap-3">
        <Server class="w-3.5 h-3.5 shrink-0 text-slate-400 dark:text-slate-500" />
        <span class="text-sm">{systemModel}</span>
      </div>
    {/if}
    <div class="flex items-center gap-3">
      <Clock class="w-3.5 h-3.5 shrink-0 text-slate-400 dark:text-slate-500" />
      <span class="text-sm">{formatUptimeCompact(uptimeSeconds)}</span>
    </div>
    {#if timeZone}
      <div class="flex items-center gap-3">
        <Globe class="w-3.5 h-3.5 shrink-0 text-slate-400 dark:text-slate-500" />
        <span class="text-sm">{timeZone}</span>
      </div>
    {/if}
    <div class="flex items-center gap-3">
      <Cpu class="w-3.5 h-3.5 shrink-0 text-slate-400 dark:text-slate-500" />
      <span class="text-sm">{cpuCores} {t('system.systemInfo.cpuCores')}</span>
    </div>
    {#if temperatureAvailable}
      <div class="flex items-center gap-3">
        <Thermometer class="w-3.5 h-3.5 shrink-0 text-slate-400 dark:text-slate-500" />
        <span class="text-sm">{temperatureValue.toFixed(1)}{tempSymbol}</span>
      </div>
    {/if}
  </div>
</div>
