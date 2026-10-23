<script lang="ts">
  import Modal from './Modal.svelte';

  // Test host for opening and closing two Modals through state the host owns,
  // as a page does. A test harness rerender would reset every prop and re-run
  // each Modal's open effect, which a page never does.
  let lowerOpen = $state(false);
  let upperOpen = $state(false);
</script>

<button type="button">Page button</button>
<button type="button" onclick={() => (lowerOpen = !lowerOpen)}>Toggle lower</button>
<button type="button" onclick={() => (upperOpen = !upperOpen)}>Toggle upper</button>
<button
  type="button"
  onclick={() => {
    lowerOpen = false;
    upperOpen = false;
  }}>Close both</button
>

<Modal isOpen={lowerOpen} title="Lower modal" showCloseButton={false}>
  {#snippet children()}
    <button type="button">Lower action</button>
  {/snippet}
</Modal>

<Modal isOpen={upperOpen} title="Upper modal" showCloseButton={false}>
  {#snippet children()}
    <button type="button">Upper action</button>
  {/snippet}
</Modal>
