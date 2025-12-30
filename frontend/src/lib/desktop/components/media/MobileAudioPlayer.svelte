<script lang="ts">
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import { t } from '$lib/i18n';
  import { X } from '@lucide/svelte';

  interface Props {
    audioUrl: string;
    speciesName?: string;
    detectionTime?: string;
    detectionId?: number | string;
    showSpectrogram?: boolean;
    onClose?: () => void;
  }

  let {
    audioUrl,
    speciesName = '',
    detectionTime = '',
    detectionId = undefined,
    showSpectrogram = true,
    onClose,
  }: Props = $props();

  function handleClose() {
    if (onClose) onClose();
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      handleClose();
    }
  }

  $effect(() => {
    document.addEventListener('keydown', handleKeydown);
    return () => {
      document.removeEventListener('keydown', handleKeydown);
    };
  });
</script>

<!-- Mobile bottom-sheet modal wrapping the existing AudioPlayer -->
<div
  class="fixed inset-0 z-50 bg-black/50 flex items-end md:hidden"
  role="dialog"
  aria-modal="true"
>
  <div class="w-full rounded-t-3xl shadow-2xl relative overflow-hidden bg-base-100">
    <!-- Header -->
    <div class="flex items-center justify-between p-4 border-b border-base-300">
      <div class="flex-1 min-w-0">
        <h3 class="font-bold text-sm truncate">{speciesName}</h3>
        {#if detectionTime}
          <p class="text-xs text-base-content opacity-60 truncate">{detectionTime}</p>
        {/if}
      </div>
      <button
        onclick={handleClose}
        class="btn btn-ghost btn-sm btn-circle"
        aria-label={t('common.aria.closeModal')}
      >
        <X class="size-5" />
      </button>
    </div>

    <!-- Player Content -->
    <div class="p-3">
      <AudioPlayer
        {audioUrl}
        detectionId={detectionId ? String(detectionId) : ''}
        responsive={true}
        {showSpectrogram}
        showDownload={true}
        spectrogramSize="md"
        className="w-full"
      />
    </div>
  </div>
</div>
