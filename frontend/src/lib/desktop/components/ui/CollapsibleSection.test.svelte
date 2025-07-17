<script>
  import CollapsibleSection from './CollapsibleSection.svelte';

  let { defaultOpen = false, showContent = false } = $props();
</script>

<CollapsibleSection title="Test Section" {defaultOpen}>
  {#snippet children()}
    {#if showContent}
      <div>
        <p>Test content</p>
        <p>More content</p>
      </div>
    {/if}
  {/snippet}
</CollapsibleSection>
