<script lang="ts">
  import Modal from './Modal.svelte';

  let {
    isOpen = false,
    showChildren = false,
    showCustomHeader = false,
    showCustomFooter = false,
    scrollBody = false,
    size = 'md',
  }: {
    isOpen?: boolean;
    showChildren?: boolean;
    showCustomHeader?: boolean;
    showCustomFooter?: boolean;
    scrollBody?: boolean;
    size?: 'md' | 'full';
  } = $props();
</script>

<Modal {isOpen} {scrollBody} {size} title="Test Modal">
  {#snippet header({ titleId })}
    {#if showCustomHeader}
      <div>
        <h3 id={titleId} class="text-lg font-bold">Custom Header</h3>
        <p class="text-sm text-[var(--color-base-content)]/70">With subtitle</p>
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
