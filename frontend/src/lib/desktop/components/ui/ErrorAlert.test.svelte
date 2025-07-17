<script>
  import ErrorAlert from './ErrorAlert.svelte';

  let { type = 'error', dismissible = false, onDismiss = () => {}, ...rest } = $props();
</script>

<ErrorAlert {type} {dismissible} {onDismiss} {...rest}>
  {#snippet children()}
    {#if rest.showChildren}
      <span>Custom error content</span> <a href="#">With a link</a>
    {:else}
      <strong>Important:</strong> <span>This is a complex message</span>
    {/if}
  {/snippet}
</ErrorAlert>
