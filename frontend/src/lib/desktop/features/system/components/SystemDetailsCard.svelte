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
  import { isContainerEnvironment } from '$lib/desktop/features/system/environment';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import Skeleton from '$lib/desktop/components/ui/Skeleton.svelte';
  import SkeletonStatus from '$lib/desktop/components/ui/SkeletonStatus.svelte';

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
    /** Shows a skeleton of the rows until the first system info answer. */
    loading?: boolean;
    /** Why the system info could not load; replaces the rows. */
    error?: string | null;
    onRetry?: () => Promise<unknown> | unknown;
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
    loading = false,
    error = null,
    onRetry,
  }: Props = $props();

  /** Rows in the skeleton: a typical host shows eight to nine. */
  const SKELETON_ROW_WIDTHS = ['w-40', 'w-32', 'w-24', 'w-48', 'w-20', 'w-36', 'w-24', 'w-40'];

  let card = $state<HTMLDivElement>();
  // A Retry that succeeds removes the alert; focus then lands on the card
  const focusCard = () => [card];

  let cpuDisplay = $derived([cpuArch, cpuModel].filter(Boolean).join(' / '));

  let environmentDisplay = $derived(
    environment && virtualization ? `${environment} (${virtualization})` : (environment ?? '')
  );

  let isContainer = $derived(isContainerEnvironment(environment));
</script>

<div
  bind:this={card}
  tabindex="-1"
  class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-primary)]"
>
  <div class="flex items-center justify-between gap-2 mb-3">
    <h3 class="text-xs font-semibold uppercase tracking-wider text-muted">
      {t('system.systemInfo.title')}
    </h3>
    {#if loading && !error}
      <SkeletonStatus label={t('common.loading')} className="text-xs" />
    {/if}
  </div>
  {#if error}
    <ErrorAlert message={error} {onRetry} focusFallback={focusCard} />
  {:else if loading}
    <div class="space-y-2.5">
      {#each SKELETON_ROW_WIDTHS as width, i (i)}
        <div class="flex items-center gap-3">
          <Skeleton className="h-3.5 w-3.5 shrink-0" />
          <span class="text-sm"
            ><Skeleton className="inline-block align-middle h-3.5 {width}" /></span
          >
        </div>
      {/each}
    </div>
  {:else}
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
  {/if}
</div>
