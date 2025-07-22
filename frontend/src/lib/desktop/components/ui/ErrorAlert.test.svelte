<script>
  import ErrorAlert from './ErrorAlert.svelte';

  let { type = 'error', dismissible = false, onDismiss = () => {}, showChildren = false, ...rest } = $props();
</script>

<ErrorAlert {type} {dismissible} {onDismiss} {...rest}>
  {#snippet children()}
    {#if showChildren}
      <span>Custom error content</span> <a href="#">With a link</a>
    {:else}
      <strong>Important:</strong> <span>This is a complex message</span>
    {/if}
  {/snippet}
</ErrorAlert>
