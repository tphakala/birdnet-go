<script>
  import Card from './Card.svelte';

  let { title = '', showHeader = false, showFooter = false } = $props();
</script>

<Card {title}>
  {#snippet header()}
    {#if showHeader}
      <div>Custom Header</div>
    {/if}
  {/snippet}

  {#snippet children()}
    Card content
  {/snippet}

  {#snippet footer()}
    {#if showFooter}
      <div>Footer content</div>
    {/if}
  {/snippet}
</Card>
