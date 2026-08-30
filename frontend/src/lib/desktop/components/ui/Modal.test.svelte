<script lang="ts">
  import Modal from './Modal.svelte';

  let {
    isOpen = false,
    showChildren = false,
    showCustomHeader = false,
    showCustomFooter = false,
    customHeaderTitle = 'Custom Header',
  } = $props();
</script>

{#snippet customHeader(modalTitleId: string)}
  <div>
    <h3 id={modalTitleId} class="text-lg font-bold">{customHeaderTitle}</h3>
    <p class="text-sm text-[var(--color-base-content)]/70">With subtitle</p>
  </div>
{/snippet}

<Modal {isOpen} title="Test Modal" header={showCustomHeader ? customHeader : undefined}>
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
