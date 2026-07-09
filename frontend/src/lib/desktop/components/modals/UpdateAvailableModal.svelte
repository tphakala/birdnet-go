<script lang="ts">
  /**
   * UpdateAvailableModal
   *
   * "What's changed" window for a newer build: shows the latest version details
   * (title, released date, channel, changelog from the manifest) with a link to
   * the GitHub release page. Driven by /api/v2/system/update-check.
   */
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import { t } from '$lib/i18n';
  import { AlertTriangle, ExternalLink } from '@lucide/svelte';
  import type { UpdateInfo } from '$lib/types/update';

  interface Props {
    isOpen: boolean;
    info: UpdateInfo;
    currentVersion?: string;
    onClose: () => void;
  }

  let { isOpen, info, currentVersion, onClose }: Props = $props();

  // Guard a malformed timestamp so the modal never shows "Invalid Date".
  let releasedDate = $derived.by(() => {
    if (!info.releasedAt) return '';
    const parsed = new Date(info.releasedAt);
    return Number.isNaN(parsed.getTime()) ? '' : parsed.toLocaleDateString();
  });
  let latestLabel = $derived(info.latestName || info.latestVersion || '');
</script>

<Modal {isOpen} {onClose} size="lg" title={t('navigation.update.title')} showCloseButton>
  <div class="space-y-4">
    {#if info.critical}
      <div
        class="flex items-start gap-2 rounded-lg p-3 bg-[color-mix(in_srgb,var(--color-error)_15%,transparent)] text-[var(--color-error)]"
        role="alert"
      >
        <AlertTriangle class="size-5 shrink-0" />
        <span class="text-sm">{t('navigation.update.critical')}</span>
      </div>
    {/if}

    <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
      <dt class="text-[var(--color-base-content)]/60">{t('navigation.update.currentVersion')}</dt>
      <dd class="font-medium">{currentVersion ?? info.currentVersion ?? ''}</dd>
      <dt class="text-[var(--color-base-content)]/60">{t('navigation.update.latestVersion')}</dt>
      <dd class="font-medium">{latestLabel}</dd>
      {#if releasedDate}
        <dt class="text-[var(--color-base-content)]/60">{t('navigation.update.released')}</dt>
        <dd>{releasedDate}</dd>
      {/if}
      {#if info.channel}
        <dt class="text-[var(--color-base-content)]/60">{t('navigation.update.channel')}</dt>
        <dd class="capitalize">{info.channel}</dd>
      {/if}
    </dl>

    {#if info.notes}
      <div>
        <h4 class="text-sm font-semibold mb-1">{t('navigation.update.whatsChanged')}</h4>
        <div
          class="max-h-64 overflow-y-auto rounded-lg bg-[var(--color-base-200)] p-3 text-xs whitespace-pre-wrap"
        >
          {info.notes}
        </div>
      </div>
    {/if}
  </div>

  {#snippet footer()}
    <div class="flex flex-wrap items-center justify-end gap-3">
      <button
        type="button"
        class="text-sm px-3 py-1.5 rounded-lg text-[var(--color-base-content)]/70 hover:bg-[var(--color-base-200)]"
        onclick={onClose}
      >
        {t('common.buttons.close')}
      </button>
      {#if info.releaseURL}
        <a
          href={info.releaseURL}
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex items-center gap-1 text-sm px-3 py-1.5 rounded-lg bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:bg-[var(--color-primary)]/90"
        >
          {t('navigation.update.viewRelease')}<ExternalLink class="size-3.5" />
        </a>
      {/if}
    </div>
  {/snippet}
</Modal>
