<!--
  ReverseProxySection

  The "Reverse Proxy" card of the Security page's Server tab. Edits
  security.trustedProxies, which mixes proxy addresses with the reserved `cloudflare`
  preset token: the address rows go through NetworkAddressListInput and the token
  through the Trust Cloudflare checkbox (see utils/trustedProxies.ts for how the two are
  split and put back without reordering the saved list).

  It also runs the connection check (GET /api/v2/settings/connection) and shows it in
  ConnectionCheckPanel. The check describes the saved settings, so it runs once the
  settings have loaded and again whenever the saved list changes (after a save), and the
  panel says so while the form's list differs from the saved one.

  Opening the page with #reverse-proxy in the URL (the cross-link from the Exceptions
  tab) scrolls to and focuses this card, then drops the hash.

  @component
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import ConnectionCheckPanel from '$lib/desktop/features/settings/components/ConnectionCheckPanel.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import NetworkAddressListInput from '$lib/desktop/components/forms/NetworkAddressListInput.svelte';
  import Badge from '$lib/desktop/components/ui/Badge.svelte';
  import {
    partitionTrustedProxies,
    recombineTrustedProxies,
    setCloudflarePreset,
  } from '$lib/desktop/features/settings/utils/trustedProxies';
  import {
    isSingleHost,
    networkAddressKey,
    validateNetworkAddress,
  } from '$lib/utils/networkAddress';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import { settingsAPI, type ConnectionCheckResult } from '$lib/utils/settingsApi';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { t } from '$lib/i18n';

  interface Props {
    /** The form's security.trustedProxies. */
    trustedProxies: string[];
    /** The saved security.trustedProxies, which the connection check describes. */
    savedTrustedProxies: string[];
    /** Settings have loaded; the first check waits for this. */
    dataLoaded: boolean;
    /** Disables editing (while settings load or save). */
    disabled: boolean;
    /** Shows the card's unsaved-changes badge. */
    hasChanges: boolean;
    /** Called with the whole new trusted-proxy list. */
    onUpdate: (_list: string[]) => void;
    /** Called with true while the address input holds text that was not added. */
    onPendingChange?: (_hasPending: boolean) => void;
  }

  let {
    trustedProxies,
    savedTrustedProxies,
    dataLoaded,
    disabled,
    hasChanges,
    onUpdate,
    onPendingChange,
  }: Props = $props();

  /** Element id the Exceptions tab links to (SETTINGS_ROUTES.securityReverseProxy). */
  const ANCHOR_ID = 'reverse-proxy';
  const ACCEPT = 'ip-or-cidr';

  let partition = $derived(partitionTrustedProxies(trustedProxies));
  let stale = $derived(hasSettingsChanged(savedTrustedProxies, trustedProxies));

  let result = $state<ConnectionCheckResult | null>(null);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let anchor = $state<HTMLElement>();

  // Whether the checked peer is already a valid row; invalid rows never count.
  let peerListed = $derived.by(() => {
    const peerKey = result?.peerAddress ? networkAddressKey(result.peerAddress) : null;
    if (peerKey === null) return false;
    return partition.entries.some(
      entry =>
        validateNetworkAddress(entry, ACCEPT) === null && networkAddressKey(entry) === peerKey
    );
  });

  function updateEntries(entries: string[]) {
    onUpdate(recombineTrustedProxies(entries, partition.hasCloudflare, savedTrustedProxies));
  }

  function updateCloudflare(enabled: boolean) {
    onUpdate(setCloudflarePreset(trustedProxies, enabled, savedTrustedProxies));
  }

  function entriesWithPeer(): string[] {
    const peer = result?.peerAddress ?? '';
    return peerListed || peer === '' ? partition.entries : [...partition.entries, peer];
  }

  function trustPeer() {
    if (!peerListed) updateEntries(entriesWithPeer());
  }

  function trustPeerWithCloudflare() {
    onUpdate(recombineTrustedProxies(entriesWithPeer(), true, savedTrustedProxies));
  }

  // Only the newest request may change the panel, so a slow older response cannot
  // replace a newer one.
  let latestRequest = 0;

  async function runCheck() {
    const request = ++latestRequest;
    loading = true;
    error = null;
    try {
      const next = await settingsAPI.security.getConnection();
      if (request !== latestRequest) return;
      result = next;
    } catch (err) {
      if (request !== latestRequest) return;
      result = null;
      error = err instanceof Error ? err.message : t('settings.security.reverseProxy.check.error');
    } finally {
      if (request === latestRequest) loading = false;
    }
  }

  // The first check runs once the settings have loaded; later checks run when the saved
  // list changes, which happens after a save. Edits to the form alone do not re-check.
  let savedKey = $derived(JSON.stringify(savedTrustedProxies));
  let checkedKey: string | null = null;
  $effect(() => {
    if (!dataLoaded) return;
    if (checkedKey !== savedKey) {
      checkedKey = savedKey;
      void runCheck();
    }
  });

  onMount(() => {
    void focusFromHash();
    return () => {
      // Drop any response still on its way
      latestRequest++;
    };
  });

  async function focusFromHash() {
    if (window.location.hash !== `#${ANCHOR_ID}`) return;
    await tick();
    anchor?.scrollIntoView({ block: 'start' });
    anchor?.focus({ preventScroll: true });
    navigation.redirect(window.location.pathname + window.location.search);
  }
</script>

{#snippet rangeBadge(row: { value: string; valid: boolean })}
  {#if row.valid && !isSingleHost(row.value)}
    <Badge variant="neutral" size="sm" text={t('settings.security.reverseProxy.rangeBadge')} />
  {/if}
{/snippet}

<div
  id={ANCHOR_ID}
  bind:this={anchor}
  tabindex="-1"
  class="scroll-mt-4 rounded-lg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-primary)]"
>
  <SettingsSection
    title={t('settings.security.reverseProxy.title')}
    description={t('settings.security.reverseProxy.description')}
    {hasChanges}
  >
    <div class="space-y-4">
      <ConnectionCheckPanel
        {result}
        {loading}
        {error}
        {stale}
        {peerListed}
        cloudflareEnabled={partition.hasCloudflare}
        {disabled}
        onRecheck={runCheck}
        onTrust={trustPeer}
        onTrustWithCloudflare={trustPeerWithCloudflare}
      />

      <Checkbox
        checked={partition.hasCloudflare}
        label={t('settings.security.reverseProxy.cloudflareLabel')}
        helpText={t('settings.security.reverseProxy.cloudflareHelp')}
        {disabled}
        onchange={updateCloudflare}
      />

      <NetworkAddressListInput
        values={partition.entries}
        onUpdate={updateEntries}
        accept={ACCEPT}
        label={t('settings.security.reverseProxy.listLabel')}
        helpText={t('settings.security.reverseProxy.listHelp')}
        placeholder={t('settings.security.reverseProxy.placeholder')}
        emptyStateMessage={t('settings.security.reverseProxy.emptyState')}
        {disabled}
        rowAnnotation={rangeBadge}
        {onPendingChange}
      />

      <SettingsNote>
        <ul class="list-disc pl-5 space-y-1">
          <li>{t('settings.security.reverseProxy.ruleOwnAddress')}</li>
          <li>{t('settings.security.reverseProxy.ruleHeaders')}</li>
        </ul>
      </SettingsNote>
    </div>
  </SettingsSection>
</div>
