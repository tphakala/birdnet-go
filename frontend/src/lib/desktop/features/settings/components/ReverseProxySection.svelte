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
    TRUSTED_PROXY_ACCEPT,
  } from '$lib/desktop/features/settings/utils/trustedProxies';
  import { isSingleHost, networkContains } from '$lib/utils/networkAddress';
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
    /** Text typed into the address input but not added yet. Bindable. */
    draft?: string;
    /** Changing this value clears the address input's add report. */
    reportResetKey?: number;
  }

  let {
    trustedProxies,
    savedTrustedProxies,
    dataLoaded,
    disabled,
    hasChanges,
    onUpdate,
    draft = $bindable(''),
    reportResetKey,
  }: Props = $props();

  /** Element id the Exceptions tab links to (SETTINGS_ROUTES.securityReverseProxy). */
  const ANCHOR_ID = 'reverse-proxy';

  let partition = $derived(partitionTrustedProxies(trustedProxies));

  let result = $state<ConnectionCheckResult | null>(null);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let anchor = $state<HTMLElement>();
  let peer = $derived(result?.peerAddress ?? '');

  // The first row that contains the checked peer (the peer itself or a range around
  // it); an invalid row contains nothing. Trusting the peer again would add nothing.
  let peerCoveredBy = $derived(
    peer === '' ? null : (partition.entries.find(entry => networkContains(entry, peer)) ?? null)
  );
  let peerListed = $derived(peerCoveredBy !== null);

  function updateEntries(entries: string[]) {
    onUpdate(recombineTrustedProxies(entries, partition.hasCloudflare, savedTrustedProxies));
  }

  function updateCloudflare(enabled: boolean) {
    onUpdate(recombineTrustedProxies(partition.entries, enabled, savedTrustedProxies));
  }

  function entriesWithPeer(): string[] {
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
        stale={hasChanges}
        {peerCoveredBy}
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
        accept={TRUSTED_PROXY_ACCEPT}
        label={t('settings.security.reverseProxy.listLabel')}
        helpText={t('settings.security.reverseProxy.listHelp')}
        placeholder={t('settings.security.reverseProxy.placeholder')}
        emptyStateMessage={partition.hasCloudflare
          ? t('settings.security.reverseProxy.emptyStateCloudflareOnly')
          : t('settings.security.reverseProxy.emptyState')}
        {disabled}
        rowAnnotation={rangeBadge}
        bind:draft
        {reportResetKey}
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
