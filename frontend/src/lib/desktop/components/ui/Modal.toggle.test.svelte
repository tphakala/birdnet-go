<script lang="ts">
  import Modal from './Modal.svelte';

  // Test host for opening and closing two Modals through state the host owns,
  // as a page does. A test harness rerender would reset every prop and re-run
  // each Modal's open effect, which a page never does.
  let lowerOpen = $state(false);
  let upperOpen = $state(false);
</script>

<button type="button"
  >Page button<span tabindex="-1" aria-hidden="true" data-testid="page-button-icon"></span></button
>
<button type="button" disabled>Disabled button</button>
<section tabindex="-1" aria-label="Wrapper"><p>Wrapper text</p></section>
<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<section tabindex="0" aria-label="Region">
  <section tabindex="-1" aria-label="Inner wrapper"><p>Inner wrapper text</p></section>
</section>
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
