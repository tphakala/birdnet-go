<!--
  CertificateInfoCard Component
  Displays certificate metadata with optional download, regenerate, and delete actions.
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { ShieldCheck, Download, RefreshCw, Trash2, AlertTriangle } from '@lucide/svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import type { TLSCertificateInfo } from '$lib/utils/settingsApi';

  interface Props {
    certInfo: TLSCertificateInfo;
    downloadUrl?: string;
    onRegenerate?: () => void;
    onDelete?: () => void;
    regenerateLoading?: boolean;
    deleteLoading?: boolean;
  }

  let {
    certInfo,
    downloadUrl,
    onRegenerate,
    onDelete,
    regenerateLoading = false,
    deleteLoading = false,
  }: Props = $props();
</script>

<div class="rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-3 mt-4">
  <div class="flex items-center gap-2 mb-3">
    <ShieldCheck class="w-4 h-4 text-[var(--color-success)]" />
    <span class="text-sm font-medium">{t('components.tls.certificateInstalled')}</span>
  </div>
  <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-4 gap-y-2 text-sm">
    {#if certInfo.subject}
      <div>
        <span class="text-xs text-[var(--color-base-content)]/60"
          >{t('components.tls.subject')}</span
        >
        <p class="font-mono text-xs">{certInfo.subject}</p>
      </div>
    {/if}
    {#if certInfo.issuer}
      <div>
        <span class="text-xs text-[var(--color-base-content)]/60">{t('components.tls.issuer')}</span
        >
        <p class="font-mono text-xs">{certInfo.issuer}</p>
      </div>
    {/if}
    {#if certInfo.sans && certInfo.sans.length > 0}
      <div>
        <span class="text-xs text-[var(--color-base-content)]/60">{t('components.tls.sans')}</span>
        <p class="font-mono text-xs">{certInfo.sans.join(', ')}</p>
      </div>
    {/if}
    {#if certInfo.notAfter}
      <div>
        <span class="text-xs text-[var(--color-base-content)]/60"
          >{t('components.tls.validUntil')}</span
        >
        <p class="font-mono text-xs">{certInfo.notAfter}</p>
      </div>
    {/if}
    {#if certInfo.daysUntilExpiry !== undefined}
      <div>
        <span class="text-xs text-[var(--color-base-content)]/60"
          >{t('components.tls.daysRemaining')}</span
        >
        <p
          class="font-mono text-xs"
          class:text-[var(--color-error)]={certInfo.daysUntilExpiry < 30}
        >
          {certInfo.daysUntilExpiry}
          {#if certInfo.daysUntilExpiry < 30}
            <AlertTriangle class="w-3 h-3 inline ml-1" />
          {/if}
        </p>
      </div>
    {/if}
    {#if certInfo.fingerprint}
      <div class="sm:col-span-2">
        <span class="text-xs text-[var(--color-base-content)]/60"
          >{t('components.tls.fingerprint')}</span
        >
        <p class="font-mono text-xs break-all">{certInfo.fingerprint}</p>
      </div>
    {/if}
  </div>

  {#if certInfo.daysUntilExpiry !== undefined && certInfo.daysUntilExpiry < 30}
    <ErrorAlert type="warning" message={t('components.tls.expiryWarning')} className="mt-3" />
  {/if}

  {#if downloadUrl || onRegenerate || onDelete}
    <div class="mt-3 flex gap-2 flex-wrap">
      {#if downloadUrl}
        <a
          href={downloadUrl}
          download="birdnet-go.crt"
          class="inline-flex items-center justify-center gap-2 px-3 py-1.5 rounded-lg text-xs font-medium bg-[var(--color-base-200)] border border-[var(--color-base-300)] hover:bg-[var(--color-base-300)] cursor-pointer transition-all no-underline text-[var(--color-base-content)]"
        >
          <Download class="w-3 h-3" />
          {t('components.tls.downloadCertificate')}
        </a>
      {/if}
      {#if onRegenerate}
        <button
          type="button"
          class="inline-flex items-center justify-center gap-2 px-3 py-1.5 rounded-lg text-xs font-medium bg-[var(--color-base-200)] border border-[var(--color-base-300)] hover:bg-[var(--color-base-300)] cursor-pointer transition-all disabled:opacity-50 disabled:cursor-not-allowed"
          disabled={regenerateLoading}
          onclick={onRegenerate}
        >
          {#if regenerateLoading}
            <div
              class="animate-spin h-3 w-3 border-2 border-current border-t-transparent rounded-full"
            ></div>
          {:else}
            <RefreshCw class="w-3 h-3" />
          {/if}
          {t('components.tls.regenerateButton')}
        </button>
      {/if}
      {#if onDelete}
        <button
          type="button"
          class="inline-flex items-center justify-center gap-2 px-3 py-1.5 rounded-lg text-xs font-medium text-[var(--color-error)] hover:bg-[var(--color-error)]/10 cursor-pointer transition-all disabled:opacity-50 disabled:cursor-not-allowed"
          disabled={deleteLoading}
          onclick={onDelete}
        >
          <Trash2 class="w-3 h-3" />
          {t('components.tls.removeCertificate')}
        </button>
      {/if}
    </div>
  {/if}
</div>
