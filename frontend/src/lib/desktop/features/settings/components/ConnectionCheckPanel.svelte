<!--
  ConnectionCheckPanel

  Shows how the current browser connection reaches BirdNET-Go, as reported by
  GET /api/v2/settings/connection for the saved trusted-proxy list, and offers the one
  action that fixes the common case. Presentational: the result, the loading and error
  state and every form fact come in as props, and actions go out through callbacks.

  Statuses (see proxytrust.ConnectionStatus): direct, trusted_proxy (success),
  trusted_proxy_unverified, untrusted_public_proxy, untrusted_proxy and
  untrusted_proxy_unverifiable (warnings). Only untrusted_proxy offers "Trust {peer}",
  since only there listing the peer is known to verify the client; the two unverified
  statuses offer "Trust {peer} and Cloudflare" when the server says the Cloudflare preset
  would verify the client.

  @component
-->
<script lang="ts">
  import { tick } from 'svelte';
  import { RefreshCw, ShieldCheck, CircleCheck, Info } from '@lucide/svelte';
  import SettingsButton from '$lib/desktop/features/settings/components/SettingsButton.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import { t } from '$lib/i18n';
  import { generateId } from '$lib/utils/uuid';
  import { isSingleHost } from '$lib/utils/networkAddress';
  import type { ConnectionCheckResult, ConnectionStatus } from '$lib/utils/settingsApi';

  interface Props {
    /** The last check, or null before the first one finishes or after an error. */
    result: ConnectionCheckResult | null;
    /** A check is running; the panel shows that instead of an earlier result. */
    loading: boolean;
    /** Why the last check failed, or null. */
    error: string | null;
    /** The form's trusted-proxy list differs from the saved one the check describes. */
    stale: boolean;
    /**
     * The first valid row of the form's list that contains the checked peer (the peer
     * itself or a range around it), or null when none does.
     */
    peerCoveredBy: string | null;
    /** The form's list has the Cloudflare preset on. */
    cloudflareEnabled: boolean;
    /** Disables the trust actions (for example while settings save). */
    disabled?: boolean;
    onRecheck: () => void;
    /** Add the peer to the form's list. */
    onTrust: () => void;
    /** Add the peer (if missing) and turn the Cloudflare preset on in the form. */
    onTrustWithCloudflare: () => void;
  }

  let addedNote = $state<HTMLElement>();

  // The trust button is replaced by the "added" note; move focus there so it is not lost.
  async function runAndFocusNote(action: () => void) {
    action();
    await tick();
    addedNote?.focus();
  }

  let {
    result,
    loading,
    error,
    stale,
    peerCoveredBy,
    cloudflareEnabled,
    disabled = false,
    onRecheck,
    onTrust,
    onTrustWithCloudflare,
  }: Props = $props();

  type Tone = 'success' | 'warning' | 'info';
  const TONES: Record<ConnectionStatus, Tone> = {
    direct: 'info',
    trusted_proxy: 'success',
    trusted_proxy_unverified: 'warning',
    untrusted_public_proxy: 'warning',
    untrusted_proxy: 'warning',
    untrusted_proxy_unverifiable: 'warning',
  };

  const titleId = generateId('connection-check-title');

  let status = $derived(result?.status);
  let peer = $derived(result?.peerAddress ?? '');
  let client = $derived(result?.verifiedClientAddress ?? '');

  // The statuses where the peer is known but its headers did not verify
  let unverifiedWithPeer = $derived(
    (status === 'untrusted_proxy_unverifiable' || status === 'trusted_proxy_unverified') &&
      peer !== ''
  );
  let canTrust = $derived(status === 'untrusted_proxy' && peer !== '');
  let canTrustWithCloudflare = $derived(
    unverifiedWithPeer && (result?.cloudflarePresetWouldVerify ?? false)
  );
  // The one action the panel offers, if any
  let trustAction = $derived(
    canTrust
      ? { label: t('settings.security.reverseProxy.check.trust', { peer }), run: onTrust }
      : canTrustWithCloudflare
        ? {
            label: t('settings.security.reverseProxy.check.trustWithCloudflare', { peer }),
            run: onTrustWithCloudflare,
          }
        : null
  );
  let peerListed = $derived(peerCoveredBy !== null);
  // A range around the peer covers it as well as the peer itself; say which row does.
  let coveringRange = $derived(
    peerCoveredBy !== null && !isSingleHost(peerCoveredBy) ? peerCoveredBy : null
  );
  let trustApplied = $derived(
    canTrustWithCloudflare ? peerListed && cloudflareEnabled : peerListed
  );
  let errorTitle = $derived(t('settings.security.reverseProxy.check.error'));

  // Text for the always-present status region, so a running or finished check is
  // announced politely. A failure goes to the alert region instead.
  let announcement = $derived.by(() => {
    if (loading) return t('settings.security.reverseProxy.check.checking');
    if (!error && status) return t(`settings.security.reverseProxy.status.${status}.title`);
    return '';
  });
  // Text for the always-present alert region, so a failed check is announced as an alert.
  let errorAnnouncement = $derived(error ? errorTitle : '');
</script>

<section aria-labelledby={titleId}>
  <div class="flex items-center justify-between gap-3">
    <h4 id={titleId} class="text-sm font-semibold">
      {t('settings.security.reverseProxy.check.title')}
    </h4>
    <!-- aria-disabled rather than disabled, so a keyboard user keeps focus here while it runs -->
    <SettingsButton
      variant="ghost"
      aria-disabled={loading ? 'true' : undefined}
      onclick={onRecheck}
    >
      <RefreshCw class="size-4 {loading ? 'animate-spin' : ''}" aria-hidden="true" />
      {t('settings.security.reverseProxy.check.recheck')}
    </SettingsButton>
  </div>

  <div class="sr-only" role="status">{announcement}</div>
  <div class="sr-only" role="alert">{errorAnnouncement}</div>

  <div class="mt-2">
    {#if loading}
      <div class="flex items-center gap-2 py-2 text-sm text-muted">
        <LoadingSpinner size="sm" aria-hidden="true" />
        <span>{t('settings.security.reverseProxy.check.checking')}</span>
      </div>
    {:else if error}
      <!-- The alert region above announces the failure; this box is not a live region -->
      <ErrorAlert type="error" role={undefined}>
        {#snippet children()}
          <p class="font-medium">{errorTitle}</p>
          {#if error !== errorTitle}
            <p class="mt-1 text-xs">{error}</p>
          {/if}
        {/snippet}
      </ErrorAlert>
    {:else if result && status}
      <!-- The status region above announces the outcome; this box is not a live region -->
      <ErrorAlert type={TONES[status]} role={undefined}>
        {#snippet children()}
          <p class="font-medium">{t(`settings.security.reverseProxy.status.${status}.title`)}</p>

          {#if status === 'direct'}
            {#if peer}
              <p class="mt-1">{t('settings.security.reverseProxy.status.direct.body', { peer })}</p>
              <p class="mt-1 text-xs">
                {t('settings.security.reverseProxy.status.direct.hint', { peer })}
              </p>
            {:else}
              <p class="mt-1">{t('settings.security.reverseProxy.status.direct.bodyNoPeer')}</p>
            {/if}
          {:else if status === 'trusted_proxy'}
            <p class="mt-1">
              {t('settings.security.reverseProxy.status.trusted_proxy.body', { peer, client })}
            </p>
            <p class="mt-1 text-xs">
              {t('settings.security.reverseProxy.status.trusted_proxy.hint', { client })}
            </p>
          {:else if status === 'trusted_proxy_unverified'}
            <!-- With Cloudflare in front the generic causes would blame a proxy that works -->
            {#if !canTrustWithCloudflare}
              <p class="mt-1">
                {t('settings.security.reverseProxy.status.trusted_proxy_unverified.body', { peer })}
              </p>
              <ul class="mt-1 list-disc pl-5 space-y-0.5">
                <li>
                  {t('settings.security.reverseProxy.status.trusted_proxy_unverified.causeRange')}
                </li>
                <li>
                  {t('settings.security.reverseProxy.status.trusted_proxy_unverified.causeHeaders')}
                </li>
                <li>
                  {t(
                    'settings.security.reverseProxy.status.trusted_proxy_unverified.causeConflict'
                  )}
                </li>
                <li>{t('settings.security.reverseProxy.check.selfClientCause')}</li>
              </ul>
            {/if}
          {:else if status === 'untrusted_public_proxy'}
            <p class="mt-1">
              {t('settings.security.reverseProxy.status.untrusted_public_proxy.body', { peer })}
            </p>
            {#if result.peerIsCloudflareEdge && !cloudflareEnabled}
              <p class="mt-1 font-medium">
                {t('settings.security.reverseProxy.check.cloudflareEdge', { peer })}
              </p>
            {/if}
          {:else if status === 'untrusted_proxy'}
            <p class="mt-1">
              {t('settings.security.reverseProxy.status.untrusted_proxy.body', { peer })}
            </p>
          {:else if status === 'untrusted_proxy_unverifiable' && !canTrustWithCloudflare}
            {#if peer}
              <p class="mt-1">
                {t('settings.security.reverseProxy.status.untrusted_proxy_unverifiable.body', {
                  peer,
                })}
              </p>
            {:else}
              <p class="mt-1">
                {t('settings.security.reverseProxy.status.untrusted_proxy_unverifiable.bodyNoPeer')}
              </p>
            {/if}
            <p class="mt-1">
              {t('settings.security.reverseProxy.status.untrusted_proxy_unverifiable.fix')}
            </p>
            <p class="mt-1">{t('settings.security.reverseProxy.check.selfClientCause')}</p>
          {/if}

          {#if unverifiedWithPeer && !canTrustWithCloudflare}
            <p class="mt-1 text-xs">
              {t('settings.security.reverseProxy.check.chainHint', { peer })}
            </p>
          {/if}

          {#if canTrustWithCloudflare}
            <p class="mt-2 font-medium">
              {t('settings.security.reverseProxy.check.cloudflareInFront', { peer })}
            </p>
          {/if}

          {#if trustAction && trustApplied}
            <p
              bind:this={addedNote}
              tabindex="-1"
              class="mt-3 flex items-center gap-1.5 font-medium focus:outline-none"
            >
              <CircleCheck class="size-4 shrink-0 text-[var(--color-success)]" aria-hidden="true" />
              {coveringRange
                ? t('settings.security.reverseProxy.check.coveredBy', { range: coveringRange })
                : t('settings.security.reverseProxy.check.added')}
            </p>
          {:else if trustAction}
            {@const action = trustAction}
            <div class="mt-3 flex flex-wrap items-center gap-x-3 gap-y-2">
              <SettingsButton {disabled} onclick={() => runAndFocusNote(action.run)}>
                <ShieldCheck class="size-4" aria-hidden="true" />
                {action.label}
              </SettingsButton>
              {#if canTrust}
                <p class="text-xs">
                  {t('settings.security.reverseProxy.check.trustWarning', { peer })}
                </p>
              {/if}
            </div>
          {/if}
        {/snippet}
      </ErrorAlert>
    {/if}
  </div>

  {#if stale && !loading}
    <p class="mt-3 flex items-start gap-1.5 text-xs text-muted">
      <Info class="size-3.5 shrink-0 mt-px" aria-hidden="true" />
      <span>{t('settings.security.reverseProxy.check.stale')}</span>
    </p>
  {/if}
</section>
