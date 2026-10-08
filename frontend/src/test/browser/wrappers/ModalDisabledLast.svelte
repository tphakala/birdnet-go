<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
</script>

<button type="button" data-testid="outside-before">Outside before</button>

<Modal isOpen={true} title="Trap Modal" showCloseButton={false}>
  {#snippet children()}
    <input type="radio" name="mode" value="a" aria-label="Mode A" />
    <input type="radio" name="mode" value="b" aria-label="Mode B" checked />
    <button type="button">First</button>
  {/snippet}

  {#snippet footer()}
    <button type="button">Middle</button>
    <button type="button" disabled>Disabled last</button>
  {/snippet}
</Modal>

<button type="button" data-testid="outside-after">Outside after</button>
