<script lang="ts">
  import Modal from './Modal.svelte';

  // Test host for two Modals open at once, as the setup wizard and its leave
  // confirmation are. The second one opens later than the first.
  let { firstOpen = true, secondOpen = false }: { firstOpen?: boolean; secondOpen?: boolean } =
    $props();
</script>

<Modal isOpen={firstOpen} title="Lower modal" showCloseButton={false}>
  {#snippet children()}
    <button type="button">Lower action</button>
  {/snippet}
</Modal>

<Modal isOpen={secondOpen} title="Upper modal" showCloseButton={false}>
  {#snippet children()}
    <button type="button">Upper action</button>
  {/snippet}
</Modal>
