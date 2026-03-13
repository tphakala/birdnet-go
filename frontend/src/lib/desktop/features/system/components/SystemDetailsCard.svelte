<script lang="ts">
  import { t } from '$lib/i18n';
  import {
    Monitor,
    Server,
    Network,
    Clock,
    Globe,
    Cpu,
    Thermometer,
    Container,
  } from '@lucide/svelte';
  import { formatUptimeCompact } from '$lib/utils/formatters';

  interface Props {
    osDisplay: string;
    systemModel?: string;
    hostname: string;
    uptimeSeconds: number;
    timeZone?: string;
    cpuCores: number;
    cpuArch?: string;
    cpuModel?: string;
    environment?: string;
    virtualization?: string;
    temperatureAvailable: boolean;
    temperatureValue: number;
    tempSymbol: string;
  }

  let {
    osDisplay,
    systemModel,
    hostname,
    uptimeSeconds,
    timeZone,
    cpuCores,
    cpuArch,
    cpuModel,
    environment,
    virtualization,
    temperatureAvailable,
    temperatureValue,
    tempSymbol,
  }: Props = $props();

  let cpuDisplay = $derived([cpuArch, cpuModel].filter(Boolean).join(' / '));

  let environmentDisplay = $derived(
    environment && virtualization ? `${environment} (${virtualization})` : (environment ?? '')
  );

  const containerTypes = ['Docker', 'Podman', 'LXC', 'Container', 'systemd-nspawn'];
  let isContainer = $derived(containerTypes.some(ct => environment?.startsWith(ct) ?? false));
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <h3 class="text-xs font-semibold uppercase tracking-wider mb-3 text-muted">
    {t('system.systemInfo.title')}
  </h3>
  <div class="space-y-2.5">
    <div class="flex items-center gap-3">
      <Monitor class="w-3.5 h-3.5 shrink-0 text-muted" />
      <span class="text-sm truncate">{osDisplay}</span>
    </div>
    {#if environment}
      <div class="flex items-center gap-3" title={t('system.systemInfo.environment')}>
        {#if isContainer}
          <Container class="w-3.5 h-3.5 shrink-0 text-muted" />
        {:else}
          <Server class="w-3.5 h-3.5 shrink-0 text-muted" />
        {/if}
        <span class="text-sm truncate">{environmentDisplay}</span>
      </div>
    {/if}
    {#if hostname}
      <div class="flex items-center gap-3">
        <Network class="w-3.5 h-3.5 shrink-0 text-muted" />
        <span class="text-sm truncate">{hostname}</span>
      </div>
    {/if}
    {#if systemModel}
      <div class="flex items-center gap-3">
        <Server class="w-3.5 h-3.5 shrink-0 text-muted" />
        <span class="text-sm">{systemModel}</span>
      </div>
    {/if}
    <div class="flex items-center gap-3">
      <Clock class="w-3.5 h-3.5 shrink-0 text-muted" />
      <span class="text-sm">{formatUptimeCompact(uptimeSeconds)}</span>
    </div>
    {#if timeZone}
      <div class="flex items-center gap-3">
        <Globe class="w-3.5 h-3.5 shrink-0 text-muted" />
        <span class="text-sm">{timeZone}</span>
      </div>
    {/if}
    <div class="flex items-center gap-3">
      <Cpu class="w-3.5 h-3.5 shrink-0 text-muted" />
      <span class="text-sm">{cpuCores} {t('system.systemInfo.cpuCores')}</span>
    </div>
    {#if cpuDisplay}
      <div class="flex items-center gap-3" title={t('system.systemInfo.cpuDetails')}>
        <Cpu class="w-3.5 h-3.5 shrink-0 text-muted" />
        <span class="text-sm truncate">{cpuDisplay}</span>
      </div>
    {/if}
    {#if temperatureAvailable}
      <div class="flex items-center gap-3">
        <Thermometer class="w-3.5 h-3.5 shrink-0 text-muted" />
        <span class="text-sm">{temperatureValue.toFixed(1)}{tempSymbol}</span>
      </div>
    {/if}
  </div>
</div>
