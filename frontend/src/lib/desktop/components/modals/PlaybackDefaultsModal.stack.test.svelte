<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import PlaybackDefaultsModal from './PlaybackDefaultsModal.svelte';

  // Test host: the playback defaults dialog opened from a button inside another
  // open Modal, as a player hosted in a dialog would open it.
  let { onCloseLower = undefined }: { onCloseLower?: () => void } = $props();

  let defaultsOpen = $state(false);
</script>

<Modal isOpen={true} title="Lower modal" showCloseButton={false} onClose={onCloseLower}>
  {#snippet children()}
    <button type="button" onclick={() => (defaultsOpen = true)}>Open defaults</button>
    <PlaybackDefaultsModal
      isOpen={defaultsOpen}
      current={{ gainDb: 0, denoise: '', highPassHz: 20 }}
      onClose={() => (defaultsOpen = false)}
    />
  {/snippet}
</Modal>
