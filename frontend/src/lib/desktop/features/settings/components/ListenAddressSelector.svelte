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

  let { listen, onchange, disabled = false }: Props = $props();

  // Parse host:port from listen string
  let host = $state('0.0.0.0');
  let port = $state(8090);

  // Dropdown state
  let dropOpen = $state(false);

  // Default interfaces used as fallback and merged with API results
  const defaultInterfaces: NetworkInterface[] = [
    { address: '0.0.0.0', name: 'all', label: 'All interfaces', status: 'up' },
    { address: '127.0.0.1', name: 'lo', label: 'Loopback', status: 'up' },
  ];

  let interfaces = $state<NetworkInterface[]>([...defaultInterfaces]);

  // Parse the listen prop into host and port, with IPv6 support
  function parseListen(value: string): { host: string; port: number } {
    // Handle bracketed IPv6 addresses e.g. [::1]:8090
    if (value.startsWith('[') && value.includes(']')) {
      const endBracketIndex = value.lastIndexOf(']');
      if (endBracketIndex > 0 && value.charAt(endBracketIndex + 1) === ':') {
        const h = value.substring(0, endBracketIndex + 1);
        const portStr = value.substring(endBracketIndex + 2);
        const p = parseInt(portStr, 10);
        return { host: h, port: isNaN(p) ? 8090 : p };
      }
      // Bracketed IPv6 without a port
      return { host: value, port: 8090 };
    }

    const lastColon = value.lastIndexOf(':');
    // No colon or multiple colons (bare IPv6) — treat as host-only
    if (lastColon === -1 || value.indexOf(':') !== lastColon) {
      return { host: value, port: 8090 };
    }

    // IPv4 or hostname with port
    const h = value.substring(0, lastColon);
    const p = parseInt(value.substring(lastColon + 1), 10);
    return { host: h, port: isNaN(p) ? 8090 : p };
  }

  // Reactively sync host/port when the listen prop changes (e.g. async settings load)
  $effect(() => {
    const parsed = parseListen(listen);
    host = parsed.host;
    port = parsed.port;
    ensureCurrentHostInList();
  });

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
        // Merge fetched interfaces with defaults so 0.0.0.0 and 127.0.0.1 are always available
        const fetchedInterfaces = response.interfaces;
        const fetchedAddresses = new Set(fetchedInterfaces.map(i => i.address));
        interfaces = [
          ...fetchedInterfaces,
          ...defaultInterfaces.filter(i => !fetchedAddresses.has(i.address)),
        ];
      }
    } catch {
      // Keep fallback defaults (0.0.0.0 + 127.0.0.1)
    } finally {
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

  // Translate built-in interface labels; pass through labels from the OS
  function getLocalizedLabel(iface: NetworkInterface): string {
    if (iface.name === 'all' && iface.address === '0.0.0.0') {
      return t('settings.integration.observability.listenAddress.allInterfaces');
    }
    if (iface.name === 'lo' && iface.address === '127.0.0.1') {
      return t('settings.integration.observability.listenAddress.loopback');
    }
    return iface.label;
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
    const target = (event.target as HTMLElement | null)?.closest('[data-listen-dropdown]');
    if (!target) {
      dropOpen = false;
    }
  }

  // Icon for interface type
  function getIcon(iface: NetworkInterface) {
    if (iface.address === '0.0.0.0') return Globe;
    return Monitor;
  }

  // Derived selected interface with custom fallback instead of interfaces[0]
  let selectedIface = $derived(
    interfaces.find(i => i.address === host) ?? {
      address: host,
      name: 'custom',
      label: t('settings.integration.observability.listenAddress.customAddress'),
      status: 'unknown',
    }
  );
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
                {getLocalizedLabel(iface)} ({iface.name}){#if iface.status === 'down'}&middot; {t(
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
