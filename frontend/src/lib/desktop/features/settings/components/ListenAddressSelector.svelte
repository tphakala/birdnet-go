<!--
  ListenAddressSelector.svelte

  Purpose: Compound control for selecting a listen address (IP + port).
  Fetches available network interfaces from /api/v2/system/network-interfaces
  and renders an AlertRuleEditor-style custom dropdown for IP selection plus
  an existing NumberField for port entry.

  Props:
  - listen: string - current listen address e.g. "0.0.0.0:8090"
  - onchange: (listen: string) => void - called when IP or port changes
  - disabled?: boolean - disables both controls

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import { untrack } from 'svelte';
  import { ChevronDown, Check, Globe, Monitor } from '@lucide/svelte';

  interface NetworkInterface {
    address: string;
    name: string;
    label: string;
    status: string;
  }

  interface Props {
    listen: string;
    onchange: (_listen: string) => void;
    disabled?: boolean;
  }

  let { listen: initialListen, onchange, disabled = false }: Props = $props();

  // Parse host:port from listen string
  let host = $state('0.0.0.0');
  let port = $state(8090);

  // Dropdown state
  let dropOpen = $state(false);
  let interfaces = $state<NetworkInterface[]>([
    { address: '0.0.0.0', name: 'all', label: 'All interfaces', status: 'up' },
    { address: '127.0.0.1', name: 'lo', label: 'Loopback', status: 'up' },
  ]);
  // Parse the listen prop into host and port
  function parseListen(value: string): { host: string; port: number } {
    const lastColon = value.lastIndexOf(':');
    if (lastColon === -1) return { host: value, port: 8090 };
    const h = value.substring(0, lastColon);
    const p = parseInt(value.substring(lastColon + 1), 10);
    return { host: h, port: isNaN(p) ? 8090 : p };
  }

  // Initialize from prop — untrack() reads the prop once without creating a reactive
  // dependency, so host/port are owned state (not kept in sync with the prop after mount)
  {
    const parsed = untrack(() => parseListen(initialListen));
    host = parsed.host;
    port = parsed.port;
  }

  // Fetch network interfaces on mount
  $effect(() => {
    fetchInterfaces();
  });

  async function fetchInterfaces() {
    try {
      const response = await api.get<{ interfaces: NetworkInterface[] }>(
        '/api/v2/system/network-interfaces'
      );
      if (response.interfaces && response.interfaces.length > 0) {
        interfaces = response.interfaces;
      }
    } catch {
      // Keep fallback defaults (0.0.0.0 + 127.0.0.1)
    } finally {
      // If current host isn't in the list, add it as a custom entry
      ensureCurrentHostInList();
    }
  }

  function ensureCurrentHostInList() {
    if (!interfaces.some(iface => iface.address === host)) {
      interfaces = [
        ...interfaces,
        {
          address: host,
          name: 'custom',
          label: t('settings.integration.observability.listenAddress.customAddress'),
          status: 'unknown',
        },
      ];
    }
  }

  function selectHost(address: string) {
    host = address;
    dropOpen = false;
    onchange(`${host}:${port}`);
  }

  function updatePort(newPort: number) {
    port = newPort;
    onchange(`${host}:${port}`);
  }

  function handleClickOutside(event: MouseEvent) {
    const target = event.target as Node | null;
    const el = target instanceof HTMLElement ? target : null;
    if (!el?.closest('[data-listen-dropdown]')) {
      dropOpen = false;
    }
  }

  // Icon for interface type
  function getIcon(iface: NetworkInterface) {
    if (iface.address === '0.0.0.0') return Globe;
    return Monitor;
  }

  // Derived icon for the trigger button (avoids {@const} outside {#each}/{#if})
  let selectedIface = $derived(interfaces.find(i => i.address === host) ?? interfaces[0]);
  let SelectedIcon = $derived(getIcon(selectedIface));
</script>

<svelte:document onclick={handleClickOutside} />

<div class="grid grid-cols-1 gap-6 md:grid-cols-2">
  <!-- IP Address Dropdown -->
  <div class="relative" data-listen-dropdown>
    <span class="mb-1 block text-xs font-medium text-[var(--color-base-content)]/60">
      {t('settings.integration.observability.listenAddress.ipLabel')}
    </span>
    <button
      type="button"
      {disabled}
      aria-haspopup="listbox"
      aria-expanded={dropOpen}
      aria-label={t('settings.integration.observability.listenAddress.ipLabel')}
      class="flex w-full cursor-pointer items-center gap-2 rounded-lg border bg-[var(--color-base-200)] px-3 py-2 text-left text-sm transition-all disabled:cursor-not-allowed disabled:opacity-50 {dropOpen
        ? 'border-[var(--color-primary)] ring-2 ring-[var(--color-primary)]/20'
        : 'border-[var(--color-base-300)]'}"
      onclick={() => {
        if (!disabled) dropOpen = !dropOpen;
      }}
    >
      <div
        class="flex h-5 w-5 flex-shrink-0 items-center justify-center rounded-md bg-[var(--color-primary)]/10"
      >
        <SelectedIcon class="h-3 w-3 text-[var(--color-primary)]" />
      </div>
      <span class="min-w-0 flex-1 truncate text-[var(--color-base-content)]">
        {host}
      </span>
      <ChevronDown
        class="h-3.5 w-3.5 flex-shrink-0 text-[var(--color-base-content)]/40 transition-transform {dropOpen
          ? 'rotate-180'
          : ''}"
      />
    </button>
    {#if dropOpen}
      <div
        role="listbox"
        class="absolute left-0 right-0 top-full z-50 mt-1 max-h-60 overflow-hidden overflow-y-auto rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-100)] shadow-lg"
      >
        {#each interfaces as iface (iface.address)}
          {@const Icon = getIcon(iface)}
          <button
            type="button"
            role="option"
            aria-selected={host === iface.address}
            class="flex w-full cursor-pointer items-center gap-2.5 px-3 py-2.5 text-left transition-colors hover:bg-[var(--color-base-200)] {host ===
            iface.address
              ? 'bg-[var(--color-primary)]/5'
              : ''}"
            onclick={() => selectHost(iface.address)}
          >
            <div
              class="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-lg bg-[var(--color-primary)]/10"
            >
              <Icon class="h-3.5 w-3.5 text-[var(--color-primary)]" />
            </div>
            <div class="min-w-0 flex-1">
              <div class="text-sm font-medium text-[var(--color-base-content)]">
                {iface.address}
              </div>
              <div class="text-[11px] text-[var(--color-base-content)]/40">
                {iface.label} ({iface.name}){#if iface.status === 'down'}&middot; {t(
                    'settings.integration.observability.listenAddress.interfaceDown'
                  )}{/if}
              </div>
            </div>
            {#if host === iface.address}
              <Check class="h-3.5 w-3.5 flex-shrink-0 text-[var(--color-primary)]" />
            {/if}
          </button>
        {/each}
      </div>
    {/if}
  </div>

  <!-- Port NumberField -->
  <NumberField
    label={t('settings.integration.observability.listenAddress.portLabel')}
    value={port}
    onUpdate={updatePort}
    min={1}
    max={65535}
    step={1}
    placeholder="8090"
    {disabled}
  />
</div>
