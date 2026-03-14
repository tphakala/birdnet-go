<!--
  CertificateStateDisplay Component
  Wraps loading, error, installed, and empty states for TLS certificate display.
-->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { t } from '$lib/i18n';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import CertificateInfoCard from '$lib/desktop/components/ui/CertificateInfoCard.svelte';
  import type { TLSCertificateInfo } from '$lib/utils/settingsApi';

  interface Props {
    loading: boolean;
    error: string | null;
    certInfo: TLSCertificateInfo | null;
    downloadUrl?: string;
    onRegenerate?: () => void;
    onDelete?: () => void;
    regenerateLoading?: boolean;
    deleteLoading?: boolean;
    uploadForm?: Snippet;
    children?: Snippet;
  }

  let {
    loading,
    error,
    certInfo,
    downloadUrl,
    onRegenerate,
    onDelete,
    regenerateLoading = false,
    deleteLoading = false,
    uploadForm,
    children,
  }: Props = $props();
</script>

{#if loading}
  <div class="flex items-center gap-2 py-3 mt-4">
    <div
      class="animate-spin h-4 w-4 border-2 border-[var(--color-primary)] border-t-transparent rounded-full"
    ></div>
    <span class="text-sm text-[var(--color-base-content)]/60">{t('components.tls.loading')}</span>
  </div>
{:else if error}
  <div class="mt-4">
    <ErrorAlert type="error" message={error} />
  </div>
{:else if certInfo?.installed}
  <CertificateInfoCard
    {certInfo}
    {downloadUrl}
    {onRegenerate}
    {onDelete}
    {regenerateLoading}
    {deleteLoading}
  />
  {#if children}{@render children()}{/if}
{:else if uploadForm}
  {@render uploadForm()}
{/if}
