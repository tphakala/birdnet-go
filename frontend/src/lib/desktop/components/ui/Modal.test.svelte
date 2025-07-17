<script>
  import Modal from './Modal.svelte';

  let {
    isOpen = false,
    showChildren = false,
    showCustomHeader = false,
    showCustomFooter = false,
  } = $props();
</script>

<Modal {isOpen} title="Test Modal">
  {#snippet header()}
    {#if showCustomHeader}
      <div>
        <h3 class="text-lg font-bold">Custom Header</h3>
        <p class="text-sm text-base-content/70">With subtitle</p>
      </div>
    {/if}
  {/snippet}

  {#snippet children()}
    {#if showChildren}
      <p>Custom modal content</p>
    {/if}
  {/snippet}

  {#snippet footer()}
    {#if showCustomFooter}
      <button class="btn btn-primary">Custom Action</button>
    {/if}
  {/snippet}
</Modal>
