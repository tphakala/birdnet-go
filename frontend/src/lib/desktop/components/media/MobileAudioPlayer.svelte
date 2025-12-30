<script lang="ts">
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import { t } from '$lib/i18n';

  export let audioUrl: string;
  export let speciesName: string = '';
  export let detectionTime: string = '';
  export let detectionId: number | string | undefined = undefined;
  export let showSpectrogram: boolean = true;
  export let onClose: (() => void) | undefined;

  function handleClose() {
    if (onClose) onClose();
  }
</script>

<!-- Mobile bottom-sheet modal wrapping the existing AudioPlayer -->
<div class="fixed inset-0 z-50 bg-black/50 flex items-end md:hidden" role="dialog" aria-modal="true">
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
        <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
          <path fill-rule="evenodd" d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z" clip-rule="evenodd" />
        </svg>
      </button>
    </div>

    <!-- Player Content -->
    <div class="p-3">
      <AudioPlayer
        audioUrl={audioUrl}
        detectionId={detectionId ? String(detectionId) : ''}
        responsive={true}
        showSpectrogram={showSpectrogram}
        showDownload={true}
        spectrogramSize="md"
        className="w-full"
      />
    </div>
  </div>

</div>

<style>
  :global(.mobile-audio-player-open) { overflow: hidden; }
</style>
